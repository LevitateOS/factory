package control

import (
	"context"
	"encoding/json"
	"errors"
	"strconv"
	"time"

	"github.com/levitateos/factory/pkg/collaboration"
	"github.com/levitateos/factory/pkg/factory"
	"github.com/levitateos/factory/pkg/project"
	"github.com/levitateos/factory/pkg/store"
)

type externalPullReader interface {
	ReadExternalPullTarget(context.Context,int64,int64) (factory.ReviewObservation,error)
}

// ExternalPullOffer captures a visible proposal only. The returned exact
// head/base, issue plan and source must be submitted unchanged by a fresh
// explicit command; refreshing this proposal carries no spending authority.
func (c *Coordinator) ExternalPullOffer(ctx context.Context,repository int64,selection factory.ExternalPullSelection) (factory.ExternalPullAdoption,error) {
	var empty factory.ExternalPullAdoption
	reader,ok:=c.Reviews.(externalPullReader)
	if !ok || c.Store==nil || selection.Issue<1 || repository<1 || selection.Number<1 { return empty,store.ErrAdmissionChanged }
	policy,err:=c.Store.RepositoryPolicy(ctx,repository)
	if err!=nil { return empty,err }
	observed,err:=reader.ReadExternalPullTarget(ctx,repository,selection.Number)
	if err!=nil { return empty,err }
	if observed.BaseRef!=policy.TargetBranch { return empty,&factory.PublicationRefusal{Reason:"external_issue_plan_base_changed"} }
	status,err:=c.AcceptanceStatus(ctx,repository,strconv.FormatInt(selection.Issue,10))
	if err!=nil || !status.Valid || status.Acceptance==nil { return empty,store.ErrAdmissionChanged }
	request:=factory.ExternalPullAdoption{Policy:policy.Revision,Repository:repository,Issue:selection.Issue,Acceptance:status.Acceptance.ID,PRNumber:selection.Number,PRID:observed.PRID,Connection:policy.Roles[project.RoleCoder].Account,Limits:policy.AttemptLimits,
		Target:factory.PublicationPullTarget{AuthorID:observed.PRAuthorID,IssueID:observed.IssueID,HeadRepository:repository,HeadRef:observed.HeadRef,BaseRef:observed.BaseRef,HeadOID:observed.HeadOID,BaseOID:observed.BaseOID}}
	plan,err:=c.Store.ExternalIssuePlan(ctx,repository,selection.Issue,observed.BaseOID)
	if err==nil {
		if plan.Validate()!=nil { return empty,store.ErrAdmissionChanged }
		if plan.Connection==request.Connection { request.PlanAssignment=plan.ID }
	} else if !errors.Is(err,store.ErrNotFound) { return empty,err }
	return request,request.Validate()
}

