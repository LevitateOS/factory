package runtime

import (
	"context"
	"errors"
	"net"
	"os"
	"path/filepath"
	"syscall"
	"time"

	"golang.org/x/sys/unix"
)

func nativeListener(config NativeConfig) (net.Listener, func() error, error) {
	lock, err := os.OpenFile(filepath.Join(config.Directory, "backend.lock"), os.O_CREATE|os.O_RDWR|syscall.O_NOFOLLOW|syscall.O_NONBLOCK, 0o600)
	if err != nil {
		return nil, nil, errors.New("native backend profile lock is unavailable")
	}
	fail := func(message string) (net.Listener, func() error, error) {
		_ = lock.Close()
		return nil, nil, errors.New(message)
	}
	info, err := lock.Stat()
	if err != nil || !nativeOwnedFile(info) || info.Mode().Perm()&0o077 != 0 || !info.Mode().IsRegular() {
		return fail("native backend profile lock must be an owned private regular file")
	}
	if unix.Flock(int(lock.Fd()), unix.LOCK_EX|unix.LOCK_NB) != nil {
		return fail("another native backend owns this profile")
	}
	if old, err := os.Lstat(config.Socket); err == nil {
		if old.Mode()&os.ModeSocket == 0 || !nativeOwnedFile(old) {
			return fail("native backend socket path contains another resource")
		}
		probe, err := net.DialTimeout("unix", config.Socket, 250*time.Millisecond)
		if err == nil {
			_ = probe.Close()
			return fail("native backend socket already has a live listener")
		}
		if !errors.Is(err, syscall.ECONNREFUSED) {
			return fail("native backend socket ownership is uncertain")
		}
		current, err := os.Lstat(config.Socket)
		if err != nil || !os.SameFile(old, current) || os.Remove(config.Socket) != nil {
			return fail("native backend socket changed during stale endpoint recovery")
		}
	} else if !errors.Is(err, os.ErrNotExist) {
		return fail("native backend socket path is unavailable")
	}
	listener, err := net.ListenUnix("unix", &net.UnixAddr{Name: config.Socket, Net: "unix"})
	if err != nil {
		return fail("native backend could not bind its private socket")
	}
	listener.SetUnlinkOnClose(false)
	bound, err := os.Lstat(config.Socket)
	if err != nil {
		_ = listener.Close()
		return fail("native backend socket identity is unavailable")
	}
	release := func() error {
		_ = listener.Close()
		var cleanup error
		if current, err := os.Lstat(config.Socket); err == nil && os.SameFile(bound, current) {
			cleanup = os.Remove(config.Socket)
		}
		return errors.Join(cleanup, lock.Close())
	}
	if err := os.Chmod(config.Socket, 0o600); err != nil {
		_ = release()
		return nil, nil, errors.New("native backend socket could not be made private")
	}
	return listener, release, nil
}

func nativeOwnedFile(info os.FileInfo) bool {
	stat, ok := info.Sys().(*syscall.Stat_t)
	return ok && stat.Uid == uint32(os.Geteuid())
}

func nativePeerContext(ctx context.Context, conn net.Conn) context.Context {
	peer := false
	if conn, ok := conn.(*net.UnixConn); ok {
		if raw, err := conn.SyscallConn(); err == nil {
			if err := raw.Control(func(fd uintptr) {
				identity, err := unix.GetsockoptUcred(int(fd), unix.SOL_SOCKET, unix.SO_PEERCRED)
				peer = err == nil && identity.Uid == uint32(os.Geteuid())
			}); err != nil {
				peer = false
			}
		}
	}
	return context.WithValue(ctx, nativePeerKey{}, peer)
}
