# Cancellable advisory file locks

Soda's Go coordinators use `filelock.Acquire` to wait for native shared or
exclusive locks while respecting a context. The package owns lock acquisition;
callers own files, permissions, lock ordering, protected state and release.

## Use inside the Soda module

Pass an already opened file and a `golang.org/x/sys/unix` lock mode. For example,
a helper that acquires and releases an exclusive lock:

```go
import (
    "context"
    "os"
    "github.com/levitateos/sodaos/internal/filelock"
    "golang.org/x/sys/unix"
)

func inspectLocked(ctx context.Context, file *os.File) error {
    if err := filelock.Acquire(ctx, file, unix.LOCK_EX); err != nil {
        return err
    }
    // Read the state protected by this lock here.
    return unix.Flock(int(file.Fd()), unix.LOCK_UN)
}
```

Use `unix.LOCK_SH` for a shared lock. The caller opens and later closes `file`;
keep all protected work before unlocking.

## Behavior and limits

[`Acquire`](filelock.go) attempts a nonblocking native `flock`, retrying every
25 ms on contention or interruption. Cancellation or deadline expiry while
waiting returns the context error; other descriptor/syscall errors are returned.
Cancellation after successful acquisition does not automatically unlock it.

These are advisory locks: every cooperating writer must follow the same lock
protocol. The package creates no file or directory and provides no business
transaction or distributed lock. It uses native Unix locking on Soda's supported
Linux platform.

See [factory interfaces](../../docs/architecture/factory-interfaces.md) for the
coordinator's state ownership and [Go conventions](../../docs/development/go-packages.md)
for package responsibilities.
