package application

import (
	"crypto/rand"
	"encoding/hex"
	"errors"
)

// Snapshot identifies host-inventoried stopped data. No client supplies a path.
type Snapshot struct {
	ID          string `json:"id"`
	CreatedAt   uint64 `json:"created_at"`
	Application Spec   `json:"application"`
}

type SnapshotRequest struct {
	Application       Spec   `json:"application"`
	Action            string `json:"action"`
	ID                string `json:"id,omitempty"`
	ExpectedContainer string `json:"expected_container,omitempty"`
}

type SnapshotInventory struct {
	Snapshots []Snapshot `json:"snapshots"`
	Complete  bool       `json:"complete"`
}

func NewIdentity() (string, error) {
	var bytes [12]byte
	if _, err := rand.Read(bytes[:]); err != nil {
		return "", err
	}
	return "a" + hex.EncodeToString(bytes[:]), nil
}

func (s Snapshot) Validate() error {
	if !identifier.MatchString(s.ID) || s.CreatedAt == 0 {
		return errors.New("invalid application snapshot")
	}
	return s.Application.Validate()
}

func (r SnapshotRequest) Validate() error {
	if err := r.Application.Validate(); err != nil {
		return err
	}
	if r.Action == "list" && r.ID == "" && r.ExpectedContainer == "" {
		return nil
	}
	if r.Action == "inspect" && identifier.MatchString(r.ID) && r.ExpectedContainer == "" {
		return nil
	}
	if r.Action == "backup" && identifier.MatchString(r.ID) && digest.MatchString(r.ExpectedContainer) {
		return nil
	}
	return errors.New("invalid application snapshot operation")
}

func (s Snapshot) Matches(current Spec) bool {
	current.Revision, current.Data = s.Application.Revision, s.Application.Data
	return s.Validate() == nil && current == s.Application
}
