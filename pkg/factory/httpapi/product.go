package httpapi

import (
	"context"
	"net/http"

	"github.com/levitateos/factory/pkg/collaboration"
	"github.com/levitateos/factory/pkg/store"
)

// Product composes the shared route owners for one host-bound current account.
// Its identity/current callback replaces group-level identity fields; sources
// and resource authority still belong to the supplied services. Hosts own the
// authenticated channel and must resolve a fresh binding for each request.
type Product struct {
	Store               *store.Store
	Host                collaboration.HostID
	Actor               collaboration.ActorID
	Principal           string
	AuthorName          string
	Current             func(http.ResponseWriter, *http.Request) bool
	Feed                Feed
	Requests            Requests
	Work                Work
	Repositories        Repositories
	Runs                Runs
	Records             Records
	Acceptances         Acceptances
	CollaborationIssues CollaborationIssues
	CollaborationPulls  CollaborationPulls
	PullFiles           CollaborationPullFiles
	PullCommits         CollaborationPullCommits
	Reviews             CollaborationReviews
	Threads             CollaborationThreads
	Checks              CollaborationChecks
	Discussions         CollaborationDiscussions
	HumanComments       HumanComments
	HumanLocks          HumanLocks
	HumanStates         HumanStates
	HumanReviews        HumanReviews
	HumanInlines        HumanInlines
	HumanThreads        HumanThreads
	HumanReviewRequests HumanReviewRequests
	ReviewRequestReads  CollaborationReviewRequests
	HumanMerges         HumanMerges
	HumanEdits          HumanEdits
	HumanIssueCreates   HumanIssueCreates
	HumanBranchCreates  HumanBranchCreates
	HumanPullCreates    HumanPullCreates
	RepositoryCatalog   CollaborationRepositories
	CapabilityReads     CollaborationCapabilities
}

// ProductRoutes owns only route registration. Account identity, service ports and
// current authority are supplied anew by ServeProductHTTP for each request.
type ProductRoutes struct{ mux *http.ServeMux }
type productBindingKey struct{}
type productRoute uint8

const (
	productIdentity productRoute = iota
	productFeed
	productRequests
	productWork
	productRepositories
	productRuns
	productRecords
	productAcceptances
	productCollaborationIssues
	productCollaborationPulls
	productPullFiles
	productPullCommits
	productReviews
	productThreads
	productChecks
	productRepositoryCatalog
	productCapabilityReads
	productDiscussions
	productHumanComments
	productHumanLocks
	productHumanMerges
	productHumanReviewRequests
	productReviewRequestReads
	productHumanThreads
	productHumanInlines
	productHumanReviews
	productHumanStates
	productHumanEdits
	productHumanPullCreates
	productHumanIssueCreates
	productHumanBranchCreates
)

// Handler retains the host-bound interface. Native hosts with changing accounts
// keep ProductRoutes and supply their fresh Product per request instead.
func (s Product) Handler() http.Handler {
	routes := NewProductRoutes()
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		routes.ServeProductHTTP(w, r, s)
	})
}

// ServeProductHTTP binds no authority beyond this request and starts no work.
func (routes *ProductRoutes) ServeProductHTTP(w http.ResponseWriter, r *http.Request, product Product) {
	routes.mux.ServeHTTP(w, r.WithContext(context.WithValue(r.Context(), productBindingKey{}, product)))
}

