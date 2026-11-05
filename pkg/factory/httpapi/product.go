package httpapi

import (
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
	HumanPullCreates    HumanPullCreates
	RepositoryCatalog   CollaborationRepositories
	CapabilityReads     CollaborationCapabilities
}

// Handler starts no listener, provider, background drive or account enrollment.
// Hosts keep output upgrade/peer ownership and worker MCP on their own channels.
func (s Product) Handler() http.Handler {
	current := func(w http.ResponseWriter, r *http.Request) bool {
		return currentAccount(w, r, s.Host, s.Actor, s.Current)
	}
	feed := s.Feed
	feed.Host, feed.Actor, feed.AuthorName, feed.Current = s.Host, s.Actor, s.AuthorName, current
	requests := s.Requests
	requests.Host, requests.Actor, requests.Principal, requests.Current = s.Host, s.Actor, s.Principal, current
	work := s.Work
	work.Host, work.Actor, work.Current = s.Host, s.Actor, current
	repositories := s.Repositories
	repositories.Host, repositories.Actor, repositories.Principal, repositories.Current = s.Host, s.Actor, s.Principal, current
	runs := s.Runs
	runs.Host, runs.Actor, runs.Principal, runs.Current = s.Host, s.Actor, s.Principal, current
	records := s.Records
	records.Host, records.Actor, records.Current = s.Host, s.Actor, current
	acceptances := s.Acceptances
	acceptances.Host, acceptances.Actor, acceptances.Current = s.Host, s.Actor, current
	issues := s.CollaborationIssues
	issues.Host, issues.Actor, issues.Current = s.Host, s.Actor, current
	pulls := s.CollaborationPulls
	pulls.Host, pulls.Actor, pulls.Current = s.Host, s.Actor, current
	files := s.PullFiles
	files.Host, files.Actor, files.Current = s.Host, s.Actor, current
	reviews := s.Reviews
	reviews.Host, reviews.Actor, reviews.Current = s.Host, s.Actor, current
	threads := s.Threads
	threads.Host, threads.Actor, threads.Current = s.Host, s.Actor, current
	checks := s.Checks
	checks.Host, checks.Actor, checks.Current = s.Host, s.Actor, current
	catalog := s.RepositoryCatalog
	catalog.Host, catalog.Actor, catalog.Current = s.Host, s.Actor, current
	capabilities := s.CapabilityReads
	capabilities.Host, capabilities.Actor, capabilities.Current = s.Host, s.Actor, current
	discussions := s.Discussions
	discussions.Host, discussions.Actor, discussions.Current = s.Host, s.Actor, current
	comments := s.HumanComments
	comments.Host, comments.Actor, comments.Current = s.Host, s.Actor, current
	locks := s.HumanLocks
	locks.Host, locks.Actor, locks.Current = s.Host, s.Actor, current
	merges := s.HumanMerges
	merges.Host, merges.Actor, merges.Current = s.Host, s.Actor, current
	reviewerRequests := s.HumanReviewRequests
	reviewerRequests.Host, reviewerRequests.Actor, reviewerRequests.Current = s.Host, s.Actor, current
	reviewRequestReads := s.ReviewRequestReads
	reviewRequestReads.Host, reviewRequestReads.Actor, reviewRequestReads.Current = s.Host, s.Actor, current
	humanThreads := s.HumanThreads
	humanThreads.Host, humanThreads.Actor, humanThreads.Current = s.Host, s.Actor, current
	inlines := s.HumanInlines
	inlines.Host, inlines.Actor, inlines.Current = s.Host, s.Actor, current
	humanReviews := s.HumanReviews
	humanReviews.Host, humanReviews.Actor, humanReviews.Current = s.Host, s.Actor, current
	states := s.HumanStates
	states.Host, states.Actor, states.Current = s.Host, s.Actor, current
	edits := s.HumanEdits
	edits.Host, edits.Actor, edits.Current = s.Host, s.Actor, current
	pullCreates := s.HumanPullCreates
	pullCreates.Host, pullCreates.Actor, pullCreates.Current = s.Host, s.Actor, current
	creates := s.HumanIssueCreates
	creates.Host, creates.Actor, creates.Current = s.Host, s.Actor, current
	guard := func(handler http.Handler, stores ...*store.Store) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			if !current(w, r) {
				return
			}
			if s.Store == nil {
				writeError(w, 503, "store_unavailable", "Factory product storage is unavailable.")
				return
			}
			for _, selected := range stores {
				if selected != nil && selected != s.Store {
					writeError(w, 503, "store_unavailable", "Factory product storage is unavailable.")
					return
				}
			}
			handler.ServeHTTP(w, r)
		})
	}
	feedHTTP := guard(feed.Handler(), feed.Service.Store, feed.Posts.Store, feed.Commands.Posts.Store)
	requestHTTP := guard(requests.Handler(), requests.Service.Store, requests.Posts.Store, requests.Planning.Requests.Store, requests.Exploration.Requests.Store)
	workHTTP := guard(work.Handler(), work.Reads.Store, work.Issues.Store)
	repositoryHTTP := guard(repositories.Handler(), repositories.Store, repositories.Status.Store, repositories.Access.Store, repositories.Activity.Store, repositories.Sponsorships.Store, repositories.Repair.Store)
	runHTTP := guard(runs.Handler(), runs.Store, runs.Status.Store, runs.Commands.Store)
	recordHTTP := guard(records.Handler()) // Source interfaces retain their own canonical scope checks.
	acceptanceHTTP := guard(acceptances.Handler())
	mux := http.NewServeMux()
	mergeBase := "/api/factory/collaboration/repositories/{repository}/pulls/{number}/merges"
	mux.Handle("POST "+mergeBase, guard(merges.Handler(), merges.Service.Store))
	mux.Handle("GET "+mergeBase+"/commands/{command}", guard(merges.Handler(), merges.Service.Store))
	mux.Handle("POST "+mergeBase+"/commands/{command}/cancel", guard(merges.Handler(), merges.Service.Store))
	reviewerBase := "/api/factory/collaboration/repositories/{repository}/pulls/{number}/review-requests"
	mux.Handle("GET "+reviewerBase, guard(reviewRequestReads.Handler()))
	mux.Handle("POST "+reviewerBase, guard(reviewerRequests.Handler(), reviewerRequests.Service.Store))
	mux.Handle("GET "+reviewerBase+"/commands/{command}", guard(reviewerRequests.Handler(), reviewerRequests.Service.Store))
	mux.Handle("POST "+reviewerBase+"/commands/{command}/cancel", guard(reviewerRequests.Handler(), reviewerRequests.Service.Store))
	threadBase := "/api/factory/collaboration/repositories/{repository}/pulls/{number}/thread-resolutions"
	mux.Handle("GET /api/factory/collaboration/repositories/{repository}/pulls/{number}/threads/{thread}/selection", guard(humanThreads.Handler(), humanThreads.Service.Store))
	mux.Handle("POST "+threadBase, guard(humanThreads.Handler(), humanThreads.Service.Store))
	mux.Handle("GET "+threadBase+"/commands/{command}", guard(humanThreads.Handler(), humanThreads.Service.Store))
	mux.Handle("POST "+threadBase+"/commands/{command}/cancel", guard(humanThreads.Handler(), humanThreads.Service.Store))
	inlineBase := "/api/factory/collaboration/repositories/{repository}/pulls/{number}/inline-comments"
	mux.Handle("POST "+inlineBase, guard(inlines.Handler(), inlines.Service.Store))
	mux.Handle("GET "+inlineBase+"/commands/{command}", guard(inlines.Handler(), inlines.Service.Store))
	mux.Handle("POST "+inlineBase+"/commands/{command}/cancel", guard(inlines.Handler(), inlines.Service.Store))
	reviewBase := "/api/factory/collaboration/repositories/{repository}/pulls/{number}/reviews"
	mux.Handle("POST "+reviewBase, guard(humanReviews.Handler(), humanReviews.Service.Store))
	mux.Handle("GET "+reviewBase+"/commands/{command}", guard(humanReviews.Handler(), humanReviews.Service.Store))
	mux.Handle("POST "+reviewBase+"/commands/{command}/cancel", guard(humanReviews.Handler(), humanReviews.Service.Store))
	pullCreateBase := "/api/factory/collaboration/repositories/{repository}/pulls"
	pullCreateCommand := "/api/factory/collaboration/repositories/{repository}/pull-creation/commands/{command}"
	mux.Handle("POST "+pullCreateBase, guard(pullCreates.Handler(), pullCreates.Service.Store))
	mux.Handle("GET "+pullCreateCommand, guard(pullCreates.Handler(), pullCreates.Service.Store))
	mux.Handle("POST "+pullCreateCommand+"/cancel", guard(pullCreates.Handler(), pullCreates.Service.Store))
	createBase := "/api/factory/collaboration/repositories/{repository}/issues"
	createCommand := "/api/factory/collaboration/repositories/{repository}/issue-creation/commands/{command}"
	mux.Handle("POST "+createBase, guard(creates.Handler(), creates.Service.Store))
	mux.Handle("GET "+createCommand, guard(creates.Handler(), creates.Service.Store))
	mux.Handle("POST "+createCommand+"/cancel", guard(creates.Handler(), creates.Service.Store))
	mux.Handle("/api/factory/collaboration/repositories/", guard(issues.Handler()))
	mux.Handle("/api/factory/collaboration/repositories", guard(catalog.Handler()))
	mux.Handle("/api/factory/collaboration/repositories/{repository}/capabilities", guard(capabilities.Handler()))
	mux.Handle("/api/factory/collaboration/repositories/{repository}/issues/{number}/capabilities", guard(capabilities.Handler()))
	mux.Handle("/api/factory/collaboration/repositories/{repository}/pulls/{number}/capabilities", guard(capabilities.Handler()))
	mux.Handle("/api/factory/collaboration/repositories/{repository}/pulls", guard(pulls.Handler()))
	mux.Handle("/api/factory/collaboration/repositories/{repository}/pulls/{number}", guard(pulls.Handler()))
	mux.Handle("/api/factory/collaboration/repositories/{repository}/pulls/{number}/files", guard(files.Handler()))
	mux.Handle("/api/factory/collaboration/repositories/{repository}/pulls/{number}/checks", guard(checks.Handler()))
	mux.Handle("/api/factory/collaboration/repositories/{repository}/pulls/{number}/checks/jobs", guard(checks.Handler()))
	mux.Handle("/api/factory/collaboration/repositories/{repository}/pulls/{number}/threads", guard(threads.Handler()))
	mux.Handle("/api/factory/collaboration/repositories/{repository}/pulls/{number}/threads/{thread}", guard(threads.Handler()))
	mux.Handle("/api/factory/collaboration/repositories/{repository}/pulls/{number}/threads/{thread}/comments", guard(threads.Handler()))
	mux.Handle("/api/factory/collaboration/repositories/{repository}/pulls/{number}/reviews", guard(reviews.Handler()))
	mux.Handle("/api/factory/collaboration/repositories/{repository}/pulls/{number}/reviews/{review}", guard(reviews.Handler()))
	mux.Handle("/api/factory/collaboration/repositories/{repository}/issues/{number}/comments", guard(discussions.Handler()))
	mux.Handle("/api/factory/collaboration/repositories/{repository}/pulls/{number}/comments", guard(discussions.Handler()))
	for _, kind := range []string{"issues", "pulls"} {
		base := "/api/factory/collaboration/repositories/{repository}/" + kind + "/{number}/comments"
		mux.Handle("POST "+base, guard(comments.Handler(), comments.Service.Store))
		mux.Handle("GET "+base+"/commands/{command}", guard(comments.Handler(), comments.Service.Store))
		mux.Handle("POST "+base+"/commands/{command}/cancel", guard(comments.Handler(), comments.Service.Store))
		editBase := "/api/factory/collaboration/repositories/{repository}/" + kind + "/{number}/edits"
		mux.Handle("POST "+editBase, guard(edits.Handler(), edits.Service.Store))
		mux.Handle("GET "+editBase+"/commands/{command}", guard(edits.Handler(), edits.Service.Store))
		mux.Handle("POST "+editBase+"/commands/{command}/cancel", guard(edits.Handler(), edits.Service.Store))
		lockBase := "/api/factory/collaboration/repositories/{repository}/" + kind + "/{number}/locks"
		mux.Handle("POST "+lockBase, guard(locks.Handler(), locks.Service.Store))
		mux.Handle("GET "+lockBase+"/commands/{command}", guard(locks.Handler(), locks.Service.Store))
		mux.Handle("POST "+lockBase+"/commands/{command}/cancel", guard(locks.Handler(), locks.Service.Store))
		stateBase := "/api/factory/collaboration/repositories/{repository}/" + kind + "/{number}/state"
		mux.Handle("POST "+stateBase, guard(states.Handler(), states.Service.Store))
		mux.Handle("GET "+stateBase+"/commands/{command}", guard(states.Handler(), states.Service.Store))
		mux.Handle("POST "+stateBase+"/commands/{command}/cancel", guard(states.Handler(), states.Service.Store))
	}
	mux.Handle("/api/factory/identity", guard(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
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
	})))
	mux.Handle("/api/factory/repositories", feedHTTP)
	mux.Handle("/api/factory/feed", feedHTTP)
	mux.Handle("/api/factory/feed/", feedHTTP)
	mux.Handle("/api/factory/feed/posts/{id}/review", requestHTTP)
	mux.Handle("/api/factory/departments", requestHTTP)
	mux.Handle("/api/factory/intents", requestHTTP)
	mux.Handle("/api/factory/intents/", requestHTTP)
	mux.Handle("/api/factory/capacity", repositoryHTTP)
	mux.Handle("/api/factory/runs/", runHTTP)
	mux.Handle("/api/factory/library", recordHTTP)
	mux.Handle("/api/factory/library/", recordHTTP)
	mux.Handle("/api/factory/artifacts/", recordHTTP)
	base := "/api/repositories/{repositoryID}/factory"
	mux.Handle(base, repositoryHTTP)
	mux.Handle(base+"/", repositoryHTTP)
	mux.Handle(base+"/actions", runHTTP)
	issue := base + "/issues/{issueID}"
	mux.Handle(issue, workHTTP)
	mux.Handle(issue+"/", workHTTP)
	mux.Handle(issue+"/acceptance-preview", acceptanceHTTP)
	mux.Handle(issue+"/acceptances", acceptanceHTTP)
	mux.Handle(issue+"/withdrawal", acceptanceHTTP)
	return mux
}
