package runtime

import (
	"context"
	"encoding/json"
	"errors"
	"strconv"
	"strings"

	"github.com/levitateos/factory/pkg/collaboration"
	"github.com/levitateos/factory/pkg/factory"
)

// Public issue enrollment uses the host's existing human-operation journal.
// Its causal receipt is distinct from GitHub GraphQL and privileged controller
// publication. No SDK or host authority enters the standalone workflow owner.
type nativeFountainIssueResult struct {
	RepositoryID int64 `json:"repository_id"`
	ActorID int64 `json:"actor_id"`
	IssueID int64 `json:"issue_id"`
	SubjectID int64 `json:"subject_id"`
	Subject string `json:"subject"`
	Index int64 `json:"index"`
	CommentID int64 `json:"comment_id"`
	Title string `json:"title"`
	BodyDigest string `json:"body_digest"`
	ContentVersion int64 `json:"content_version"`
	TitleCommentID int64 `json:"title_comment_id"`
	IsClosed bool `json:"is_closed"`
	ClosedUnix int64 `json:"closed_unix"`
	IsLocked bool `json:"is_locked"`
	ReasonDigest string `json:"reason_digest"`
}

func validateFountainIssueReceipt(raw json.RawMessage, authority, command, account, repository, kind string, write any) (nativeFountainIssueResult,error) {
	var result nativeFountainIssueResult
	if !strings.HasPrefix(authority,"native-fountain:") || len(raw)>4096 { return result,errNativeReadScope }
	var envelope struct {
		Enrollment string `json:"enrollment"`
		Command string `json:"command_id"`
		IntentDigest string `json:"intent_digest"`
		Record struct {
			ControllerGrantID string `json:"controller_grant_id,omitempty"`
			InstallationID string `json:"installation_id"`
			OperationID string `json:"operation_id"`
			Outcome string `json:"outcome"`
			Reason string `json:"reason_code,omitempty"`
			ActorID string `json:"actor_id"`
			RepositoryID string `json:"repository_id"`
			Kind string `json:"kind"`
			Effect string `json:"effect_state"`
			Cancellation string `json:"cancellation_status"`
			Completion string `json:"completion_state"`
			IntentDigest string `json:"intent_digest"`
			Receipt json.RawMessage `json:"receipt"`
		} `json:"record"`
	}
	var fields map[string]json.RawMessage
	if json.Unmarshal(raw,&fields)!=nil || nativeDecode(fields["fountain_issue"],&envelope)!=nil { return result,errNativeRead }
	var response nativeCreationResponse
	if nativeDecode(fields["provider_response"],&response)!=nil || response.Protocol!="fountain_desktop_issue" || response.Status!=200 ||
		!nativeCreationDigest(response.ResponseSHA256) || response.RequestID!=nil && !nativeObservedText(*response.RequestID,256,false) {
		return result,errNativeRead
	}
	record:=envelope.Record
	wantKind:=map[string]string{"create":"issue.create","comment":"discussion.comment.create","edit":"discussion.edit","state":"discussion.state.apply","lock":"discussion.lock.apply"}[kind]
	if wantKind=="" || !factory.ValidID(envelope.Enrollment) || envelope.Command!=command || !factory.ValidDigest(envelope.IntentDigest) ||
		record.ControllerGrantID!="" || !nativeObservedText(record.InstallationID,128,false) || record.OperationID!=command || record.ActorID!=account || record.RepositoryID!=repository ||
		record.Kind!=wantKind || record.IntentDigest!=envelope.IntentDigest || record.Effect!="committed" || record.Completion!="complete" ||
		nativeDecode(record.Receipt,&result)!=nil || strconv.FormatInt(result.RepositoryID,10)!=repository || strconv.FormatInt(result.ActorID,10)!=account || result.IssueID<1 || result.Index<1 {
		return result,errNativeReadScope
	}
	encoded,err:=json.Marshal(write)
	if err!=nil { return result,err }
	var q struct {
		Repository string `json:"repository"`; Subject string `json:"subject"`; SubjectID string `json:"subject_id"`; Number uint64 `json:"number"`
		Title *string `json:"title"`; Body *string `json:"body"`; Close bool `json:"close"`; Lock bool `json:"lock"`; Reason string `json:"reason"`
		OriginalTitle string `json:"original_title"`; OriginalBodySHA256 string `json:"original_body_sha256"`; RequireUnchanged bool `json:"require_unchanged"`
	}
	if nativeDecode(encoded,&q)!=nil || q.Repository!=repository { return result,errNativeReadScope }
	if kind!="create" && (q.Subject!="issue" || result.Subject!="issue" || result.SubjectID!=result.IssueID || q.SubjectID!=strconv.FormatInt(result.IssueID,10) || q.Number!=uint64(result.Index)) { return result,errNativeReadScope }
	switch kind {
	case "create":
		if q.Title==nil || q.Body==nil || result.Title!=*q.Title || result.BodyDigest!=nativeCreationSHA([]byte(*q.Body)) { return result,errNativeRead }
	case "comment":
		var comment struct { BodySHA256 string `json:"body_sha256"`; ExternalID string `json:"external_id"` }
		if json.Unmarshal(fields["comment"],&comment)!=nil || q.Body==nil || result.CommentID<1 || comment.ExternalID!=strconv.FormatInt(result.CommentID,10) || comment.BodySHA256!=nativeCreationSHA([]byte(*q.Body)) { return result,errNativeRead }
	case "edit":
		title,bodyDigest:=q.OriginalTitle,q.OriginalBodySHA256
		if q.Title!=nil { title=*q.Title }
		if q.Body!=nil { bodyDigest=nativeCreationSHA([]byte(*q.Body)) }
		if result.Title!=title || result.BodyDigest!=bodyDigest || result.ContentVersion<0 { return result,errNativeRead }
	case "state":
		if result.IsClosed!=q.Close || result.CommentID<1 || q.Close && result.ClosedUnix<=0 || !q.Close && result.ClosedUnix!=0 { return result,errNativeRead }
	case "lock":
		if result.IsLocked!=q.Lock || result.CommentID<1 || result.ReasonDigest!=nativeCreationSHA([]byte(q.Reason)) { return result,errNativeRead }
	}
	return result,nil
}

