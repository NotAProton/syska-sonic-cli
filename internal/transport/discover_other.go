//go:build !linux
// +build !linux

package transport

import (
	"context"

	"github.com/notaproton/syska-sonic-cli/internal/clierrors"
)

func Discover(ctx context.Context, f Filter) ([]Device, error) {
	return nil, clierrors.New(clierrors.CodeDevice, "BLE discovery is only implemented on Linux (BlueZ)").
		WithHint("this build targets Windows/macOS Android; run on a Linux host with BlueZ")
}
