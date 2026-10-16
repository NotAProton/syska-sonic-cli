// Package transport abstracts the BLE link to a Syska Sonic bulb. Discovery
// works today through BlueZ D-Bus; the send path is shared and returns a
// teachable "protocol pending" error until the BLE protocol is decoded from
// the vendor Android app.
package transport

import (
	"context"
	"encoding/json"
	"time"

	"github.com/notaproton/syska-sonic-cli/internal/clierrors"
)

// Device is one row from a BLE scan.
type Device struct {
	Address          string            `json:"address"`
	Name             string            `json:"name"`
	Alias            string            `json:"alias,omitempty"`
	RSSI             int16             `json:"rssi"`
	Connected        bool              `json:"connected"`
	UUIDs            []string          `json:"uuids"`
	ManufacturerData map[uint16]string `json:"manufacturer_data"`
}

// Target identifies where a mutation goes.
type Target struct {
	Profile string `json:"profile,omitempty"`
	MAC     string `json:"mac,omitempty"`
}

// Filter narrows a discovery scan.
type Filter struct {
	MAC          string
	NameContains string
	ServiceUUID  string
}

// Frame is one encoded write (opaque until the protocol package lands).
type Frame struct {
	Op    string `json:"op"`
	Bytes []byte `json:"bytes,omitempty"`
	Note  string `json:"note,omitempty"`
}

// Transport is the minimal interface the CLI needs.
type Transport interface {
	Discover(ctx context.Context, f Filter) ([]Device, error)
	Send(ctx context.Context, t Target, frames []Frame) error
}

// NotImplemented is the shared send path used until the protocol is decoded.
func NotImplemented(what string) error {
	return clierrors.New(clierrors.CodeProtocol, "%s is not implemented yet: the BLE protocol is not decoded", what).
		WithHint("run 'syska-sonic-cli doctor --json' for status; discovery works today; protocol decoding from the vendor Android app is the next milestone in docs/agent.md").
		WithValid("--dry-run works now for every mutating command")
}

// DefaultTimeout bounds blocking transport calls.
const DefaultTimeout = 5 * time.Second

// RowsJSON encodes rows as RawMessage for job results.
func RowsJSON(rows []Device) json.RawMessage {
	b, _ := json.Marshal(rows)
	return b
}
