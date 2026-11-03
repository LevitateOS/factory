package factory

import (
	"context"
	"encoding/json"

	"github.com/levitateos/factory/pkg/collaboration"
)

// GraphCodec owns the provider encoding captured by approved generations. It
// performs no provider I/O or admission. Receipt projection may look up scoped
// identities in the local registry. The engine plans semantic edits; the adapter
// encodes their exact conditional guards and interprets original receipts.
type GraphCodec interface {
	EncodeGraph(collaboration.GraphIntent, int64) (json.RawMessage, error)
	InspectGraph(json.RawMessage) (GraphPacketFacts, error)
	DecodeGraphReceipt(context.Context, json.RawMessage) (GraphReceiptFacts, error)
}

type GraphIssueText struct {
	Title string
	Body  string
}

// GraphPacketFacts is a read-only projection of an approved packet. Canonical
// restores the provider's established field ordering after JSONB round trips,
// so existing approval digests remain valid. It is not a newly approved intent.
// Issue bindings remain provider issue IDs, separate from repository/actor keys.
type GraphPacketFacts struct {
	Canonical         json.RawMessage
	Creates           []collaboration.GraphCreate
	Edits             map[int64]GraphIssueText
	DependencyChanges int
}

// GraphReceiptFacts supplies only the issue identities consumed by the existing
// generation adoption journal. Dependencies remain in the original raw receipt;
// adoption independently re-reads exact edges before granting acceptance.
type GraphReceiptFacts struct {
	Original   json.RawMessage
	Repository int64
	Actor      int64
	Creates    map[string]GeneratedIssueIdentity
	Edits      []GeneratedIssueIdentity
}
