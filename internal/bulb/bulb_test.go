package bulb

import (
	"strings"
	"testing"

	"github.com/notaproton/syska-sonic-cli/internal/clierrors"
)

func TestParseSetValid(t *testing.T) {
	if p, err := ParseSet("power", "ON", 0, false); err != nil || p.Value != "on" {
		t.Fatalf("case-insensitive power: %+v %v", p, err)
	}
	p, err := ParseSet("brightness", "40", 0, false)
	if err != nil || p.Value != 40 {
		t.Fatalf("brightness: %+v %v", p, err)
	}
	p, err = ParseSet("effect", "rainbow", 3, true)
	if err != nil || p.Value != "rainbow" || p.Speed != 3 {
		t.Fatalf("effect: %+v %v", p, err)
	}
	p, err = ParseSet("color", "#ff8800", 0, false)
	if err != nil || p.Value != "" || p.ColorHex != "#ff8800" {
		t.Fatalf("hex color: %+v %v", p, err)
	}
	p, err = ParseSet("color", "255,128,0", 0, false)
	if err != nil || p.ColorHex != "#ff8000" {
		t.Fatalf("rgb color: %+v %v", p, err)
	}
	p, err = ParseSet("color", "red", 0, false)
	if err != nil || p.Value != "red" || p.ColorHex != "#ff0000" {
		t.Fatalf("named color: %+v %v", p, err)
	}
	p, err = ParseSet("color", "#f80", 0, false)
	if err != nil || p.ColorHex != "#ff8800" {
		t.Fatalf("short hex: %+v %v", p, err)
	}
}

func TestParseSetRejectsWithTeaching(t *testing.T) {
	check := func(prop, value string, wantValid string) {
		t.Helper()
		_, err := ParseSet(prop, value, 0, false)
		ce, ok := cliErrOf(err)
		if !ok || ce.Code != clierrors.CodeUsage {
			t.Fatalf("expected usage error for %s=%q, got %v", prop, value, err)
		}
		if !strings.Contains(ce.Message, prop) && !strings.Contains(ce.Message, value) {
			t.Fatalf("message should mention the bad input: %q", ce.Message)
		}
	}
	check("power", "maybe", "on, off")
	check("ledstrip", "99", "power, brightness") // unknown property rejects loudly
	check("brightness", "400", "0-100")
	check("brightness", "forty", "integer")
	check("color", "magenta", "named colors")
	check("color", "255,999,0", "0-255")
	check("effect", "shimmer", "solid, rainbow")
}

func TestSpeedAppliesOnlyToEffect(t *testing.T) {
	if _, err := ParseSet("brightness", "30", 7, true); err != nil {
		t.Fatalf("speed irrelevant for brightness: %v", err)
	}
}

func TestParseGet(t *testing.T) {
	if p, err := ParseGet("COLOR"); err != nil || p != "color" {
		t.Fatalf("get parse: %q %v", p, err)
	}
	_, err := ParseGet("temperature")
	if err == nil {
		t.Fatal("unknown property accepted")
	}
}

func cliErrOf(err error) (*clierrors.Error, bool) {
	ce, ok := err.(*clierrors.Error)
	return ce, ok
}
