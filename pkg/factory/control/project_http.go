package control

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"time"

	"github.com/levitateos/factory/pkg/factory"
	"github.com/levitateos/factory/pkg/project"
	"github.com/levitateos/factory/pkg/store"
	"github.com/levitateos/factory/pkg/strictjson"
)

// ProjectHandler serves only the private SO_PEERCRED-gated Forge integration
// listener. Never mount it on the browser, extension, worker or intake listener.
func (c *Coordinator) ProjectHandler() http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Cache-Control", "no-store")
		if r.Method != http.MethodPost || r.URL.Path != "/projects" || r.URL.RawQuery != "" || r.URL.RawPath != "" || r.URL.ForceQuery {
			http.NotFound(w, r)
			return
		}
		var in factory.ProjectServiceRequest
		r.Body = http.MaxBytesReader(w, r.Body, 64<<10)
		if strictjson.Decode(r.Body, &in) != nil || (in.Action != "authority" && !project.ValidID(in.Project)) || (in.Action == "authority" && in.Repository <= 0) || (in.Action == "stop" && (in.Principal == "" || len(in.Principal) > 256)) {
			http.Error(w, "invalid project operation", http.StatusBadRequest)
			return
		}
		// Once admitted, Project mutations finish under their own deadline even
		// if the Forge process or its client connection goes away.
		ctx, cancel := context.WithTimeout(context.WithoutCancel(r.Context()), 10*time.Minute)
		defer cancel()
		var out factory.ProjectServiceResult
		var err error
		switch in.Action {
		case "stop":
			out.Withdrawal, out.Runs, err = c.StopProject(ctx, in.Principal, in.Project)
		case "verify-start":
			out.Verification, err = c.VerifyProjectStart(ctx, in.Project)
		case "complete-start":
			out.DispatchOpen, err = c.ClearProjectStopAfterStart(ctx, in.Project, in.Verification)
		case "authority":
			out.Authority, err = c.EffectiveAuthority(ctx, in.Repository)
		default:
			http.Error(w, "invalid project operation", http.StatusBadRequest)
			return
		}
		if err != nil {
			status := http.StatusServiceUnavailable
			if errors.Is(err, factory.ErrNotFound) || errors.Is(err, store.ErrNotFound) {
				status = http.StatusNotFound
			}
			http.Error(w, "factory project operation unconfirmed", status)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(out)
	})
}
