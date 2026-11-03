package httpapi

import (
	"bytes"
	"errors"
	"mime"
	"net/http"
	"time"

	"github.com/levitateos/factory/pkg/collaboration"
	"github.com/levitateos/factory/pkg/factory"
	factoryservice "github.com/levitateos/factory/pkg/factory/service"
)

// Records serves retained library observations and verified artifact bytes.
// Hosts supply bound identity, current channel/account admission and canonical
// service source/visibility ports. Reads neither create work nor grant authority.
type Records struct {
	Library   factoryservice.Library
	Artifacts factoryservice.Artifacts
	Host      collaboration.HostID
	Actor     collaboration.ActorID
	Current   func(http.ResponseWriter, *http.Request) bool
}

func (s Records) Handler() http.Handler {
	mux := http.NewServeMux()
	register := func(path string, serve http.HandlerFunc) {
		mux.HandleFunc("GET "+path, func(w http.ResponseWriter, r *http.Request) {
			if r.Method != http.MethodGet {
				w.Header().Set("Allow", http.MethodGet)
				writeError(w, 405, "method_not_allowed", "HTTP method not supported.")
				return
			}
			if s.current(w, r) {
				serve(w, r)
			}
		})
	}
	register("/api/factory/library", s.search)
	register("/api/factory/library/item", s.item)
	register("/api/factory/artifacts/{id}", s.artifact)
	return mux
}

func (s Records) current(w http.ResponseWriter, r *http.Request) bool {
	return currentAccount(w, r, s.Host, s.Actor, s.Current)
}

func (s Records) search(w http.ResponseWriter, r *http.Request) {
	repository, ok := positiveID(r.URL.Query().Get("repository"))
	if !ok {
		writeError(w, 400, "invalid_request", "A project repository is required.")
		return
	}
	query := r.URL.Query()
	page, err := s.Library.Search(r.Context(), s.Actor, collaboration.RepositoryID(repository), factory.LibraryQuery{Query: query.Get("query"), Kind: query.Get("kind"), Cursor: query.Get("cursor"), History: query.Get("history") == "true", CurrentOnly: query.Get("current_only") == "true"})
	if err != nil {
		libraryReadError(w, err)
		return
	}
	if s.current(w, r) {
		writeJSON(w, 200, page)
	}
}

func (s Records) item(w http.ResponseWriter, r *http.Request) {
	repository, ok := positiveID(r.URL.Query().Get("repository"))
	if !ok {
		writeError(w, 400, "invalid_request", "A project repository is required.")
		return
	}
	item, err := s.Library.Read(r.Context(), s.Actor, collaboration.RepositoryID(repository), r.URL.Query().Get("id"), r.URL.Query().Get("history") == "true")
	if err != nil {
		libraryReadError(w, err)
		return
	}
	if s.current(w, r) {
		writeJSON(w, 200, item)
	}
}

func libraryReadError(w http.ResponseWriter, err error) {
	switch {
	case errors.Is(err, factoryservice.ErrLibraryNotFound):
		writeError(w, 404, "item_not_found", "Library item is not available.")
	case errors.Is(err, factoryservice.ErrRepositoryHidden):
		writeError(w, 404, "repository_not_found", "Repository is not visible.")
	case errors.Is(err, factory.ErrLibraryQuery):
		writeError(w, 400, "invalid_request", "Could not search the project library.")
	default:
		writeError(w, 503, "library_unavailable", "Project library is unavailable.")
	}
}

func (s Records) artifact(w http.ResponseWriter, r *http.Request) {
	repository, ok := positiveID(r.URL.Query().Get("repository"))
	if !ok || !factory.ValidID(r.PathValue("id")) {
		writeError(w, 400, "invalid_request", "Artifact identity is invalid.")
		return
	}
	download, err := s.Artifacts.Read(r.Context(), s.Actor, collaboration.RepositoryID(repository), r.PathValue("id"))
	if err != nil {
		code := 503
		if errors.Is(err, factoryservice.ErrArtifactNotFound) || errors.Is(err, factoryservice.ErrRepositoryHidden) {
			code = 404
		}
		writeError(w, code, "unavailable", "Artifact is unavailable.")
		return
	}
	if !s.current(w, r) {
		return
	}
	a, data := download.Artifact, download.Content
	w.Header().Set("Content-Type", a.MediaType)
	w.Header().Set("X-Content-Type-Options", "nosniff")
	w.Header().Set("Content-Security-Policy", "sandbox; default-src 'none'")
	w.Header().Set("Cache-Control", "private, no-store")
	w.Header().Set("Content-Disposition", mime.FormatMediaType("attachment", map[string]string{"filename": a.Name}))
	http.ServeContent(w, r, a.Name, time.Time{}, bytes.NewReader(data))
}