func NewProductRoutes() *ProductRoutes {
	mux := http.NewServeMux()
	mergeBase := "/api/factory/collaboration/repositories/{repository}/pulls/{number}/merges"
	mux.Handle("POST "+mergeBase, productHumanMerges)
	mux.Handle("GET "+mergeBase+"/commands/{command}", productHumanMerges)
	mux.Handle("POST "+mergeBase+"/commands/{command}/cancel", productHumanMerges)
	reviewerBase := "/api/factory/collaboration/repositories/{repository}/pulls/{number}/review-requests"
	mux.Handle("GET "+reviewerBase, productReviewRequestReads)
	mux.Handle("GET "+reviewerBase+"/lookup", productReviewRequestReads)
	mux.Handle("POST "+reviewerBase, productHumanReviewRequests)
	mux.Handle("GET "+reviewerBase+"/commands/{command}", productHumanReviewRequests)
	mux.Handle("POST "+reviewerBase+"/commands/{command}/cancel", productHumanReviewRequests)
	threadBase := "/api/factory/collaboration/repositories/{repository}/pulls/{number}/thread-resolutions"
	mux.Handle("GET /api/factory/collaboration/repositories/{repository}/pulls/{number}/threads/{thread}/selection", productHumanThreads)
	mux.Handle("POST "+threadBase, productHumanThreads)
	mux.Handle("GET "+threadBase+"/commands/{command}", productHumanThreads)
	mux.Handle("POST "+threadBase+"/commands/{command}/cancel", productHumanThreads)
	inlineBase := "/api/factory/collaboration/repositories/{repository}/pulls/{number}/inline-comments"
	mux.Handle("POST "+inlineBase, productHumanInlines)
	mux.Handle("GET "+inlineBase+"/commands/{command}", productHumanInlines)
	mux.Handle("POST "+inlineBase+"/commands/{command}/cancel", productHumanInlines)
	reviewBase := "/api/factory/collaboration/repositories/{repository}/pulls/{number}/reviews"
	mux.Handle("POST "+reviewBase, productHumanReviews)
	mux.Handle("GET "+reviewBase+"/commands/{command}", productHumanReviews)
	mux.Handle("POST "+reviewBase+"/commands/{command}/cancel", productHumanReviews)
	pullCreateBase := "/api/factory/collaboration/repositories/{repository}/pulls"
	pullCreateCommand := "/api/factory/collaboration/repositories/{repository}/pull-creation/commands/{command}"
	mux.Handle("POST "+pullCreateBase, productHumanPullCreates)
	mux.Handle("GET "+pullCreateCommand, productHumanPullCreates)
	mux.Handle("POST "+pullCreateCommand+"/cancel", productHumanPullCreates)
	createBase := "/api/factory/collaboration/repositories/{repository}/issues"
	branchCreateBase := "/api/factory/collaboration/repositories/{repository}/branches"
	branchCreateCommand := "/api/factory/collaboration/repositories/{repository}/branch-creation/commands/{command}"
	mux.Handle("POST "+branchCreateBase, productHumanBranchCreates)
	mux.Handle("GET "+branchCreateCommand, productHumanBranchCreates)
	mux.Handle("POST "+branchCreateCommand+"/cancel", productHumanBranchCreates)
	createCommand := "/api/factory/collaboration/repositories/{repository}/issue-creation/commands/{command}"
	mux.Handle("POST "+createBase, productHumanIssueCreates)
	mux.Handle("GET "+createCommand, productHumanIssueCreates)
	mux.Handle("POST "+createCommand+"/cancel", productHumanIssueCreates)
	mux.Handle("/api/factory/collaboration/repositories/", productCollaborationIssues)
	mux.Handle("/api/factory/collaboration/repositories", productRepositoryCatalog)
	mux.Handle("/api/factory/collaboration/repositories/{repository}/capabilities", productCapabilityReads)
	mux.Handle("/api/factory/collaboration/repositories/{repository}/issues/{number}/capabilities", productCapabilityReads)
	mux.Handle("/api/factory/collaboration/repositories/{repository}/pulls/{number}/capabilities", productCapabilityReads)
	mux.Handle("/api/factory/collaboration/repositories/{repository}/pulls", productCollaborationPulls)
	mux.Handle("/api/factory/collaboration/repositories/{repository}/pulls/{number}", productCollaborationPulls)
	mux.Handle("/api/factory/collaboration/repositories/{repository}/pulls/{number}/files", productPullFiles)
	mux.Handle("/api/factory/collaboration/repositories/{repository}/pulls/{number}/commits", productPullCommits)
	mux.Handle("/api/factory/collaboration/repositories/{repository}/pulls/{number}/checks", productChecks)
	mux.Handle("/api/factory/collaboration/repositories/{repository}/pulls/{number}/checks/jobs", productChecks)
	mux.Handle("/api/factory/collaboration/repositories/{repository}/pulls/{number}/threads", productThreads)
	mux.Handle("/api/factory/collaboration/repositories/{repository}/pulls/{number}/threads/{thread}", productThreads)
	mux.Handle("/api/factory/collaboration/repositories/{repository}/pulls/{number}/threads/{thread}/comments", productThreads)
	mux.Handle("/api/factory/collaboration/repositories/{repository}/pulls/{number}/reviews", productReviews)
	mux.Handle("/api/factory/collaboration/repositories/{repository}/pulls/{number}/reviews/{review}", productReviews)
	mux.Handle("/api/factory/collaboration/repositories/{repository}/issues/{number}/comments", productDiscussions)
	mux.Handle("/api/factory/collaboration/repositories/{repository}/pulls/{number}/comments", productDiscussions)
	for _, kind := range []string{"issues", "pulls"} {
		base := "/api/factory/collaboration/repositories/{repository}/" + kind + "/{number}/comments"
		mux.Handle("POST "+base, productHumanComments)
		mux.Handle("GET "+base+"/commands/{command}", productHumanComments)
		mux.Handle("POST "+base+"/commands/{command}/cancel", productHumanComments)
		editBase := "/api/factory/collaboration/repositories/{repository}/" + kind + "/{number}/edits"
		mux.Handle("POST "+editBase, productHumanEdits)
		mux.Handle("GET "+editBase+"/commands/{command}", productHumanEdits)
		mux.Handle("POST "+editBase+"/commands/{command}/cancel", productHumanEdits)
		lockBase := "/api/factory/collaboration/repositories/{repository}/" + kind + "/{number}/locks"
		mux.Handle("POST "+lockBase, productHumanLocks)
		mux.Handle("GET "+lockBase+"/commands/{command}", productHumanLocks)
		mux.Handle("POST "+lockBase+"/commands/{command}/cancel", productHumanLocks)
		stateBase := "/api/factory/collaboration/repositories/{repository}/" + kind + "/{number}/state"
		mux.Handle("POST "+stateBase, productHumanStates)
		mux.Handle("GET "+stateBase+"/commands/{command}", productHumanStates)
		mux.Handle("POST "+stateBase+"/commands/{command}/cancel", productHumanStates)
	}
	mux.Handle("/api/factory/identity", productIdentity)
	mux.Handle("/api/factory/repositories", productFeed)
	mux.Handle("/api/factory/feed", productFeed)
	mux.Handle("/api/factory/feed/", productFeed)
	mux.Handle("/api/factory/feed/posts/{id}/review", productRequests)
	mux.Handle("/api/factory/departments", productRequests)
	mux.Handle("/api/factory/intents", productRequests)
	mux.Handle("/api/factory/intents/", productRequests)
	mux.Handle("/api/factory/capacity", productRepositories)
	mux.Handle("/api/factory/runs/", productRuns)
	mux.Handle("/api/factory/publications/", productRuns)
	mux.Handle("/api/factory/library", productRecords)
	mux.Handle("/api/factory/library/", productRecords)
	mux.Handle("/api/factory/artifacts/", productRecords)
	base := "/api/repositories/{repositoryID}/factory"
	mux.Handle(base, productRepositories)
	mux.Handle(base+"/", productRepositories)
	mux.Handle(base+"/actions", productRuns)
	issue := base + "/issues/{issueID}"
	mux.Handle(base+"/issues/progress", productWork)
	mux.Handle(issue, productWork)
	mux.Handle(issue+"/", productWork)
	mux.Handle(issue+"/acceptance-preview", productAcceptances)
	mux.Handle(issue+"/acceptances", productAcceptances)
	mux.Handle(issue+"/withdrawal", productAcceptances)
	return &ProductRoutes{mux: mux}
}

