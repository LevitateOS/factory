package runtime

import (
	"context"
	"net/http"

	factoryhttpapi "github.com/levitateos/factory/pkg/factory/httpapi"
	factoryservice "github.com/levitateos/factory/pkg/factory/service"
)

// NativeProductBinding is a trusted account/connector port, not JSON admission.
// It resolves current profile/account truth on each request and returns service
// ports bound to that account. IPC capability alone never supplies an actor.
type NativeProductBinding func(context.Context, factoryservice.Application) (factoryhttpapi.Product, error)

type NativeApplication struct {
	application factoryservice.Application
	bind        NativeProductBinding
	routes      *factoryhttpapi.ProductRoutes
	handler     http.Handler
}

// NewNativeApplication uses the same service owner as the appliance host.
// Construction neither starts its coordinator nor drives/reconciles retained work.
func NewNativeApplication(ports factoryservice.ApplicationPorts, bind NativeProductBinding) *NativeApplication {
	a := &NativeApplication{application: factoryservice.NewApplication(ports), bind: bind, routes: factoryhttpapi.NewProductRoutes()}
	// Binding can itself perform account/provider I/O. It belongs inside the
	// same lifetime gate as product work, even before background runtime starts.
	a.handler = a.application.Coordinator.AdmitHTTP(http.HandlerFunc(a.serveHTTP))
	return a
}

func (a *NativeApplication) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	a.handler.ServeHTTP(w, r)
}

// Recover is explicit startup recovery over the canonical application/store.
// It starts no coordinator, workflow, provider call or native execution.
func (a *NativeApplication) Recover(ctx context.Context) error { return a.application.Recover(ctx) }

// StopAdmission refuses new product work before the host retires its listeners.
func (a *NativeApplication) StopAdmission() { a.application.StopAdmission() }

// Close drains admitted bindings/product handlers before the host closes Store.
// It does not close Store or start/reconcile the background runtime.
func (a *NativeApplication) Close() error { return a.application.Close() }

func (a *NativeApplication) serveHTTP(w http.ResponseWriter, r *http.Request) {
	product := factoryhttpapi.Product{Store: a.application.Store}
	if a.bind != nil && r.Context().Err() == nil {
		bound, err := a.bind(r.Context(), a.application)
		if err != nil {
			nativeProductResourceError(w, err)
			return
		}
		if bound.Store == a.application.Store {
			product = bound
		}
	}
	// Missing/unknown account binding retains the canonical unavailable response
	// from shared HTTP. It never becomes an empty successful account or feed.
	a.routes.ServeProductHTTP(w, r, product)
}