// AdoptExternalPull composes current authenticated facts with a new explicit
// command and allowance. Replaying an admitted command reads its own receipt;
// it never reobserves a newer head or renews that command's policy/source.
func (c *Coordinator) AdoptExternalPull(ctx context.Context, command,principal string, request factory.ExternalPullAdoption) (factory.ExternalPullAdmission,error) {
	var empty factory.ExternalPullAdmission
	if c.Store==nil || c.Reviews==nil || c.Host==nil || c.Broker==nil || !factory.ValidID(command) || request.Validate()!=nil { return empty,store.ErrAdmissionChanged }
	payload,err:=json.Marshal(request); if err!=nil { return empty,err }
	cmd:=factory.Command{ID:command,Type:factory.CommandAdoptExternalPull,Target:factory.ExternalPullTarget(request.Repository,request.PRNumber),Principal:principal,Payload:string(payload)}
	cmd.Digest=factory.SettingsDigest(cmd.Type,cmd.Target,cmd.Payload)
	if err=cmd.Validate(); err!=nil { return empty,err }
	if prior,err:=c.Store.FactoryCommand(ctx,command); err==nil {
		if prior.Type!=cmd.Type || prior.Target!=cmd.Target || prior.Principal!=cmd.Principal || prior.Digest!=cmd.Digest || prior.Finished=="" { return empty,store.ErrCommandConflict }
		return c.externalPullAdmission(ctx,command,request)
	} else if !errors.Is(err,store.ErrNotFound) { return empty,err }
	// Neither supported native source connector can launch an external fork
	// yet. Explicit consent is required by the model, but cannot manufacture
	// source custody or grant support. Keep that operation unavailable.
	if request.Target.HeadRepository!=request.Repository { return empty,&factory.PublicationRefusal{Reason:"external_fork_execution_unavailable"} }
	if request.PlanAssignment=="" { return c.planExternalPull(ctx,cmd,request) }
	plan,err:=c.Store.Assignment(ctx,request.PlanAssignment)
	if err!=nil || plan.Validate()!=nil || plan.Repository!=request.Repository || plan.Issue!=request.Issue || plan.Acceptance!=request.Acceptance || plan.Role!=project.RoleCoder || plan.Phase!=project.ExecutionInvestigate || plan.Stage!=factory.AssignmentFinished || plan.Outcome!=factory.Succeeded ||
		plan.Result==nil || plan.Result.PhaseResult==nil || plan.Result.PhaseResult.Output==nil || plan.Result.PhaseResult.Output.Plan==nil || plan.Result.PhaseResult.Output.PlanReview=="" || plan.SourceCommit!=request.Target.BaseOID || plan.Connection!=request.Connection {
		return empty,&factory.PublicationRefusal{Reason:"external_issue_plan_required"}
	}
	policy,err:=c.Store.RepositoryPolicy(ctx,request.Repository)
	if err!=nil { return empty,err }
	if policy.Validate()!=nil || policy.Revision!=request.Policy || policy.TargetBranch!=request.Target.BaseRef || policy.Review.NativeConnection=="" { return empty,store.ErrAdmissionChanged }
	workspace,environment,err:=c.Store.ExecutionWorkspace(ctx,collaboration.RepositoryID(request.Repository))
	if err!=nil || environment==nil || workspace!=plan.ProjectID || environment.Profile!=policy.Review.NativeProfile { return empty,store.ErrAdmissionChanged }
	status,err:=c.AcceptanceStatus(ctx,request.Repository,strconv.FormatInt(request.Issue,10))
	if err!=nil || !status.Valid || status.Acceptance==nil || status.Acceptance.ID!=request.Acceptance { return empty,store.ErrAdmissionChanged }
	if err=c.admitExternalIssuePlan(ctx,plan,policy,*status.Acceptance); err!=nil { return empty,err }
	p:=factory.Publication{ID:command,AssignmentID:command,ProjectID:plan.ProjectID,Role:project.RoleCoder,Authority:plan.Authority,Acceptance:request.Acceptance,Preparation:plan.Preparation,Candidate:request.Target.HeadOID,BaseSHA:request.Target.BaseOID,TargetBranch:request.Target.BaseRef,
		PRNumber:request.PRNumber,PRID:request.PRID,Stage:factory.PublicationPublished,Repository:request.Repository,Issue:request.Issue,Publish:factory.PublicationOperation{Kind:factory.OpRefPublish},PRCreate:factory.PublicationOperation{Kind:factory.OpPRCreate}}
	authority,err:=c.currentPublicationAuthority(ctx,p,true)
	if err!=nil { return empty,err }; p.Authority=authority
	origin,err:=c.captureExternalOrigin(ctx,command,request,policy)
	if err!=nil { return empty,err }
	now:=time.Now()
	p.External,p.CreatedUnix=&origin,now.Unix()
	p.Outcome,p.Reason,p.FinishedUnix=factory.Succeeded,factory.PublishReasonLinked,now.Unix()
	a:=factory.Assignment{ID:command,AttemptRoot:command,PublicationAssignment:command,ProjectID:p.ProjectID,Role:project.RoleCoder,Authority:authority,ActorID:plan.ActorID,Acceptance:p.Acceptance,Preparation:p.Preparation,SourceCommit:p.BaseSHA,Stage:factory.AssignmentRetained,Repository:p.Repository,Issue:p.Issue,NativeRev:status.Revision,CreatedUnix:now.Unix(),
		Subject:factory.ExecutionSubject{Kind:factory.SubjectIssue,Issue:&factory.IssueSubject{Issue:p.Issue,Acceptance:p.Acceptance}},ExternalPublication:&factory.ExternalPublicationOwner{PlanAssignment:plan.ID,Connection:request.Connection,Origin:origin}}
	admitted,err:=c.Store.RecordExternalPublicationCommand(ctx,cmd,request,a,p,now)
	if err!=nil { return empty,err }
	return factory.ExternalPullAdmission{CommandID:command,Request:request,Publication:&admitted},nil
}

func (c *Coordinator) externalPullAdmission(ctx context.Context,command string,request factory.ExternalPullAdoption) (factory.ExternalPullAdmission,error) {
	var empty factory.ExternalPullAdmission
	if p,err:=c.Store.PublicationByID(ctx,command); err==nil {
		if p.Validate()!=nil || p.External==nil || p.External.CommandID!=command { return empty,store.ErrCommandConflict }
		return factory.ExternalPullAdmission{CommandID:command,Request:request,Publication:&p},nil
	} else if !errors.Is(err,store.ErrNotFound) { return empty,err }
	a,err:=c.Store.Assignment(ctx,command)
	if err!=nil { return empty,err }
	if a.Validate()!=nil || a.ExternalPlanning==nil { return empty,store.ErrCommandConflict }
	return factory.ExternalPullAdmission{CommandID:command,Request:request,Planning:&factory.ExternalPullPlanningStatus{Assignment:a.ID,Run:a.Run,Stage:a.Stage}},nil
}
