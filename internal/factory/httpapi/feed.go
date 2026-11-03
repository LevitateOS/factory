package httpapi

import (
	"encoding/json"
	"net/http"
	"strings"

	"github.com/levitateos/sodaos/internal/collaboration"
	factoryservice "github.com/levitateos/sodaos/internal/factory/service"
)

// Feed serves the shared catalogue, feed and subscription protocol for one
// host-authenticated principal. Hosts supply current authentication/channel and
// origin admission; Visibility and Posts retain per-resource authority. No
// request header, query or body selects Actor or Host.
type Feed struct {
	Service    factoryservice.Feed
	Posts      factoryservice.Posts
	Commands   factoryservice.PostCommands
	AuthorName string
	Host       collaboration.HostID
	Actor      collaboration.ActorID
	Visibility factoryservice.RepositoryVisibility
	// Current must recheck this principal's host admission and render its own
	// refusal when it returns false. Missing admission always refuses.
	Current func(http.ResponseWriter, *http.Request) bool
}

// Handler constructs no listener, session, provider client or work driver.
// The host mounts it only behind its authenticated browser or private IPC path.
func (s Feed) Handler() http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var serve http.HandlerFunc
		methods := http.MethodGet
		switch r.URL.Path {
		case "/api/factory/repositories":
			serve = s.repositories
		case "/api/factory/feed":
			serve = s.page
		case "/api/factory/feed/subscription":
			serve = s.subscription
			methods += ", " + http.MethodPut
		case "/api/factory/feed/posts":
			serve, methods = s.submitPost, http.MethodPost
		default:
			if rest, ok := strings.CutPrefix(r.URL.Path, "/api/factory/feed/posts/"); ok {
				id, tail, hasTail := strings.Cut(rest, "/")
				if id != "" && (!hasTail || tail != "") {
					switch tail {
					case "":
						serve = s.readPost
					case "replies":
						serve = s.replies
					case "retry":
						serve, methods = s.retryPost, http.MethodPost
					default:
						if command, ok := strings.CutPrefix(tail, "retry/"); ok && !strings.Contains(command, "/") {
							serve = s.retryReceipt
							r.SetPathValue("command", command)
						}
					}
					r.SetPathValue("id", id)
				}
			}
			if serve == nil {
				http.NotFound(w, r)
				return
			}
		}
		if r.Method != methods && !(r.URL.Path == "/api/factory/feed/subscription" && (r.Method == http.MethodGet || r.Method == http.MethodPut)) {
			w.Header().Set("Allow", methods)
			writeError(w, 405, "method_not_allowed", "HTTP method not supported.")
			return
		}
		if s.current(w, r) {
			if strings.HasPrefix(r.URL.Path, "/api/factory/feed/posts") {
				port := s.Posts.Store
				if r.Method == http.MethodPost {
					port = s.Commands.Posts.Store
				}
				if port == nil || port != s.Service.Store {
					writeError(w, 503, "store_unavailable", "Feed post transport is unavailable.")
					return
				}
			}
			serve(w, r)
		}
	})
}

func (s Feed) current(w http.ResponseWriter, r *http.Request) bool {
	return currentAccount(w, r, s.Host, s.Actor, s.Current)
}

func currentAccount(w http.ResponseWriter, r *http.Request, host collaboration.HostID, actor collaboration.ActorID, current func(http.ResponseWriter, *http.Request) bool) bool {
	if actor <= 0 || !collaboration.ValidID(string(host)) || current == nil || r.Context().Err() != nil {
		writeError(w, 503, "authority_unavailable", "Current Factory account admission is unavailable.")
		return false
	}
	return current(w, r)
}

func writeJSON(w http.ResponseWriter, status int, value any) {
	body, err := json.Marshal(value)
	if err != nil {
		status = http.StatusInternalServerError
		body = []byte(`{"error":{"code":"internal_error","message":"Cannot encode response."}}`)
	}
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.Header().Set("Cache-Control", "no-store")
	w.WriteHeader(status)
	_, _ = w.Write(append(body, '\n'))
}

func writeError(w http.ResponseWriter, status int, code, message string) {
	writeJSON(w, status, map[string]any{"error": map[string]string{"code": code, "message": message}})
}
