package control_test

import (
	"context"
	"errors"
	"net/http"
	"time"

	"github.com/levitateos/sodaos/internal/factory"
	forgejopublish "github.com/levitateos/sodaos/internal/forgejo/publish"
)

func nativeContextError(ctx context.Context) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	if deadline, ok := ctx.Deadline(); ok && !time.Now().Before(deadline) {
		return context.DeadlineExceeded
	}
	return nil
}

func nativeContextWait(ctx context.Context, delay time.Duration) bool {
	timer := time.NewTimer(delay)
	defer timer.Stop()
	select {
	case <-ctx.Done():
		return false
	case <-timer.C:
		return nativeContextError(ctx) == nil
	}
}

// nativeMergeBusyError reports whether err is the transient busy-host
// signal on a direct native call: an HTTP 503 or a native_busy wait.
func nativeMergeBusyError(err error) bool {
	var status *forgejopublish.StatusError
	if errors.As(err, &status) && status.Status == http.StatusServiceUnavailable {
		return true
	}
	var wait *factory.PublicationWait
	return errors.As(err, &wait) && wait.Reason == "native_busy"
}

// nativeMergeCall retries one idempotent native read (or same-identity
// submit) until it answers; only the transient busy-host signal
// retries, everything else fails fast.
func nativeMergeCallContext[T any](ctx context.Context, call func(context.Context) (T, error)) (T, error) {
	var zero T
	for {
		if err := nativeContextError(ctx); err != nil {
			return zero, err
		}
		value, err := call(ctx)
		if ctxErr := nativeContextError(ctx); ctxErr != nil {
			return zero, ctxErr
		}
		if err == nil {
			return value, nil
		}
		if !nativeMergeBusyError(err) {
			return zero, err
		}
		if !nativeContextWait(ctx, 200*time.Millisecond) {
			return zero, nativeContextError(ctx)
		}
		if ctxErr := nativeContextError(ctx); ctxErr != nil {
			return zero, ctxErr
		}
	}
}