func (route productRoute) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	s, _ := r.Context().Value(productBindingKey{}).(Product)
	current := func(w http.ResponseWriter, r *http.Request) bool {
		return currentAccount(w, r, s.Host, s.Actor, s.Current)
	}
	if !current(w, r) {
		return
	}
	if s.Store == nil {
		writeError(w, 503, "store_unavailable", "Factory product storage is unavailable.")
		return
	}
	serve := func(handler http.Handler, stores ...*store.Store) {
		for _, selected := range stores {
			if selected != nil && selected != s.Store {
				writeError(w, 503, "store_unavailable", "Factory product storage is unavailable.")
				return
			}
		}
		handler.ServeHTTP(w, r)
	}
	switch route {
	case productIdentity:
		if r.Method != http.MethodGet {
			w.Header().Set("Allow", http.MethodGet)
			writeError(w, 405, "method_not_allowed", "HTTP method not supported.")
			return
		}
		if r.URL.RawQuery != "" || r.URL.ForceQuery {
			writeError(w, 400, "invalid_request", "No query parameters are accepted.")
			return
		}
		if current(w, r) {
			writeJSON(w, 200, struct {
				Host  collaboration.HostID  `json:"host"`
				Actor collaboration.ActorID `json:"actor,string"`
			}{s.Host, s.Actor})
		}
	case productFeed:
		feed := s.Feed
		feed.Host, feed.Actor, feed.AuthorName, feed.Current = s.Host, s.Actor, s.AuthorName, current
		serve(feed.Handler(), feed.Service.Store, feed.Posts.Store, feed.Commands.Posts.Store)
	case productRequests:
		requests := s.Requests
		requests.Host, requests.Actor, requests.Principal, requests.Current = s.Host, s.Actor, s.Principal, current
		serve(requests.Handler(), requests.Service.Store, requests.Posts.Store, requests.Planning.Requests.Store, requests.Exploration.Requests.Store)
	case productWork:
		work := s.Work
		work.Host, work.Actor, work.Current = s.Host, s.Actor, current
		serve(work.Handler(), work.Reads.Store, work.Issues.Store)
	case productRepositories:
		repositories := s.Repositories
		repositories.Host, repositories.Actor, repositories.Principal, repositories.Current = s.Host, s.Actor, s.Principal, current
		serve(repositories.Handler(), repositories.Store, repositories.Status.Store, repositories.Access.Store, repositories.Activity.Store, repositories.Sponsorships.Store, repositories.Repair.Store)
	case productRuns:
		runs := s.Runs
		runs.Host, runs.Actor, runs.Principal, runs.Current = s.Host, s.Actor, s.Principal, current
		serve(runs.Handler(), runs.Store, runs.Status.Store, runs.Commands.Store)
	case productRecords:
		records := s.Records
		records.Host, records.Actor, records.Current = s.Host, s.Actor, current
		serve(records.Handler())
	case productAcceptances:
		acceptances := s.Acceptances
		acceptances.Host, acceptances.Actor, acceptances.Current = s.Host, s.Actor, current
		serve(acceptances.Handler())
	case productCollaborationIssues:
		issues := s.CollaborationIssues
		issues.Host, issues.Actor, issues.Current = s.Host, s.Actor, current
		serve(issues.Handler())
	case productCollaborationPulls:
		pulls := s.CollaborationPulls
		pulls.Host, pulls.Actor, pulls.Current = s.Host, s.Actor, current
		serve(pulls.Handler())
	case productPullFiles:
		files := s.PullFiles
		files.Host, files.Actor, files.Current = s.Host, s.Actor, current
		serve(files.Handler())
	case productPullCommits:
		commits := s.PullCommits
		commits.Host, commits.Actor, commits.Current = s.Host, s.Actor, current
		serve(commits.Handler())
	case productReviews:
		reviews := s.Reviews
		reviews.Host, reviews.Actor, reviews.Current = s.Host, s.Actor, current
		serve(reviews.Handler())
	case productThreads:
		threads := s.Threads
		threads.Host, threads.Actor, threads.Current = s.Host, s.Actor, current
		serve(threads.Handler())
	case productChecks:
		checks := s.Checks
		checks.Host, checks.Actor, checks.Current = s.Host, s.Actor, current
		serve(checks.Handler())
	case productRepositoryCatalog:
		catalog := s.RepositoryCatalog
		catalog.Host, catalog.Actor, catalog.Current = s.Host, s.Actor, current
		serve(catalog.Handler())
	case productCapabilityReads:
		capabilities := s.CapabilityReads
		capabilities.Host, capabilities.Actor, capabilities.Current = s.Host, s.Actor, current
		serve(capabilities.Handler())
	case productDiscussions:
		discussions := s.Discussions
		discussions.Host, discussions.Actor, discussions.Current = s.Host, s.Actor, current
		serve(discussions.Handler())
	case productHumanComments:
		comments := s.HumanComments
		comments.Host, comments.Actor, comments.Current = s.Host, s.Actor, current
		serve(comments.Handler(), comments.Service.Store)
	case productHumanLocks:
		locks := s.HumanLocks
		locks.Host, locks.Actor, locks.Current = s.Host, s.Actor, current
		serve(locks.Handler(), locks.Service.Store)
	case productHumanMerges:
		merges := s.HumanMerges
		merges.Host, merges.Actor, merges.Current = s.Host, s.Actor, current
		serve(merges.Handler(), merges.Service.Store)
	case productHumanReviewRequests:
		reviewerRequests := s.HumanReviewRequests
		reviewerRequests.Host, reviewerRequests.Actor, reviewerRequests.Current = s.Host, s.Actor, current
		serve(reviewerRequests.Handler(), reviewerRequests.Service.Store)
	case productReviewRequestReads:
		reviewRequestReads := s.ReviewRequestReads
		reviewRequestReads.Host, reviewRequestReads.Actor, reviewRequestReads.Current = s.Host, s.Actor, current
		serve(reviewRequestReads.Handler())
	case productHumanThreads:
		humanThreads := s.HumanThreads
		humanThreads.Host, humanThreads.Actor, humanThreads.Current = s.Host, s.Actor, current
		serve(humanThreads.Handler(), humanThreads.Service.Store)
	case productHumanInlines:
		inlines := s.HumanInlines
		inlines.Host, inlines.Actor, inlines.Current = s.Host, s.Actor, current
		serve(inlines.Handler(), inlines.Service.Store)
	case productHumanReviews:
		humanReviews := s.HumanReviews
		humanReviews.Host, humanReviews.Actor, humanReviews.Current = s.Host, s.Actor, current
		serve(humanReviews.Handler(), humanReviews.Service.Store)
	case productHumanStates:
		states := s.HumanStates
		states.Host, states.Actor, states.Current = s.Host, s.Actor, current
		serve(states.Handler(), states.Service.Store)
	case productHumanEdits:
		edits := s.HumanEdits
		edits.Host, edits.Actor, edits.Current = s.Host, s.Actor, current
		serve(edits.Handler(), edits.Service.Store)
	case productHumanPullCreates:
		pullCreates := s.HumanPullCreates
		pullCreates.Host, pullCreates.Actor, pullCreates.Current = s.Host, s.Actor, current
		serve(pullCreates.Handler(), pullCreates.Service.Store)
	case productHumanIssueCreates:
		creates := s.HumanIssueCreates
		creates.Host, creates.Actor, creates.Current = s.Host, s.Actor, current
		serve(creates.Handler(), creates.Service.Store)
	case productHumanBranchCreates:
		branchCreates := s.HumanBranchCreates
		branchCreates.Host, branchCreates.Actor, branchCreates.Current = s.Host, s.Actor, current
		serve(branchCreates.Handler(), branchCreates.Service.Store)
	}
}
