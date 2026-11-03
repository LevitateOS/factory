package github

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"net/http"
	"strconv"
	"time"

	"github.com/levitateos/factory/pkg/collaboration"
)

// NativeAccountRegistry records provider observations, never permissions. Its
// implementation is the existing canonical Go Store, not a provider-owned DB.
type NativeAccountRegistry interface {
	BindCollaborationConnection(context.Context, collaboration.ConnectionObservation) (collaboration.ConnectionBinding, error)
	ObserveCollaborationConnectionRepository(context.Context, collaboration.ConnectionRef, string, string) (collaboration.RepositoryRef, error)
}

// NativeAccountScope is fixed by trusted runtime composition for its lifetime.
// It contains no credential, selected actor or browser-controlled profile.
type NativeAccountScope struct {
	Profile  collaboration.Profile
	Registry NativeAccountRegistry
}

func (s NativeAccountScope) valid() bool {
	return s.Registry != nil && collaboration.ValidID(string(s.Profile.ID)) && collaboration.ValidText(s.Profile.Name, 120)
}

func (s NativeAccountScope) bind(ctx context.Context, connection, account, login string) (collaboration.ConnectionBinding, error) {
	return s.Registry.BindCollaborationConnection(ctx, collaboration.ConnectionObservation{
		Profile: s.Profile, Provider: collaboration.GitHub, APIBase: "https://api.github.com",
		Connection: collaboration.ConnectionID(connection), Account: account, Login: login,
	})
}

func bindingError(w http.ResponseWriter, err error) {
	status, code := 503, "canonical_account_unavailable"
	if errors.Is(err, collaboration.ErrIdentityConflict) || errors.Is(err, collaboration.ErrUnboundLedger) {
		status, code = 409, "canonical_account_conflict"
	}
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(map[string]string{"error": code})
}

func canonicalRepositoryPage(binding collaboration.ConnectionBinding, items []collaboration.RepositoryRef, page, limit, next int) collaboration.Page[collaboration.RepositoryRef] {
	cursor := ""
	if next != 0 {
		cursor = strconv.Itoa(next)
	}
	// A content-derived version of this account/page observation, not a global
	// inventory version or permission receipt. Include scope and pagination.
	facts, _ := json.Marshal(struct {
		Connection        collaboration.ConnectionRef
		Items             []collaboration.RepositoryRef
		Page, Limit, Next int
	}{binding.Connection, items, page, limit, next})
	digest := sha256.Sum256(facts)
	return collaboration.Page[collaboration.RepositoryRef]{
		Observation: collaboration.Observation{State: collaboration.ReadAvailable, Revision: collaboration.Revision(hex.EncodeToString(digest[:])), ObservedAt: time.Now().UTC()},
		Items:       items, Next: cursor, Complete: next == 0,
	}
}
