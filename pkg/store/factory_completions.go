package store

// Frozen D13 post-completion storage contracts (FW30).
//
// factory_completions holds one row per verified completion, keyed by
// repository generation; the (publication, occurrence) unique key keeps
// recovery re-observation from recording the event twice.
// factory_reassessments holds one row per bounded cycle with its sealed
// evidence, decision receipts, report and adoption cursor. Native graph
// effects stay in factory_intent_generations and are referenced, never
// duplicated. FW31 implements persistence, the atomic completion fence
// and the dispatch gate against these tables; FW32 stages and applies
// per-intent revisions through the cycle journal.

const (
	// TableFactoryCompletions is the unique completion-event owner.
	TableFactoryCompletions = "factory_completions"
	// TableFactoryReassessments is the immutable-cycle owner.
	TableFactoryReassessments = "factory_reassessments"
)

// FactoryCompletionsDDL creates the unique completion-event table. The
// (repository, generation) primary key orders events per repository for
// the pending-event fence; data carries the factory.CompletionEvent.
const FactoryCompletionsDDL = `CREATE TABLE factory_completions(
feed_seq BIGINT NOT NULL UNIQUE DEFAULT nextval(pg_get_serial_sequence('factory_assignments','seq')),
repository BIGINT NOT NULL CHECK(repository>0), generation BIGINT NOT NULL CHECK(generation>=1),
event TEXT NOT NULL CHECK(event ~ '^[a-f0-9]{32}$'),
publication TEXT NOT NULL CHECK(publication ~ '^[a-f0-9]{32}$'),
occurrence TEXT NOT NULL CHECK(char_length(occurrence)>0 AND char_length(occurrence)<=256),
data JSONB NOT NULL,
PRIMARY KEY(repository,generation), UNIQUE(publication,occurrence))`

// FactoryReassessmentsDDL creates the bounded-cycle table. One row per
// cycle keeps sealed evidence, decision receipts, the report and the
// adoption cursor; data carries the factory.ReassessmentCycle plus its
// sealed inventory digest. Cycle rows are append-only with explicit
// Retry succession: updates never rewrite a sealed row, deletes never
// run. FW31 adds the state-transition guard its fence needs.
const FactoryReassessmentsDDL = `CREATE TABLE factory_reassessments(
cycle TEXT PRIMARY KEY CHECK(cycle ~ '^[a-f0-9]{32}$'),
repository BIGINT NOT NULL CHECK(repository>0), generation BIGINT NOT NULL CHECK(generation>=1),
event TEXT NOT NULL CHECK(event ~ '^[a-f0-9]{32}$'),
state TEXT NOT NULL, data JSONB NOT NULL,
FOREIGN KEY(repository,generation) REFERENCES factory_completions(repository,generation))`

// FactoryStallEventsDDL creates the bound-observation table. The
// (repository, generation, fingerprint) primary key replays a
// redetected round to the identical row; episode identity stays
// (repository, generation) by construction of the stored event. The
// seq column records true observation order for the standing query;
// R37 advances progress on verified delivery rather than reading
// recency into a content hash.
const FactoryStallEventsDDL = `CREATE TABLE factory_stall_events(
seq BIGSERIAL UNIQUE NOT NULL,
repository BIGINT NOT NULL CHECK(repository>0), generation BIGINT NOT NULL CHECK(generation>=0),
event TEXT NOT NULL CHECK(event ~ '^[a-f0-9]{32}$'),
episode TEXT NOT NULL CHECK(episode ~ '^[a-f0-9]{32}$'),
fingerprint TEXT NOT NULL CHECK(fingerprint ~ '^[a-f0-9]{64}$'),
data JSONB NOT NULL,
PRIMARY KEY(repository,generation,fingerprint))`

// FactoryCompletionTriggers freezes recorded history: completion events
// never change, and cycle rows are never deleted. FW31 owns the
// reassessment state guard; no trigger here invents it.
const FactoryCompletionTriggers = `CREATE TRIGGER factory_completion_immutable_update BEFORE UPDATE ON factory_completions FOR EACH ROW EXECUTE FUNCTION soda_reject_immutable()`

const FactoryCompletionDeleteTrigger = `CREATE TRIGGER factory_completion_immutable_delete BEFORE DELETE ON factory_completions FOR EACH ROW EXECUTE FUNCTION soda_reject_immutable()`

const FactoryReassessmentDeleteTrigger = `CREATE TRIGGER factory_reassessment_immutable_delete BEFORE DELETE ON factory_reassessments FOR EACH ROW EXECUTE FUNCTION soda_reject_immutable()`
