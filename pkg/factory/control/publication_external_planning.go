package control

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"strconv"
	"time"

	"github.com/levitateos/factory/pkg/collaboration"
	"github.com/levitateos/factory/pkg/factory"
	"github.com/levitateos/factory/pkg/project"
	"github.com/levitateos/factory/pkg/store"
)

func (c *Coordinator) captureExternalOrigin(ctx context.Context,command string,request factory.ExternalPullAdoption,policy factory.RepositoryPolicy) (factory.ExternalPublicationOrigin,error) {
	var empty factory.ExternalPublicationOrigin
	if policy.Revision!=request.Policy || policy.TargetBranch!=request.Target.BaseRef { return empty,store.ErrAdmissionChanged }
	_,environment,err:=c.Store.ExecutionWorkspace(ctx,collaboration.RepositoryID(request.Repository))
	if err!=nil || environment==nil || environment.Profile!=policy.Review.NativeProfile { return empty,store.ErrAdmissionChanged }
	connection,err:=c.Store.CollaborationConnection(ctx,policy.Review.NativeConnection)
	if err!=nil || connection.Profile!=environment.Profile || int64(connection.Actor)!=policy.Review.ActorID { return empty,store.ErrAdmissionChanged }
	repository,err:=c.Store.CollaborationRepository(ctx,collaboration.RepositoryID(request.Repository))
	if err!=nil || repository.Host!=connection.Host { return empty,store.ErrAdmissionChanged }
	t:=request.Target
	observed,err:=c.Reviews.ObserveReview(ctx,factory.ReviewWork{Repository:request.Repository,ActorID:policy.Review.ActorID,PRNumber:request.PRNumber,PRID:request.PRID,IssueID:t.IssueID,PRAuthorID:t.AuthorID,HeadRef:t.HeadRef,BaseRef:t.BaseRef,HeadOID:t.HeadOID,BaseOID:t.BaseOID})
	if err!=nil { return empty,err }
	raw,err:=json.Marshal(observed); if err!=nil { return empty,err }; sum:=sha256.Sum256(raw)
	return factory.ExternalPublicationOrigin{Target:t,CommandID:command,Host:repository.Host,Profile:connection.Profile,Connection:connection.ID,ActorID:policy.Review.ActorID,Policy:policy.Revision,ObservationDigest:hex.EncodeToString(sum[:]),ObservationRevision:observed.NativeRev,ObservedUnix:observed.ObservedUnix},nil
}

// The ordinary Architect packet is the single admission transaction. No
// preparation send occurs before that packet: a stale approved base waits for
// normal source preparation, rather than creating unjournaled setup here.
func (c *Coordinator) planExternalPull(ctx context.Context,cmd factory.Command,request factory.ExternalPullAdoption) (factory.ExternalPullAdmission,error) {
	var empty factory.ExternalPullAdmission
	deps:=c.dispatchDeps()
	deps.PrepareSource=nil
	occupancy,err:=snapshotOccupancy(ctx,c.Store)
	if err!=nil { return empty,err }
	control,err:=c.Store.IssueControl(ctx,request.Repository,request.Issue)
	if err!=nil { return empty,err }
	plan,wait,failed:=planAttemptForRole(ctx,deps,&occupancy,request.Repository,request.Issue,request.Acceptance,control,true,project.RoleCoder,"",false,"",attemptLineage{phase:project.ExecutionInvestigate})
	if wait!=nil || failed!=nil { return empty,store.ErrAdmissionChanged }
	if plan.nativeEnvironment==nil || plan.policy.Revision!=request.Policy || plan.inputs.Tip!=request.Target.BaseOID || plan.prep.Preparation.SourceCommit!=request.Target.BaseOID || plan.sponsorship.Connection!=request.Connection { return empty,store.ErrAdmissionChanged }
	plan.attemptLimits=request.Limits
	plan.planned=request.Limits.ActiveMinutes
	deadline:=time.Now().Add(time.Duration(request.Limits.ActiveMinutes)*time.Minute)
	if deadline.Before(plan.deadline) { plan.deadline=deadline }
	if wait,failed=renderAttemptPrompt(ctx,deps,plan,plan.policy,project.RoleCoder,request.Repository,request.Issue,request.Acceptance,false); wait!=nil || failed!=nil { return empty,store.ErrAdmissionChanged }
	origin,err:=c.captureExternalOrigin(ctx,cmd.ID,request,plan.policy)
	if err!=nil { return empty,err }
	plan.preparationOwner=cmd.ID
	plan.externalPlanning=&factory.ExternalPullPlanning{Request:request,Origin:origin,Principal:cmd.Principal}
	report:=newDispatchReport()
	executeFreshAttempt(ctx,deps,&occupancy,plan,request.Repository,request.Issue,&report,nil)
	// A lost packet/launch reply recovers exactly this command's assignment.
	// A missing row is not permission to manufacture a successful admission.
	return c.externalPullAdmission(ctx,cmd.ID,request)
}

