package factory

// PublicationRefusal is a terminal executor verdict: the publication
// will not proceed under its recorded inputs and the coordinator
// records its failure instead of retrying an identical call.
type PublicationRefusal struct{ Reason string }

func (e *PublicationRefusal) Error() string { return "publication refused: " + e.Reason }

// PublicationWait reports that no verdict exists yet: the call reached
// no native decision and the publication stays open for a later pass.
// It never advances the stage.
type PublicationWait struct{ Reason string }

func (e *PublicationWait) Error() string { return "publication waits: " + e.Reason }
