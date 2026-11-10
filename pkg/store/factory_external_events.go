package store

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"strconv"

	"github.com/levitateos/factory/pkg/factory"
)

// EnqueueExternalPullEvent maps a provider locator to explicit, retained
// adoption commands. Factory-created PRs and unselected PRs never recurse into
// this path. Existing readiness work supplies dedupe, bounds and restart cursor;
// neither webhook content nor lookup absence creates another execution root.
func (s *Store) EnqueueExternalPullEvent(ctx context.Context,repository,number int64,head,delivery string) (bool,error) {
	if repository<1 || number<0 || delivery=="" || len(delivery)>128 || number==0 && !factory.ValidCommit(head) || head!="" && !factory.ValidCommit(head) { return false,ErrAdmissionChanged }
	tx,err:=s.db.BeginTx(ctx,nil); if err!=nil { return false,err }; defer tx.Rollback()
	rows,err:=tx.QueryContext(ctx,`SELECT data FROM factory_publications WHERE repository=$1 AND data ? 'external' AND stage IN ('published','fenced')
	 AND NOT COALESCE((data->>'withdraw_requested')::boolean,FALSE)
	 AND (($2>0 AND (data->>'pr_number')::bigint=$2) OR ($2=0 AND data->>'candidate'=$3)) ORDER BY seq LIMIT $4`,repository,number,head,storePublicationLimit+1)
	if err!=nil { return false,err }
	var publications []factory.Publication
	for rows.Next() {
		var raw []byte; var p factory.Publication
		if err=rows.Scan(&raw); err!=nil { rows.Close(); return false,err }
		if json.Unmarshal(raw,&p)!=nil || p.Validate()!=nil || p.External==nil { rows.Close(); return false,ErrAdmissionChanged }
		publications=append(publications,p)
	}
	err=rows.Err(); rows.Close(); if err!=nil { return false,err }
	if len(publications)>storePublicationLimit { return false,ErrReadinessCapacity }
	for _,p:=range publications {
		digest:=sha256.Sum256([]byte("external-pr-wake\x00"+strconv.FormatInt(repository,10)+"\x00"+p.ID+"\x00"+delivery))
		key:="external-pr:"+hex.EncodeToString(digest[:])
		if err=enqueueReadinessWorkTx(ctx,tx,key,key,factory.DependenceRef{Repository:repository,Issue:p.Issue}); err!=nil { return false,err }
	}
	if err=tx.Commit(); err!=nil { return false,err }; return len(publications)>0,nil
}
