// Package bulb holds the device-facing domain model: which properties exist,
// what values are accepted, and the validated intent ("plan") for each
// mutation. Frame encoding lives in the protocol package once the BLE
// protocol is decoded; the CLI and job ledger consume these plans.
package bulb

import (
	"encoding/hex"
	"fmt"
	"strconv"
	"strings"

	"github.com/notaproton/syska-sonic-cli/internal/clierrors"
)

var (
	Properties  = []string{"power", "brightness", "color", "effect"}
	Colors      = []string{"white", "red", "orange", "yellow", "green", "cyan", "blue", "purple", "pink"}
	Effects     = []string{"solid", "rainbow", "wave", "breathing", "flash"}
	PowerStates = []string{"on", "off"}
)

// Plan is a validated mutation intent, ready for protocol encoding or display.
type Plan struct {
	Property string `json:"property"`
	Value    any    `json:"value"`
	ColorHex string `json:"color_hex,omitempty"`
	Speed    int    `json:"speed,omitempty"`
	Op       string `json:"op"`
}

// ParseSet validates one mutation. Unknown names reject with the full
// accepted-value list so bad input teaches instead of dead-ends.
func ParseSet(property, value string, speed int, hasSpeed bool) (Plan, error) {
	plan := Plan{Property: strings.ToLower(strings.TrimSpace(property)), Op: "device.set"}
	switch plan.Property {
	case "power":
		v := strings.ToLower(strings.TrimSpace(value))
		for _, s := range PowerStates {
			if v == s {
				plan.Value = s
				return plan, nil
			}
		}
		return Plan{}, clierrors.New(clierrors.CodeUsage, "unknown power state %q", value).
			WithValid(PowerStates...).WithHint("e.g. syska-sonic-cli set power on")
	case "brightness":
		n, err := strconv.Atoi(strings.TrimSpace(value))
		if err != nil {
			return Plan{}, clierrors.New(clierrors.CodeUsage, "brightness %q is not an integer", value).
				WithHint("use a plain percent integer 0-100, e.g. 40")
		}
		if n < 0 || n > 100 {
			return Plan{}, clierrors.New(clierrors.CodeUsage, "brightness %d is outside 0-100", n).
				WithHint("brightness is a percent")
		}
		plan.Value = n
		return plan, nil
	case "color":
		name, hexv, err := ParseColor(value)
		if err != nil {
			return Plan{}, err
		}
		plan.Value = name
		plan.ColorHex = hexv
		return plan, nil
	case "effect":
		v := strings.ToLower(strings.TrimSpace(value))
		for _, e := range Effects {
			if v == e {
				plan.Value = e
				if hasSpeed && (speed < 0 || speed > 10) {
					return Plan{}, clierrors.New(clierrors.CodeUsage, "speed %d is outside 0-10", speed).
						WithHint("lower is slower; e.g. --speed 3")
				}
				plan.Speed = speed
				return plan, nil
			}
		}
		return Plan{}, clierrors.New(clierrors.CodeUsage, "unknown effect %q", value).
			WithValid(Effects...).WithHint("e.g. syska-sonic-cli set effect rainbow --speed 3")
	default:
		return Plan{}, clierrors.New(clierrors.CodeUsage, "unknown property %q", property).
			WithValid(Properties...).
			WithHint("'syska-sonic-cli agent-context --json' lists the enums; e.g. syska-sonic-cli set color red")
	}
}

// ParseColor accepts a curated name, #hex (#rgb or #rrggbb), or an r,g,b
// triple; it returns (name, "#rrggbb", err). Names still resolve to hex so the
// protocol layer eventually gets deterministic values.
func ParseColor(spec string) (string, string, error) {
	spec = strings.TrimSpace(spec)
	if strings.Contains(spec, ",") {
		parts := strings.Split(spec, ",")
		if len(parts) != 3 {
			return "", "", clierrors.New(clierrors.CodeUsage, "color %q needs exactly three comma-separated channels", spec).
				WithHint("formats: r,g,b e.g. 255,128,0; or a named color: %s", strings.Join(Colors, ", "))
		}
		var rgb [3]int
		for i, p := range parts {
			n, err := strconv.Atoi(strings.TrimSpace(p))
			if err != nil || n < 0 || n > 255 {
				return "", "", clierrors.New(clierrors.CodeUsage, "color channel %q is not an integer 0-255", strings.TrimSpace(p)).
					WithHint("e.g. 255,128,0")
			}
			rgb[i] = n
		}
		return "", fmt.Sprintf("#%02x%02x%02x", rgb[0], rgb[1], rgb[2]), nil
	}
	if strings.HasPrefix(spec, "#") || strings.HasPrefix(spec, "0x") {
		digits := strings.TrimPrefix(strings.TrimPrefix(spec, "#"), "0x")
		switch len(digits) {
		case 3:
			expanded := make([]byte, 6)
			for i, c := range []byte(digits) {
				expanded[2*i], expanded[2*i+1] = c, c
			}
			raw, err := hex.DecodeString(string(expanded))
			if err != nil {
				return "", "", clierrors.New(clierrors.CodeUsage, "color %q is not valid hex", spec).
					WithHint("formats: #rgb, #rrggbb; named colors: %s", strings.Join(Colors, ", "))
			}
			return "", fmt.Sprintf("#%02x%02x%02x", raw[0], raw[1], raw[2]), nil
		case 6:
			raw, err := hex.DecodeString(digits)
			if err != nil {
				return "", "", clierrors.New(clierrors.CodeUsage, "color %q is not valid hex", spec).
					WithHint("formats: #rgb, #rrggbb; named colors: %s", strings.Join(Colors, ", "))
			}
			return "", fmt.Sprintf("#%02x%02x%02x", raw[0], raw[1], raw[2]), nil
		default:
			return "", "", clierrors.New(clierrors.CodeUsage, "color %q has %d hex digits", spec, len(digits)).
				WithHint("formats: #rgb (3) or #rrggbb (6); named colors: %s", strings.Join(Colors, ", "))
		}
	}
	norm := strings.ToLower(spec)
	for _, c := range Colors {
		if norm == c {
			return norm, NameHex(norm), nil
		}
	}
	return "", "", clierrors.New(clierrors.CodeUsage, "unknown color %q", spec).
		WithValid(Colors...).
		WithHint("also accepts: #rgb, #rrggbb, or r,g,b integers; e.g. set color 255,128,0")
}

// NameHex maps the curated palette to RGB triples.
func NameHex(name string) string {
	switch name {
	case "white":
		return "#ffffff"
	case "red":
		return "#ff0000"
	case "orange":
		return "#ff8800"
	case "yellow":
		return "#ffff00"
	case "green":
		return "#00cc00"
	case "cyan":
		return "#00ccff"
	case "blue":
		return "#0000ff"
	case "purple":
		return "#8800ff"
	case "pink":
		return "#ff8fbf"
	}
	return "#000000"
}

// ParseGet validates a property probe.
func ParseGet(property string) (string, error) {
	p := strings.ToLower(strings.TrimSpace(property))
	for _, v := range Properties {
		if v == p {
			return p, nil
		}
	}
	return "", clierrors.New(clierrors.CodeUsage, "unknown property %q", property).
		WithValid(Properties...).
		WithHint("omit the argument to read the full snapshot; e.g. 'syska-sonic-cli get color'")
}

// JSONEnvelopeHint is reused as an "artifact" example where useful.
const JSONEnvelopeHint string = "envelope keys: ok, command, op, data, warnings, error"
