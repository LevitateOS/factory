//go:build !linux

package runtime

import (
	"context"
	"errors"
	"net"
)

func nativeListener(NativeConfig) (net.Listener, func() error, error) {
	return nil, nil, errors.New("native backend listener requires supported Linux peer admission")
}

func nativePeerContext(ctx context.Context, _ net.Conn) context.Context { return ctx }
