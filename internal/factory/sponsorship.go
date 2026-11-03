package factory

import (
	"errors"
	"strings"

	"github.com/levitateos/sodaos/internal/project"
)

// Sponsorship authorizes factory use of one provider connection for one
// repository and its assigned roles within an explicit allowance. It
// references the canonical broker grant, connection and credential
// generation and records its execution actor and Project; it never copies
// credential custody. GrantedBy remains the policy sponsor.
type Sponsorship struct {
	Roles            []string `json:"roles"`
	Repository       int64    `json:"repository,string"`
	Revision         int64    `json:"revision"`
	GrantedBy        int64    `json:"granted_by,string"`
	ActorID          int64    `json:"actor_id,string"`
	Generation       int64    `json:"generation"`
	Connection       string   `json:"connection"`
	GrantID          string   `json:"grant_id"`
	ProjectID        string   `json:"project_id"`
	AllowanceMinutes int      `json:"allowance_minutes"`
	Active           bool     `json:"active"`
	// Sources admits declared research source refs on this connection
	// (R10.2). A research subject may only name granted sources; an
	// empty grant leaves repo-only inquiry.
	Sources []string `json:"sources,omitempty"`
	// Collections admits library collection IDs on this connection
	// (R10.2). A collection subject must name a granted collection.
	Collections []string `json:"collections,omitempty"`
}

// MaxGrantEntries bounds one sponsorship source/collection allowlist:
// an initial explicit grant set, not a corpus limit.
const MaxGrantEntries = 8

func (s Sponsorship) Validate() error {
	if s.Repository <= 0 || s.Revision < 0 || s.GrantedBy <= 0 || s.ActorID <= 0 || s.Generation <= 0 {
		return errors.New("invalid sponsorship identity")
	}
	if s.Connection == "" || len(s.Connection) > 128 || s.GrantID == "" || len(s.GrantID) > 128 || !project.ValidID(s.ProjectID) {
		return errors.New("invalid sponsorship connection reference")
	}
	if len(s.Roles) == 0 || len(s.Roles) > 2 {
		return errors.New("sponsorship must permit one or both factory roles")
	}
	seen := make(map[string]bool, len(s.Roles))
	for _, role := range s.Roles {
		if !project.ValidFactoryRole(role) || seen[role] {
			return errors.New("invalid sponsorship role")
		}
		seen[role] = true
	}
	if s.AllowanceMinutes < 1 || s.AllowanceMinutes > 10080 {
		return errors.New("invalid sponsorship allowance")
	}
	if len(s.Sources) > MaxGrantEntries {
		return errors.New("sponsorship admits too many sources")
	}
	for _, source := range s.Sources {
		if source == "" || len(source) > 128 || strings.ContainsRune(source, 0) {
			return errors.New("invalid sponsorship source ref")
		}
	}
	if len(s.Collections) > MaxGrantEntries {
		return errors.New("sponsorship admits too many collections")
	}
	for _, collection := range s.Collections {
		if !ValidCollectionID(collection) {
			return errors.New("invalid sponsorship collection")
		}
	}
	return nil
}
