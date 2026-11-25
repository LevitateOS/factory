//go:build !linux && !darwin

package runtime

import (
	"context"
	"errors"
	"net"
	"os"
)

func nativeListener(NativeConfig) (net.Listener, func() error, error) {
	return nil, nil, errors.New("native backend listener requires supported Linux or macOS peer admission")
}

func nativePeerContext(ctx context.Context, _ net.Conn) context.Context { return ctx }

func nativeOwnedFile(os.FileInfo) bool { return false }
