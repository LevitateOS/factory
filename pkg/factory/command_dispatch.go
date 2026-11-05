package factory

// CommandDispatchState records a local one-shot network reservation, not a
// provider outcome or permission. A reserved command may never have reached the
// provider. LegacyUnknown captures predate this fence and cannot be retried on
// the assumption that no dispatch occurred.
type CommandDispatchState string

const (
	DispatchUnclaimed     CommandDispatchState = "unclaimed"
	DispatchReserved      CommandDispatchState = "reserved"
	DispatchLegacyUnknown CommandDispatchState = "legacy_unknown"
)
