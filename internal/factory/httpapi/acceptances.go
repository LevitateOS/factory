package httpapi

import (
	"errors"
	"net/http"
	"strconv"

	"github.com/levitateos/sodaos/internal/collaboration"
	factoryservice "github.com/levitateos/sodaos/internal/factory/service"
)

// Acceptances serves manual decisions over the existing admission journal.
// Hosts bind identity and supply current channel/account, visibility and
// execution-authority ports. Request bodies never choose the approver.
type Acceptances struct {
	Service         factoryservice.Acceptances
	Host            collaboration.HostID
	Actor           collaboration.ActorID
	Visibility      factoryservice.WorkVisibility
	Current         func(http.ResponseWriter, *http.Request) bool
	AuthorityError  func(http.ResponseWriter, error)
	VisibilityError func(http.ResponseWriter, error)
}

func (s Acceptances) Handler() http.Handler {
	mux := http.NewServeMux()
	register := func(method, path string, serve http.HandlerFunc) {
		mux.HandleFunc(method+" "+path, func(w http.ResponseWriter, r *http.Request) {
			if r.Method != method {
				w.Header().Set("Allow", method)
				writeError(w, 405, "method_not_allowed", "HTTP method not supported.")
				return
			}
			if s.current(w, r) {
				serve(w, r)
			}
		})
	}
	base := "/api/repositories/{repositoryID}/factory/issues/{issueID}"
	register(http.MethodGet, base+"/acceptance-preview", s.preview)
	register(http.MethodPost, base+"/acceptances", s.admit)
	register(http.MethodPost, base+"/withdrawal", s.withdraw)
	return mux
}

func (s Acceptances) current(w http.ResponseWriter, r *http.Request) bool {
	return currentAccount(w, r, s.Host, s.Actor, s.Current)
}

func (s Acceptances) scope(w http.ResponseWriter, r *http.Request) (collaboration.RepositoryID, string, bool) {
	id, valid := positiveID(r.PathValue("repositoryID"))
	if !valid {
		writeError(w, 404, "not_found", "Repository not found.")
		return 0, "", false
	}
	repository := collaboration.RepositoryID(id)
	if s.Visibility == nil {
		s.visibilityError(w, factoryservice.ErrAcceptanceUnavailable)
		return 0, "", false
	}
	if err := s.Visibility(r.Context(), s.Actor, repository); err != nil {
		s.visibilityError(w, err)
		return 0, "", false
	}
	index, valid := positiveID(r.PathValue("issueID"))
	if !valid {
		writeError(w, 404, "not_found", "Factory issue not found.")
		return 0, "", false
	}
	return repository, strconv.FormatInt(index, 10), true
}

func (s Acceptances) preview(w http.ResponseWriter, r *http.Request) {
	if r.URL.ForceQuery {
		writeError(w, 400, "invalid_request", "Malformed acceptance preview query.")
		return
	}
	comments, valid := parseAcceptancePreviewCommentIDs(r.URL.RawQuery)
	if !valid {
		writeError(w, 400, "invalid_request", "Use at most 50 distinct positive comment_id values.")
		return
	}
	repository, issue, ok := s.scope(w, r)
	if !ok {
		return
	}
	view, err := s.Service.Preview(r.Context(), s.Actor, repository, issue, comments)
	if err != nil {
		if errors.Is(err, factoryservice.ErrAcceptanceAccess) {
			s.authorityError(w, err)
		} else {
			acceptancePreviewReadError(w, err)
		}
		return
	}
	if s.current(w, r) {
		writeJSON(w, 200, view)
	}
}

func (s Acceptances) admit(w http.ResponseWriter, r *http.Request) {
	repository, issue, ok := s.scope(w, r)
	if !ok {
		return
	}
	var in factoryservice.AcceptanceChange
	if !decodeObject(w, r, &in) || !s.current(w, r) {
		return
	}
	result, err := s.Service.Admit(r.Context(), s.Actor, repository, issue, in)
	if err != nil {
		s.applicationError(w, err)
		return
	}
	if s.current(w, r) {
		writeJSON(w, 201, result)
	}
}

func (s Acceptances) withdraw(w http.ResponseWriter, r *http.Request) {
	repository, issue, ok := s.scope(w, r)
	if !ok {
		return
	}
	var in factoryservice.AcceptanceWithdrawal
	if !decodeObject(w, r, &in) || !s.current(w, r) {
		return
	}
	result, err := s.Service.Withdraw(r.Context(), s.Actor, repository, issue, in)
	if err != nil {
		s.applicationError(w, err)
		return
	}
	if s.current(w, r) {
		writeJSON(w, 200, result)
	}
}

func (s Acceptances) authorityError(w http.ResponseWriter, err error) {
	if s.AuthorityError != nil {
		s.AuthorityError(w, err)
		return
	}
	writeError(w, 503, "authority_unavailable", "Current acceptance authority is unavailable.")
}

func (s Acceptances) visibilityError(w http.ResponseWriter, err error) {
	if s.VisibilityError != nil {
		s.VisibilityError(w, err)
		return
	}
	writeError(w, 503, "authority_unavailable", "Current repository visibility is unavailable.")
}
