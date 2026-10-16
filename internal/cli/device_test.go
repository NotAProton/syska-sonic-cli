package cli

import (
	"bytes"
	"testing"

	"github.com/notaproton/syska-sonic-cli/internal/bulb"
	"github.com/notaproton/syska-sonic-cli/internal/protocol"
)

// logical de-XORs one obfuscated A2 wire frame back to [A2, cmd, payload...].
func logical(t *testing.T, f []byte) []byte {
	t.Helper()
	if len(f) != 10 || f[0] != 10 {
		t.Fatalf("bad wire frame %x", f)
	}
	key := f[1]
	out := make([]byte, 7)
	for i := range out {
		out[i] = f[2+i] ^ key
	}
	var sum byte
	for _, b := range f {
		sum += b
	}
	if sum != 0 {
		t.Fatalf("checksum invalid in %x", f)
	}
	return out
}

func TestPlanFramesColorIsSingleSolidFrame(t *testing.T) {
	plan, err := bulb.ParseSet("color", "#0000ff", 5, false)
	if err != nil {
		t.Fatal(err)
	}
	frames := planFrames(plan)
	if len(frames) != 1 {
		t.Fatalf("want exactly 1 frame, got %d", len(frames))
	}
	want := []byte{0xA2, protocol.CmdSolidColor, 0, 0, 0xFF, 0, 0}
	if got := logical(t, frames[0]); !bytes.Equal(got, want) {
		t.Errorf("logical = %x, want %x", got, want)
	}
}

func TestPlanFramesEffectUsesMixMode(t *testing.T) {
	plan, err := bulb.ParseSet("effect", "rainbow", 3, true)
	if err != nil {
		t.Fatal(err)
	}
	frames := planFrames(plan)
	if len(frames) == 0 {
		t.Fatal("want frames for effect")
	}
	got := logical(t, frames[len(frames)-1])
	if got[1] != protocol.CmdMixMode || got[2] != 0x03 {
		t.Errorf("logical = %x, want mix mode sub 3", got)
	}
}

func TestPlanFramesPowerUsesBrightness(t *testing.T) {
	off, err := bulb.ParseSet("power", "off", 5, false)
	if err != nil {
		t.Fatal(err)
	}
	frames := planFrames(off)
	if len(frames) != 2 {
		t.Fatalf("power off: want 2 frames (brightness 0 + lamp switch), got %d", len(frames))
	}
	wantDim := []byte{0xA2, protocol.CmdBrightness, 0, 0, 0, 0, 0}
	if got := logical(t, frames[0]); !bytes.Equal(got, wantDim) {
		t.Errorf("power off frame0 = %x, want %x", got, wantDim)
	}
	wantOff := []byte{0xA2, protocol.CmdModeSwitch, 1, 0, 0, 0, 0}
	if got := logical(t, frames[1]); !bytes.Equal(got, wantOff) {
		t.Errorf("power off frame1 = %x, want %x", got, wantOff)
	}

	on, err := bulb.ParseSet("power", "on", 5, false)
	if err != nil {
		t.Fatal(err)
	}
	frames = planFrames(on)
	if len(frames) != 2 {
		t.Fatalf("power on: want 2 frames (wake + brightness), got %d", len(frames))
	}
	wantWake := []byte{0xA2, protocol.CmdModeSwitch, 0, 0, 0, 0, 0}
	if got := logical(t, frames[0]); !bytes.Equal(got, wantWake) {
		t.Errorf("power on frame0 = %x, want %x", got, wantWake)
	}
	wantOn := []byte{0xA2, protocol.CmdBrightness, 100, 0x03, 0xE8, 0, 0}
	if got := logical(t, frames[1]); !bytes.Equal(got, wantOn) {
		t.Errorf("power on frame1 = %x, want %x", got, wantOn)
	}
}

func TestPlanFramesBrightnessZeroUsesLampSwitch(t *testing.T) {
	plan, err := bulb.ParseSet("brightness", "0", 5, false)
	if err != nil {
		t.Fatal(err)
	}
	frames := planFrames(plan)
	if len(frames) != 2 {
		t.Fatalf("brightness 0: want 2 frames (brightness 0 + lamp switch), got %d", len(frames))
	}
	if got := logical(t, frames[0]); got[1] != protocol.CmdBrightness {
		t.Errorf("brightness 0 frame0 = %x, want brightness", got)
	}
	want := []byte{0xA2, protocol.CmdModeSwitch, 1, 0, 0, 0, 0}
	if got := logical(t, frames[1]); !bytes.Equal(got, want) {
		t.Errorf("brightness 0 frame1 = %x, want %x", got, want)
	}

	plan, err = bulb.ParseSet("brightness", "50", 5, false)
	if err != nil {
		t.Fatal(err)
	}
	frames = planFrames(plan)
	if len(frames) != 2 {
		t.Fatalf("brightness 50: want 2 frames (wake + brightness), got %d", len(frames))
	}
	if got := logical(t, frames[0]); got[1] != protocol.CmdModeSwitch {
		t.Errorf("brightness 50 frame0 = %x, want mode switch (wake)", got)
	}
	if got := logical(t, frames[1]); got[1] != protocol.CmdBrightness {
		t.Errorf("brightness 50 frame1 = %x, want brightness", got)
	}
}
