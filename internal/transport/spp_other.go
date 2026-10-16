//go:build !linux
// +build !linux

package transport

import (
	"context"

	"github.com/notaproton/syska-sonic-cli/internal/clierrors"
)

// DialSPP is only implemented on Linux (RFCOMM socket).
func DialSPP(ctx context.Context, mac string, channel int) (*SPPConn, error) {
	return nil, clierrors.New(clierrors.CodeDevice, "SPP transport requires Linux (BlueZ)").
		WithHint("run the CLI on the Linux host with the Bluetooth adapter")
}

// SPPConn placeholder on unsupported platforms.
type SPPConn struct{}

func (*SPPConn) Write([]byte) (int, error) {
	return 0, clierrors.New(clierrors.CodeDevice, "SPP transport unavailable on this platform")
}
func (*SPPConn) Close() error { return nil }
