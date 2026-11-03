// Package filelock acquires native advisory locks without trapping a cancelled
// command in a blocking flock syscall. Callers own opening and closing files,
// permissions, lock ordering, and unlocking.
package filelock

import (
	"context"
	"errors"
	"os"
	"time"

	"golang.org/x/sys/unix"
)

// Acquire waits for a shared or exclusive lock, or returns the context error.
func Acquire(ctx context.Context, file *os.File, kind int) error {
	ticker := time.NewTicker(25 * time.Millisecond)
	defer ticker.Stop()
	for {
		if err := ctx.Err(); err != nil {
			return err
		}
		err := unix.Flock(int(file.Fd()), kind|unix.LOCK_NB)
		if err == nil {
			return nil
		}
		if !errors.Is(err, unix.EWOULDBLOCK) && !errors.Is(err, unix.EINTR) {
			return err
		}
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-ticker.C:
		}
	}
}
