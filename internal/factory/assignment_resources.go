package factory

import (
	"errors"
	"time"
)

// Reservation states. Held capacity counts against every applicable
// limit; consumed capacity served a recorded run; released capacity was
// confirmed unused and counts nowhere.
const (
	ReservationHeld     = "held"
	ReservationConsumed = "consumed"
	ReservationReleased = "released"
)

// ValidReservationState reports whether state is a known reservation state.
func ValidReservationState(state string) bool {
	switch state {
	case ReservationHeld, ReservationConsumed, ReservationReleased:
		return true
	default:
		return false
	}
}

// Reservation is one dispatch's held capacity: an appliance slot, a
// repository slot and a provider allowance slice, all keyed by the
// assignment. PlannedMinutes is the ceiling the run's deadline implies;
// only confirmed actual usage is ever charged.
type Reservation struct {
	AssignmentID   string `json:"assignment_id"`
	Repository     int64  `json:"repository,string"`
	Connection     string `json:"connection"`
	State          string `json:"state"`
	PlannedMinutes int    `json:"planned_minutes"`
	Revision       int64  `json:"revision"`
}

// Validate rejects malformed reservations.
func (r Reservation) Validate() error {
	if !ValidID(r.AssignmentID) || r.Repository <= 0 {
		return errors.New("invalid reservation scope")
	}
	if r.Connection == "" || len(r.Connection) > 128 {
		return errors.New("invalid reservation connection")
	}
	if !ValidReservationState(r.State) || r.Revision < 0 {
		return errors.New("invalid reservation state")
	}
	if r.PlannedMinutes < 1 || r.PlannedMinutes > 180 {
		return errors.New("invalid reservation plan")
	}
	return nil
}

// Usage is one settled run's confirmed provider consumption. The interval
// records the instants used to derive its rounded-up whole-minute charge.
// Rows are append-only: edits and resume never rewrite them.
type Usage struct {
	RunID      string    `json:"run_id"`
	Repository int64     `json:"repository,string"`
	Connection string    `json:"connection"`
	Minutes    int       `json:"minutes"`
	StartedAt  time.Time `json:"started_at"`
	EndedAt    time.Time `json:"ended_at"`
}

// Validate rejects malformed usage rows.
func (u Usage) Validate() error {
	if !ValidID(u.RunID) || u.Repository <= 0 {
		return errors.New("invalid usage scope")
	}
	if u.Connection == "" || len(u.Connection) > 128 {
		return errors.New("invalid usage connection")
	}
	if u.Minutes < 0 || u.Minutes > 10080 || u.StartedAt.IsZero() || u.EndedAt.IsZero() || u.EndedAt.Before(u.StartedAt) {
		return errors.New("invalid usage amount")
	}
	elapsed := u.EndedAt.Sub(u.StartedAt)
	wantMinutes := 0
	if elapsed > 0 {
		wantMinutes = int((elapsed + time.Minute - time.Nanosecond) / time.Minute)
	}
	if u.Minutes != wantMinutes {
		return errors.New("usage minutes do not match interval")
	}
	return nil
}