func fountainIssueOutcome(record collaboration.OperationRecord, intent json.RawMessage, account, repository, kind string, write any, reply nativeCreationReply) (collaboration.OperationRecord,error) {
	if reply.State=="created" && (kind=="create" || kind=="comment") || reply.State=="applied" && kind!="create" && kind!="comment" {
		if reply.Failure!=nil { return collaboration.OperationRecord{},errNativeRead }
		if _,err:=validateFountainIssueReceipt(reply.Receipt,record.Authority,record.OperationID,account,repository,kind,write); err!=nil { return collaboration.OperationRecord{},err }
		record.Effect,record.Completion,record.Reason=collaboration.EffectCommitted,collaboration.CompletionComplete,"native_issue_collaboration"
		record.Receipt=&collaboration.ProviderReceipt{Authority:record.Authority,Reference:record.OperationID,IntentDigest:nativeCreationSHA(intent),Payload:reply.Receipt}
	} else if !reply.applyFailure(&record) { return collaboration.OperationRecord{},errors.New("native issue outcome unavailable") }
	return record,record.Validate()
}

func (s *nativeProductAccount) recoverFountainIssue(ctx context.Context, cmd factory.Command, notAfter int64, repository string,
	prior collaboration.OperationLookup, record collaboration.OperationRecord, intent json.RawMessage, account, kind string, write any,
	observe func(collaboration.OperationLookup)(collaboration.OperationLookup,error)) (collaboration.OperationLookup,error) {
	if s.binding.Host.Provider!=collaboration.Fountain { return prior,nil }
	return s.recoverHumanEffect(ctx,cmd,notAfter,repository,prior,func(reply nativeCreationReply)(collaboration.OperationRecord,error){
		return fountainIssueOutcome(record,intent,account,repository,kind,write,reply)
	},observe)
}
