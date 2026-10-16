//go:build linux
// +build linux

package transport

import (
	"context"
	"fmt"
	"sort"
	"strings"

	"github.com/godbus/dbus/v5"

	"github.com/notaproton/syska-sonic-cli/internal/clierrors"
)

const (
	bluezName   = "org.bluez"
	objMgrIface = "org.freedesktop.DBus.ObjectManager"
)

type objects map[dbus.ObjectPath]map[string]map[string]dbus.Variant

// Discover runs a bounded LE scan through BlueZ over the system bus.
func Discover(ctx context.Context, f Filter) ([]Device, error) {
	conn, err := dbus.SystemBus()
	if err != nil {
		return nil, clierrors.New(clierrors.CodeDevice, "cannot reach BlueZ on the system bus").
			WithCause(err).
			WithHint("is the bluetooth daemon running? try: systemctl status bluetooth")
	}

	root := conn.Object(bluezName, dbus.ObjectPath("/"))
	var objs objects
	if err := root.Call(objMgrIface+".GetManagedObjects", 0).Store(&objs); err != nil {
		return nil, clierrors.New(clierrors.CodeDevice, "BlueZ object enumeration failed").
			WithCause(err).
			WithHint("is the bluetooth daemon running? try: systemctl status bluetooth")
	}

	var adapter dbus.ObjectPath
	for path, ifs := range objs {
		if _, ok := ifs["org.bluez.Adapter1"]; ok {
			adapter = path
			break
		}
	}
	if adapter == "" {
		return nil, clierrors.New(clierrors.CodeDevice, "no Bluetooth adapter found").
			WithHint("check hardware: lsusb; rfkill list; hciconfig -a")
	}

	adapter1 := conn.Object(bluezName, adapter)
	df := map[string]dbus.Variant{"Transport": dbus.MakeVariant("le")}
	if err := adapter1.Call("org.bluez.Adapter1.SetDiscoveryFilter", 0, df).Err; err != nil {
		return nil, bluetoothErr("set discovery filter", err)
	}
	if err := adapter1.Call("org.bluez.Adapter1.StartDiscovery", 0).Err; err != nil {
		if !strings.Contains(strings.ToLower(err.Error()), "inprogress") {
			return nil, bluetoothErr("start discovery", err).
				WithHint("is the adapter powered? try: bluetoothctl power on; rfkill unblock bluetooth")
		}
	}

	// Wait the caller-provided scan window.
	select {
	case <-ctx.Done():
	}

	// Re-read managed objects populated by the scan window.
	if err := root.Call(objMgrIface+".GetManagedObjects", 0).Store(&objs); err != nil {
		return nil, bluetoothErr("enumerate discovered devices", err)
	}
	_ = adapter1.Call("org.bluez.Adapter1.StopDiscovery", 0) // best effort

	rows := collectDevices(objs, f)
	sortRows(rows)
	return rows, nil
}

func bluetoothErr(action string, cause error) *clierrors.Error {
	return clierrors.New(clierrors.CodeDevice, "BlueZ %s failed", action).WithCause(cause).
		WithHint("if this persists: system-level bluetooth is likely degraded; run 'journalctl -b -u bluetooth'")
}

func collectDevices(objs objects, f Filter) []Device {
	rows := make([]Device, 0, 16)
	for _, ifs := range objs {
		props, ok := ifs["org.bluez.Device1"]
		if !ok {
			continue
		}
		row := Device{
			Address:   strVal(props["Address"]),
			Alias:     strVal(props["Alias"]),
			Name:      strVal(props["Name"]),
			Connected: boolVal(props["Connected"]),
			RSSI:      int16Val(props["RSSI"]),
			UUIDs:     strSlice(props["UUIDs"]),
		}
		if row.Name == "" {
			row.Name = row.Alias
		}
		if row.Address == "" {
			continue
		}
		row.ManufacturerData = mfData(props["ManufacturerData"])
		if f.ServiceUUID != "" && !containsFold(row.UUIDs, f.ServiceUUID) {
			continue
		}
		if f.NameContains != "" && !strings.Contains(strings.ToLower(row.Name), strings.ToLower(f.NameContains)) {
			continue
		}
		if f.MAC != "" && !strings.EqualFold(row.Address, f.MAC) {
			continue
		}
		row.Address = strings.ToUpper(row.Address)
		rows = append(rows, row)
	}
	return rows
}

func sortRows(rows []Device) {
	sort.SliceStable(rows, func(i, j int) bool {
		ri, rj := rssiForSort(rows[i]), rssiForSort(rows[j])
		if ri != rj {
			return ri > rj
		}
		return rows[i].Address < rows[j].Address
	})
}

// rssiForSort keeps unknown/absent RSSI (0) last in the strong-first order.
func rssiForSort(d Device) int16 {
	if d.RSSI == 0 {
		return -128
	}
	return d.RSSI
}

func strVal(v dbus.Variant) string {
	if s, ok := v.Value().(string); ok {
		return s
	}
	return ""
}

func boolVal(v dbus.Variant) bool {
	if b, ok := v.Value().(bool); ok {
		return b
	}
	return false
}

func int16Val(v dbus.Variant) int16 {
	switch t := v.Value().(type) {
	case int16:
		return t
	case int:
		return int16(t)
	case int32:
		return int16(t)
	case uint16:
		return int16(t)
	case uint:
		return int16(t)
	default:
		return 0
	}
}

func strSlice(v dbus.Variant) []string {
	s, ok := v.Value().([]string)
	if !ok {
		return nil
	}
	return s
}

// mfData tolerates the several shapes godbus may decode a{qv} into.
func mfData(v dbus.Variant) map[uint16]string {
	out := map[uint16]string{}
	switch dict := v.Value().(type) {
	case map[dbus.Variant]dbus.Variant:
		for key, val := range dict {
			if id, ok := key.Value().(uint16); ok {
				if payload := encodeBytes(val.Value()); payload != "" {
					out[id] = payload
				}
			}
		}
	case map[dbus.Variant][]byte:
		for key, payloadBytes := range dict {
			if id, ok := key.Value().(uint16); ok {
				out[id] = fmt.Sprintf("%x", payloadBytes)
			}
		}
	case map[dbus.Variant]any:
		for key, val := range dict {
			if id, ok := key.Value().(uint16); ok {
				if payload := encodeBytes(val); payload != "" {
					out[id] = payload
				}
			}
		}
	case map[uint16]dbus.Variant:
		for id, val := range dict {
			if payload := encodeBytes(val.Value()); payload != "" {
				out[id] = payload
			}
		}
	case map[uint16][]byte:
		for id, payloadBytes := range dict {
			out[id] = fmt.Sprintf("%x", payloadBytes)
		}
	}
	return out
}

func encodeBytes(value any) string {
	switch pb := value.(type) {
	case []byte:
		return fmt.Sprintf("%x", pb)
	case []any:
		bs := make([]byte, 0, len(pb))
		for _, item := range pb {
			switch n := item.(type) {
			case byte:
				bs = append(bs, n)
			case int:
				if n >= 0 && n <= 255 {
					bs = append(bs, byte(n))
				}
			default:
				return ""
			}
		}
		return fmt.Sprintf("%x", bs)
	}
	return ""
}

func containsFold(list []string, probe string) bool {
	for _, item := range list {
		if strings.EqualFold(item, probe) {
			return true
		}
	}
	return false
}
