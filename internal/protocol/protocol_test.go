package protocol

import (
	"bytes"
	"encoding/hex"
	"testing"
)

// Expected wire bytes are pinned from the smali-verified framing
// (com/syskaled/bt/a/b/b.smali virtual writer): logical [A2, cmd, p0..p4]
// XORed with a key byte, wrapped as [10, key, ...xored..., checksum] where
// all 10 bytes sum to 0 mod 256. Vectors below use key=0 unless noted, so
// the logical frame is visible after the 2-byte header.
func TestFramesKeyZero(t *testing.T) {
	tests := []struct {
		name string
		got  []byte
		want string // hex
	}{
		// app/a/d.java buttons: g(8, R,G,B); blue = -16776961, red = -65536
		{"solid blue", frame(CmdSolidColor, [5]byte{0, 0, 255, 0, 0}, 0), "0a00a2080000ff00004d"},
		{"solid red", frame(CmdSolidColor, [5]byte{255, 0, 0, 0, 0}, 0), "0a00a208ff000000004d"},
		// d(11, 55, hi(550), lo(550), 0)
		{"brightness", frame(CmdBrightness, [5]byte{55, 0x02, 0x26, 0, 0}, 0), "0a00a20b3702260000ea"},
		// app/a/g.java: f(12, 1) before mix-4
		{"mode gate", frame(CmdModeGate, [5]byte{1, 0, 0, 0, 0}, 0), "0a00a20c010000000047"},
		// app/a/s.java ALL_DONE: e(17, 1, 90, 0, 0, -1) — payload in ctor order
		{"mix1 blue90", frame(CmdMixMode, [5]byte{1, 90, 0, 0, 255}, 0), "0a00a211015a0000ffe9"},
		// app/a/g.java gradient: e(17, 4, 50, 255, 0, 0)
		{"mix4 red", frame(CmdMixMode, [5]byte{4, 50, 255, 0, 0}, 0), "0a00a2110432ff00000e"},
		// app/a/d.java switch: j(14, 1)
		{"mode switch on", frame(CmdModeSwitch, [5]byte{1, 0, 0, 0, 0}, 0), "0a00a20e010000000045"},
		// app/a/s.java test flow: k(16)
		{"test step", frame(CmdTestStep, [5]byte{0, 0, 0, 0, 0}, 0), "0a00a210000000000044"},
		// app/a/o.java sleep: i(19, 1, 30); app/a/l.java saving: h(20, 1, 30)
		{"sleep timer", frame(CmdSleepTimer, [5]byte{1, 30, 0, 0, 0}, 0), "0a00a213011e00000022"},
		{"saving power", frame(CmdSavingPower, [5]byte{1, 30, 0, 0, 0}, 0), "0a00a214011e00000021"},
		// app/a/t.java wake-up: l(21, a, b, d, e, f)
		{"wakeup", frame(CmdWakeUp, [5]byte{1, 2, 3, 4, 5}, 0), "0a00a215010203040530"},
		// UART family stays plaintext (bt/a/a/b.java, c.java)
		{"uart tone on", UARTTone(1), "01fe0000539d10000100000000000000"},
		{"uart tone off", UARTTone(0), "01fe0000539d10000000000000000000"},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			want, err := hex.DecodeString(tc.want)
			if err != nil {
				t.Fatalf("bad want hex: %v", err)
			}
			if !bytes.Equal(tc.got, want) {
				t.Errorf("got %x, want %x", tc.got, want)
			}
		})
	}
}

func TestFrameKeyNonZero(t *testing.T) {
	got := frame(CmdSolidColor, [5]byte{0, 0, 255, 0, 0}, 0x5A)
	want, _ := hex.DecodeString("0a5af8525a5aa55a5a45")
	if !bytes.Equal(got, want) {
		t.Errorf("got %x, want %x", got, want)
	}
}

// Public builders must always emit a well-formed obfuscated frame:
// 10 bytes, byte0=10, de-XOR yields [A2, cmd, payload...], checksum valid.
func TestBuildersWellFormed(t *testing.T) {
	builders := map[string]func() []byte{
		"solid":      func() []byte { return SolidColor(1, 2, 3) },
		"quick":      func() []byte { return ColorQuick(1, 2, 3) },
		"brightness": func() []byte { return Brightness(50, 500, 0) },
		"gate":       func() []byte { return ModeGate(1) },
		"mix":        func() []byte { return MixMode(4, 50, 255, 0, 0) },
		"switch":     func() []byte { return ModeSwitch(1) },
		"teststep":   func() []byte { return TestStep() },
		"sleep":      func() []byte { return SleepTimer(1, 30) },
		"saving":     func() []byte { return SavingPower(1, 30) },
		"wakeup":     func() []byte { return WakeUp(1, 2, 3, 4, 5) },
	}
	for name, build := range builders {
		f := build()
		if len(f) != 10 || f[0] != 10 {
			t.Errorf("%s: bad frame %x", name, f)
			continue
		}
		key := f[1]
		logical := make([]byte, 7)
		for i := 0; i < 7; i++ {
			logical[i] = f[2+i] ^ key
		}
		if logical[0] != opA2 {
			t.Errorf("%s: logical head %x, want a2 (%x)", name, logical[0], f)
		}
		var sum byte
		for _, b := range f {
			sum += b
		}
		if sum != 0 {
			t.Errorf("%s: checksum sum=%d, want 0 (%x)", name, sum, f)
		}
	}
}

func TestUARTString(t *testing.T) {
	got, err := UARTString("hi")
	if err != nil {
		t.Fatal(err)
	}
	// b.java: 01 FE 00 00 53 89 <len=2+18=20> 00*9 03 "hi" 00
	want, _ := hex.DecodeString("01fe000053891400000000000000000003686900")
	if !bytes.Equal(got, want) {
		t.Errorf("got %x, want %x", got, want)
	}
	if _, err := UARTString(""); err == nil {
		t.Error("empty payload should error")
	}
}
