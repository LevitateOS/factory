package store

import (
	"context"
	"database/sql"
)

// Run evidence is mutable only as observations advance. This additive guard
// leaves existing appliance payloads untouched and protects native identities
// even from other transactional writers of factory_runs.
func initializeFactoryExecutionSchema(ctx context.Context, db *sql.DB) error {
	tx, err := db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	var version int
	if err := tx.QueryRowContext(ctx, `SELECT version FROM schema_version FOR UPDATE`).Scan(&version); err != nil {
		return err
	}
	// Native workspace IDs share the preparation journal, not the appliance
	// Project namespace. Preserve the appliance FK on its generated projection;
	// immutable native ownership below prevents switching an existing row's kind.
	if _, err = tx.ExecContext(ctx, `ALTER TABLE project_preparations ADD COLUMN IF NOT EXISTS appliance_project_id TEXT GENERATED ALWAYS AS (CASE WHEN NULLIF(data->'native','null'::jsonb) IS NULL THEN project_id ELSE NULL END) STORED;
DO $$ BEGIN
 IF NOT EXISTS (SELECT 1 FROM pg_constraint WHERE conrelid='project_preparations'::regclass AND conname='project_preparations_appliance_project_fkey') THEN
  ALTER TABLE project_preparations ADD CONSTRAINT project_preparations_appliance_project_fkey FOREIGN KEY(appliance_project_id) REFERENCES projects(id);
 END IF;
END $$;
ALTER TABLE project_preparations DROP CONSTRAINT IF EXISTS project_preparations_project_id_fkey`); err != nil {
		return err
	}
	if _, err := tx.ExecContext(ctx, `CREATE OR REPLACE FUNCTION soda_guard_run_evidence() RETURNS trigger LANGUAGE plpgsql AS $$
DECLARE prior JSONB; next JSONB; field TEXT; old_process JSONB; new_process JSONB; old_custody JSONB; new_custody JSONB; old_appliance JSONB; new_appliance JSONB;
BEGIN
 prior := NULLIF(OLD.data->'native','null'::jsonb);
 next := NULLIF(NEW.data->'native','null'::jsonb);
 IF prior IS NOT NULL AND NULLIF(OLD.data->'admission','null'::jsonb) IS DISTINCT FROM NULLIF(NEW.data->'admission','null'::jsonb) THEN
  RAISE EXCEPTION 'native run admission is immutable';
 END IF;
 IF (prior IS NULL) IS DISTINCT FROM (next IS NULL) THEN
  RAISE EXCEPTION 'factory runtime kind is immutable';
 END IF;
 old_appliance:=OLD.data->'appliance_invocation'; new_appliance:=NEW.data->'appliance_invocation';
 IF old_appliance IS NOT NULL THEN
  FOREACH field IN ARRAY ARRAY['project','container','unit','invocation','login'] LOOP
   IF coalesce(old_appliance->>field,'')!='' AND old_appliance->field IS DISTINCT FROM new_appliance->field THEN RAISE EXCEPTION 'appliance invocation is immutable'; END IF;
  END LOOP;
  FOREACH field IN ARRAY ARRAY['generation','uid','gid'] LOOP
   IF coalesce((old_appliance->>field)::bigint,0)>0 AND old_appliance->field IS DISTINCT FROM new_appliance->field THEN RAISE EXCEPTION 'appliance invocation is immutable'; END IF;
  END LOOP;
 END IF;
 IF next IS NULL THEN RETURN NEW; END IF;
 IF jsonb_typeof(next)!='object' OR NEW.data ?| ARRAY['image','identity_binding','identity_lease_id','identity_generation','credential_delegated','credential_returned','appliance_invocation'] THEN
  RAISE EXCEPTION 'native run has conflicting appliance provenance';
 END IF;
 IF next->'tool' IS DISTINCT FROM prior->'tool' THEN RAISE EXCEPTION 'captured executable is immutable'; END IF;
 IF coalesce(next->>'intent','') IS DISTINCT FROM coalesce(prior->>'intent','') THEN RAISE EXCEPTION 'native launch intent is immutable'; END IF;
 FOREACH field IN ARRAY ARRAY['trust_mode','profile','instance_id','worktree_id'] LOOP
  IF next#>ARRAY['provenance',field] IS DISTINCT FROM prior#>ARRAY['provenance',field] THEN
   RAISE EXCEPTION 'native runtime identity is immutable';
  END IF;
 END LOOP;
 IF (prior->>'sealed'='true' AND next->>'sealed' IS DISTINCT FROM 'true')
 OR (prior->>'retirement'='retired' AND next->>'retirement' IS DISTINCT FROM 'retired')
 OR (prior->>'retirement'='exited' AND next->>'retirement' IN ('running','not_started')) THEN
  RAISE EXCEPTION 'native retirement cannot regress';
 END IF;
 old_process := NULLIF(prior#>'{provenance,process}','null'::jsonb);
 new_process := NULLIF(next#>'{provenance,process}','null'::jsonb);
 IF old_process IS NOT NULL THEN
  IF (old_process - 'exit_code' - 'exit_signal' - 'started_at' - 'stopped_at') IS DISTINCT FROM (new_process - 'exit_code' - 'exit_signal' - 'started_at' - 'stopped_at')
   OR (old_process->>'started_at')::timestamptz IS DISTINCT FROM (new_process->>'started_at')::timestamptz THEN
   RAISE EXCEPTION 'native process identity is immutable';
  END IF;
  FOREACH field IN ARRAY ARRAY['exit_code','exit_signal'] LOOP
   IF NULLIF(old_process->field,'null'::jsonb) IS NOT NULL AND old_process->field IS DISTINCT FROM new_process->field THEN
    RAISE EXCEPTION 'native process exit cannot change';
   END IF;
  END LOOP;
  IF NULLIF(old_process->>'stopped_at','') IS NOT NULL AND
   (old_process->>'stopped_at')::timestamptz IS DISTINCT FROM (new_process->>'stopped_at')::timestamptz THEN
   RAISE EXCEPTION 'native process stop cannot change';
  END IF;
 ELSIF prior->>'retirement'='retired' AND new_process IS NOT NULL THEN
  RAISE EXCEPTION 'retired run cannot acquire a process';
 END IF;
 IF NULLIF(new_process->>'stopped_at','') IS NOT NULL AND (
  (new_process->>'stopped_at')::timestamptz < (new_process->>'started_at')::timestamptz OR
  (NULLIF(new_process->'exit_code','null'::jsonb) IS NULL AND NULLIF(new_process->'exit_signal','null'::jsonb) IS NULL)) THEN
  RAISE EXCEPTION 'native process stop requires terminal evidence';
 END IF;
 old_custody := prior#>'{provenance,custody}'; new_custody := next#>'{provenance,custody}';
 IF coalesce(old_custody->>'acquisition_id','')!='' THEN
  FOREACH field IN ARRAY ARRAY['acquisition_id','connection','generation'] LOOP
   IF old_custody->field IS DISTINCT FROM new_custody->field THEN RAISE EXCEPTION 'native acquisition is immutable'; END IF;
  END LOOP;
 END IF;
 IF (old_custody->>'state'='released' AND new_custody->>'state' IS DISTINCT FROM 'released')
 OR (prior->>'retirement'='retired' AND prior->>'sealed'='true' AND old_custody->>'state' IN ('released','not_acquired') AND old_custody IS DISTINCT FROM new_custody) THEN
  RAISE EXCEPTION 'native custody cannot regress';
 END IF;
 RETURN NEW;
END $$;
CREATE OR REPLACE TRIGGER factory_run_evidence_immutable BEFORE UPDATE ON factory_runs FOR EACH ROW EXECUTE FUNCTION soda_guard_run_evidence();
CREATE OR REPLACE FUNCTION soda_guard_preparation_evidence() RETURNS trigger LANGUAGE plpgsql AS $$
DECLARE prior JSONB; next JSONB; observed JSONB;
BEGIN
 prior:=NULLIF(OLD.data->'native','null'::jsonb);
 next:=NULLIF(NEW.data->'native','null'::jsonb);
 observed:=NULLIF(NEW.data->'state','null'::jsonb);
 IF prior IS DISTINCT FROM next THEN RAISE EXCEPTION 'preparation runtime identity is immutable'; END IF;
 IF next IS NOT NULL AND observed IS NOT NULL AND
   (observed->'native' IS DISTINCT FROM next OR observed ? 'container') THEN
  RAISE EXCEPTION 'native preparation observation differs from its owner';
 END IF;
 IF next IS NULL AND NULLIF(observed->'native','null'::jsonb) IS NOT NULL THEN
  RAISE EXCEPTION 'appliance preparation acquired native provenance';
 END IF;
 IF coalesce(OLD.data#>>'{state,container}','')!='' AND
   OLD.data#>'{state,container}' IS DISTINCT FROM NEW.data#>'{state,container}' THEN
  RAISE EXCEPTION 'preparation container identity is immutable';
 END IF;
 IF OLD.data#>>'{state,stopped}'='true' AND OLD.data#>>'{state,retirement}'='confirmed' AND
   (NEW.data#>>'{state,stopped}' IS DISTINCT FROM 'true' OR NEW.data#>>'{state,retirement}' IS DISTINCT FROM 'confirmed') THEN
  RAISE EXCEPTION 'preparation retirement cannot regress';
 END IF;
 IF OLD.data#>>'{factory_admission,stopped}'='true' AND OLD.data#>>'{factory_admission,retirement}'='confirmed' AND
   (NEW.data#>>'{factory_admission,stopped}' IS DISTINCT FROM 'true' OR NEW.data#>>'{factory_admission,retirement}' IS DISTINCT FROM 'confirmed') THEN
  RAISE EXCEPTION 'candidate preparation retirement cannot regress';
 END IF;
 RETURN NEW;
END $$;
CREATE OR REPLACE TRIGGER preparation_evidence_immutable BEFORE UPDATE ON project_preparations FOR EACH ROW EXECUTE FUNCTION soda_guard_preparation_evidence();
CREATE OR REPLACE FUNCTION soda_preparation_quiescent(body JSONB, identity TEXT, workspace TEXT, worker_role TEXT)
RETURNS BOOLEAN LANGUAGE sql IMMUTABLE AS $$
 SELECT COALESCE(
  body#>>'{preparation,id}'=identity AND body#>>'{preparation,project}'=workspace AND body#>>'{preparation,role}'=worker_role AND (
   (body#>>'{factory_admission,project}'=workspace AND body#>>'{factory_admission,stopped}'='true' AND body#>>'{factory_admission,retirement}'='confirmed')
   OR (
    body#>>'{state,id}'=identity AND body#>>'{state,project}'=workspace AND
    NULLIF(body->'native','null'::jsonb) IS NOT DISTINCT FROM NULLIF(body#>'{state,native}','null'::jsonb) AND
    (NULLIF(body->'native','null'::jsonb) IS NULL OR NOT (body->'state' ? 'container')) AND (
     (body#>>'{state,stopped}'='true' AND body#>>'{state,ready}'='false' AND body#>>'{state,retirement}'='confirmed' AND
      COALESCE(body#>>'{state,phase}','') IN ('','stopped') AND COALESCE(body#>>'{state,role}','') IN ('',worker_role))
     OR (
      NULLIF(body->'native','null'::jsonb) IS NULL AND body#>>'{state,role}'=worker_role AND
      body#>>'{state,phase}'='ready' AND body#>>'{state,ready}'='true' AND body#>>'{state,stopped}'='false' AND
      body#>>'{state,container}' ~ '^[a-f0-9]{64}$' AND
      body#>>'{state,source_commit}'=body#>>'{preparation,source_commit}' AND
      body#>>'{state,setup_digest}'=body#>>'{preparation,setup_digest}'
     )
    )
   )
  ),FALSE)
$$`); err != nil {
		return err
	}
	return tx.Commit()
}
