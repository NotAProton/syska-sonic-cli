//go:build linux
// +build linux

package transport

import (
	"context"
	"encoding/hex"
	"strings"
	"time"

	gounix "golang.org/x/sys/unix"

	"github.com/notaproton/syska-sonic-cli/internal/clierrors"
)

const (
	rfcommChannelDefault = 1 // SPP channel of this module family
)

// SPPConn is an open classic-Bluetooth RFCOMM stream.
type SPPConn struct {
	FD      int
	MAC     string
	Channel int
}

// DialSPP opens the SPP channel (channel 1 by default). The device must be
// paired (bluetoothctl pair <mac>) and present ("power on", not claimed by
// another host).
func DialSPP(ctx context.Context, mac string, channel int) (*SPPConn, error) {
	if channel <= 0 {
		channel = rfcommChannelDefault
	}
	bd, ferr := parseMAC(mac)
	if ferr != nil {
		return nil, ferr
	}
	fd, err := gounix.Socket(gounix.AF_BLUETOOTH, gounix.SOCK_STREAM, gounix.BTPROTO_RFCOMM)
	if err != nil {
		return nil, clierrors.New(clierrors.CodeDevice, "open RFCOMM socket failed").
			WithCause(err).
			WithHint("check the BlueZ adapter: syska-sonic-cli doctor")
	}
	addr := gounix.SockaddrRFCOMM{Addr: bd, Channel: byte(channel)}
	dialDone := make(chan error, 1)
	go func() { dialDone <- gounix.Connect(fd, &addr) }()

	select {
	case err := <-dialDone:
		if err != nil {
			gounix.Close(fd)
			return nil, connectErr(err, mac, channel)
		}
	case <-ctx.Done():
		gounix.Close(fd)
		te := clierrors.New(clierrors.CodeTimeout, "RFCOMM connect to %s:%d timed out", mac, channel)
		te.Hint = "make sure the bulb is on, paired (bluetoothctl pair " + mac + "), and not busy; retry with a larger --timeout"
		return nil, te
	}
	return &SPPConn{FD: fd, MAC: mac, Channel: channel}, nil
}

func connectErr(cause error, mac string, channel int) *clierrors.Error {
	ce := clierrors.New(clierrors.CodeDevice, "RFCOMM connect to %s:%d failed", mac, channel).WithCause(cause)
	ce.Hint = "pair first: bluetoothctl pair " + mac + "; ensure no music/audio session holds the device"
	return ce
}

// Write sends bytes over the SPP stream.
func (c *SPPConn) Write(p []byte) (int, error) {
	written := 0
	for written < len(p) {
		n, err := gounix.Write(c.FD, p[written:])
		if err != nil && err != gounix.EAGAIN {
			return written, clierrors.New(clierrors.CodeDevice, "SPP write failed").WithCause(err)
		}
		if err == gounix.EAGAIN {
			time.Sleep(5 * time.Millisecond)
			continue
		}
		written += n
	}
	return written, nil
}

// Read drains device responses.
func (c *SPPConn) Read(buf []byte) (int, error) {
	return gounix.Read(c.FD, buf)
}

// Close the RFCOMM link.
func (c *SPPConn) Close() error {
	return gounix.Close(c.FD)
}

func parseMAC(mac string) ([6]byte, error) {
	var out [6]byte
	hexpart := strings.ReplaceAll(strings.ToUpper(mac), ":", "")
	if len(hexpart) != 12 {
		return out, clierrors.New(clierrors.CodeUsage, "invalid MAC %q", mac).
			WithHint("12 hex digits, colon separated, e.g. AA:BB:CC:DD:EE:FF")
	}
	raw, err := hex.DecodeString(hexpart)
	if err != nil {
		return out, clierrors.New(clierrors.CodeUsage, "invalid MAC hex %q", mac).WithCause(err)
	}
	for i, b := range raw {
		out[5-i] = b // kernel wants little-endian octets
	}
	return out, nil
}

// DefaultSendTimeout bounds blocking send/wait operations.
const DefaultSendTimeout = 15 * time.Second