func (c *Coordinator) adoptCompletedExternalPlan(ctx context.Context,a factory.Assignment,report *PublishReport) {
	if a.Validate()!=nil || a.ExternalPlanning==nil || a.Stage!=factory.AssignmentFinished || a.Outcome!=factory.Succeeded || advanceChildPlan(a)==nil || a.Result.PhaseResult.Output.PlanReview=="" { return }
	if _,err:=c.Store.PublicationByAssignment(ctx,a.ID); err==nil { return } else if !errors.Is(err,store.ErrNotFound) { publicationError(report,a.ID,"store_unavailable"); return }
	e:=a.ExternalPlanning
	status,err:=c.AcceptanceStatus(ctx,a.Repository,strconv.FormatInt(a.Issue,10))
	if err!=nil || !status.Valid || status.Acceptance==nil || status.Acceptance.ID!=a.Acceptance { publicationWait(report,a.ID,"external_plan_inputs_changed"); return }
	policy,err:=c.Store.RepositoryPolicy(ctx,a.Repository)
	if err!=nil || policy.Revision!=e.Origin.Policy { publicationWait(report,a.ID,"external_adoption_policy_changed"); return }
	if err=c.admitExternalIssuePlan(ctx,a,policy,*status.Acceptance); err!=nil { publicationWait(report,a.ID,"external_plan_unadmitted"); return }
	// Reobserve for freshness without replacing the original origin/deadline.
	if _,err=c.captureExternalOrigin(ctx,a.ID,e.Request,policy); err!=nil { publicationWait(report,a.ID,"external_pr_changed"); return }
	now:=time.Now()
	p:=factory.Publication{ID:a.ID,AssignmentID:a.ID,ProjectID:a.ProjectID,Role:project.RoleCoder,Authority:a.Authority,Acceptance:a.Acceptance,Preparation:a.Preparation,External:&e.Origin,
		Candidate:e.Request.Target.HeadOID,BaseSHA:a.SourceCommit,TargetBranch:e.Request.Target.BaseRef,PRNumber:e.Request.PRNumber,PRID:e.Request.PRID,Stage:factory.PublicationPublished,Repository:a.Repository,Issue:a.Issue,
		Publish:factory.PublicationOperation{Kind:factory.OpRefPublish},PRCreate:factory.PublicationOperation{Kind:factory.OpPRCreate},CreatedUnix:now.Unix(),FinishedUnix:now.Unix(),Outcome:factory.Succeeded,Reason:factory.PublishReasonLinked}
	if _,err=c.Store.RecordExternalPlanningPublication(ctx,a,p,now); err!=nil { publicationWait(report,a.ID,"external_plan_adoption_pending"); return }
	c.progressAfterPublish(ctx,a.ID)
}

func (c *Coordinator) admitExternalIssuePlan(ctx context.Context,a factory.Assignment,policy factory.RepositoryPolicy,acceptance factory.Acceptance) error {
	attempt:=&attemptPlan{acceptance:acceptance,projectID:a.ProjectID,policy:policy}
	if wait,failed:=readAttemptInputs(ctx,c.dispatchDeps(),attempt,a.Repository,a.Issue); wait!=nil || failed!=nil { return store.ErrStalePlanBindings }
	control,err:=c.Store.IssueControl(ctx,a.Repository,a.Issue)
	if err!=nil || attempt.inputs.Tip!=a.SourceCommit { return store.ErrStalePlanBindings }
	live,err:=livePlanBindings(acceptance,control,a.SourceCommit,a.Preparation)
	if err!=nil { return err }
	_,err=admitInvestigationPlan(ctx,c.Store,a,live)
	return err
}
