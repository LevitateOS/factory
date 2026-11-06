package runtime

import (
	"context"
	"encoding/json"

	"github.com/levitateos/factory/pkg/factory"
)

// NativeInlinePreparationObservation retains credential-free native facts for
// the product adapter. Preparation is private provider evidence, not a browser
// projection, a write grant or a causal receipt.
type NativeInlinePreparationObservation struct {
	NativeIdentity
	Preparation json.RawMessage
}

// The dedicated envelope admits JSON escaping of a maximal canonical path.
// Other reads retain their ordinary small descriptor limit.
type nativeInlinePreparationEffect struct {
	Kind  string                       `json:"kind"`
	Query nativeInlinePreparationQuery `json:"query"`
}
type nativeInlinePreparationQuery struct {
	Kind       string                     `json:"kind"`
	Repository string                     `json:"repository"`
	Number     uint32                     `json:"number"`
	Anchor     factory.ReviewThreadAnchor `json:"anchor"`
}

// InlinePreparation reads one exact immutable selection and the current own
// pending-review snapshot through the admitted native account/repository port.
// Rust owns traversal, provider decoding and the meaning of these observations.
func (p *NativeCollaboration) InlinePreparation(ctx context.Context, repository string, number uint32, anchor factory.ReviewThreadAnchor) (NativeInlinePreparationObservation, error) {
	var result NativeInlinePreparationObservation
	if !nativeRemoteID(repository) || number == 0 || anchor.Validate() != nil {
		return result, errNativeRead
	}
	identity, err := p.read(ctx, nativeRead{Kind: "inline_preparation", Repository: repository, Number: number, Anchor: &anchor}, &result.Preparation)
	if err != nil {
		return NativeInlinePreparationObservation{}, err
	}
	result.NativeIdentity = identity
	return result, nil
}
