package runtime

import (
	"context"
	"encoding/json"

	"github.com/levitateos/factory/pkg/collaboration"
	"github.com/levitateos/factory/pkg/factory"
	factoryservice "github.com/levitateos/factory/pkg/factory/service"
)

// Preparation versions select a defining implementation without rewriting old
// captures or extending their original deadlines. Version 1 creates a review;
// version 2 submits the exact pending review observed during preparation.
type nativeHumanReviews struct{ *nativeProductAccount }

var _ factoryservice.HumanReviewWriter = nativeHumanReviews{}
var _ factoryservice.HumanReviewDestinationReader = nativeHumanReviews{}

func (s nativeHumanReviews) writer(ctx context.Context, c factory.HumanReviewCapture) (factoryservice.HumanReviewWriter, error) {
	if _, err := c.Command(); err != nil {
		return nil, factoryservice.ErrHumanReviewQuery
	}
	if err := (nativeHumanNewReviews{s.nativeProductAccount}).admit(ctx, c.Request); err != nil {
		return nil, err
	}
	if c.Authority != s.humanAuthority() {
		return nil, factoryservice.ErrHumanReviewNamespace
	}
	var header struct {
		Version int `json:"version"`
	}
	if json.Unmarshal(c.ProviderIntent, &header) != nil {
		return nil, factoryservice.ErrHumanReviewConflict
	}
	switch header.Version {
	case 1:
		return nativeHumanNewReviews{s.nativeProductAccount}, nil
	case 2:
		return nativeHumanPendingReviews{s.nativeProductAccount}, nil
	default:
		return nil, factoryservice.ErrHumanReviewConflict
	}
}
func (s nativeHumanReviews) PrepareHumanReview(ctx context.Context, q factory.HumanReviewCommand) (factory.HumanReviewCapture, error) {
	return (nativeHumanNewReviews{s.nativeProductAccount}).PrepareHumanReview(ctx, q)
}
func (s nativeHumanReviews) SubmitHumanReview(ctx context.Context, c factory.HumanReviewCapture) (collaboration.OperationRecord, error) {
	w, err := s.writer(ctx, c)
	if err != nil {
		return collaboration.OperationRecord{}, err
	}
	return w.SubmitHumanReview(ctx, c)
}
func (s nativeHumanReviews) LookupHumanReview(ctx context.Context, c factory.HumanReviewCapture) (collaboration.OperationLookup, error) {
	w, err := s.writer(ctx, c)
	if err != nil {
		return collaboration.OperationLookup{}, err
	}
	return w.LookupHumanReview(ctx, c)
}
func (s nativeHumanReviews) CancelHumanReview(ctx context.Context, c factory.HumanReviewCapture) (collaboration.OperationRecord, error) {
	w, err := s.writer(ctx, c)
	if err != nil {
		return collaboration.OperationRecord{}, err
	}
	return w.CancelHumanReview(ctx, c)
}
func (s nativeHumanReviews) ReadCreatedHumanReview(ctx context.Context, c factory.HumanReviewCapture, o collaboration.OperationLookup) (collaboration.ReviewRef, error) {
	w, err := s.writer(ctx, c)
	if err != nil {
		return collaboration.ReviewRef{}, err
	}
	return w.(factoryservice.HumanReviewDestinationReader).ReadCreatedHumanReview(ctx, c, o)
}
