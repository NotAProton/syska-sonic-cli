// Package protocol encodes the Syska Sonic (BT speaker + LED module) control
// frames, byte-for-byte as the vendor Android app emits them over the SPP
// RFCOMM channel (com.syskaled.bt.a.a.* packet classes).
//
// Two frame families share one SPP stream:
//
//  1. "A2" light frames. Logically [0xA2, cmd, p0, p1, p2, p3, p4], but never
//     sent in plaintext: the app XORs the logical frame with a random key
//     byte and wraps it as
//     [len=10, key, 0xA2^key, cmd^key, p0^key, ..., p4^key, checksum]
//     where checksum makes all 10 bytes sum to 0 mod 256 (verified in smali,
//     com/syskaled/bt/a/b/b.smali virtual writer; jadx renders this call as a
//     misleading plaintext varargs call — do not trust the jadx text here).
//  2. UART bridge frames: [0x01, 0xFE, 0x00, 0x00, 'S', key, 0x10, 0, LE32, LE32]
//     for module system ops (tone), plus an ASCII passthrough variant. These
//     are sent in plaintext.
package protocol

import (
	"crypto/rand"
	"fmt"
)

const (
	opA2 = 0xA2 // light-channel op byte (logical frame start)

	CmdSolidColor  = 0x08 // g: solid color (control-page color wheel/buttons)
	CmdBrightness  = 0x0B // d: white/brightness (value byte, angle hi/lo, am)
	CmdModeGate    = 0x0C // f: mode gate with one payload byte
	CmdModeSwitch  = 0x0E // j: lamp switch — 1 turns the light off (live-verified), 0 re-enables
	CmdMixMode     = 0x11 // e: mix/effect family; subcommand byte 1..5 selects page/mode
	CmdColorQuick  = 0x12 // g: second color path (base-class c())
	CmdTestStep    = 0x10 // k: bare cmd (factory ageing-test path)
	CmdSleepTimer  = 0x13 // i: sleep-mode timer (on/off, minutes)
	CmdSavingPower = 0x14 // h: saving-power timer (on/off, minutes)
	CmdWakeUp      = 0x15 // l: wake-up mode fields
)

// frame builds one obfuscated A2 wire frame from the logical
// [0xA2, cmd, payload(5)] using the given XOR key byte.
// Wire: [10, key, 0xA2^key, cmd^key, payload^key..., checksum].
func frame(cmd byte, payload [5]byte, key byte) []byte {
	out := make([]byte, 10)
	out[0] = byte(len(out))
	out[1] = key
	out[2] = opA2 ^ key
	out[3] = cmd ^ key
	var sum byte
	for i, p := range payload {
		out[4+i] = p ^ key
	}
	for _, b := range out[:9] {
		sum += b
	}
	out[9] = 0 - sum
	return out
}

// frameRandom is frame with a fresh random key, as the app sends
// ((byte)(Math.random()*255) per packet).
func frameRandom(cmd byte, payload [5]byte) []byte {
	var kb [1]byte
	if _, err := rand.Read(kb[:]); err != nil {
		kb[0] = 0x5A
	}
	return frame(cmd, payload, kb[0]%255)
}

// SolidColor reproduces g(8, R, G, B): logical A2 08 R G B 0 0.
func SolidColor(r, g, b byte) []byte {
	return frameRandom(CmdSolidColor, [5]byte{r, g, b, 0, 0})
}

// ColorQuick reproduces the 0x12 variant, same shape as SolidColor.
func ColorQuick(r, g, b byte) []byte {
	return frameRandom(CmdColorQuick, [5]byte{r, g, b, 0, 0})
}

// Brightness reproduces d(11, p1, hi, lo, am): logical A2 0B p1 hi lo am 0.
func Brightness(p1 byte, value uint16, am byte) []byte {
	return frameRandom(CmdBrightness, [5]byte{
		p1, byte(value >> 8), byte(value), am, 0,
	})
}

// ModeGate reproduces f(12, x): logical A2 0C x 0 0 0 0.
func ModeGate(x byte) []byte {
	return frameRandom(CmdModeGate, [5]byte{x, 0, 0, 0, 0})
}

// MixMode reproduces e(17, sub, val, R, G, B): logical A2 11 sub val R G B
// (payload bytes in constructor order — no rotation).
func MixMode(sub, val, r, g, b byte) []byte {
	return frameRandom(CmdMixMode, [5]byte{sub, val, r, g, b})
}

// ModeSwitch reproduces j(14, x): logical A2 0E x 0 0 0 0.
// The app's lamp-control switch: x=1 turns the light fully off
// (live-verified); x=0 re-enables it.
func ModeSwitch(x byte) []byte {
	return frameRandom(CmdModeSwitch, [5]byte{x, 0, 0, 0, 0})
}

// TestStep reproduces k(16): logical A2 10 0 0 0 0 0.
func TestStep() []byte {
	return frameRandom(CmdTestStep, [5]byte{0, 0, 0, 0, 0})
}

// SleepTimer reproduces i(19, on, minutes): logical A2 13 on min 0 0 0.
func SleepTimer(on, minutes byte) []byte {
	return frameRandom(CmdSleepTimer, [5]byte{on, minutes, 0, 0, 0})
}

// SavingPower reproduces h(20, on, minutes): logical A2 14 on min 0 0 0.
func SavingPower(on, minutes byte) []byte {
	return frameRandom(CmdSavingPower, [5]byte{on, minutes, 0, 0, 0})
}

// WakeUp reproduces l(21, a, b, d, e, f): logical A2 15 a b d e f.
func WakeUp(a, b, d, e, f byte) []byte {
	return frameRandom(CmdWakeUp, [5]byte{a, b, d, e, f})
}

// --- UART bridge -----------------------------------------------------------

const (
	uartKeyTone = 0x9D // buildKey(4, 157) passes the app's 0x80..0xBF gate
	uartKeyStr  = 0x89 // buildKey(?) for string passthrough
)

// UARTTone renders the tone/system frame for on/off (ctor c(x): param u32 at
// offsets 8; zeros at 12..15):
//
//	01 FE 00 00 53 9D 10 00 <param LE32> 00 00 00 00
func UARTTone(param byte) []byte {
	f := make([]byte, 16)
	f[0], f[1] = 0x01, 0xFE
	f[4], f[5] = 0x53, uartKeyTone
	f[6] = 0x10
	f[8] = param
	return f
}

// UARTString renders the ASCII passthrough frame (b(String)):
//
//	01 FE 00 00 53 89 <len=text+18> 00*9 03 <text> 00
func UARTString(s string) ([]byte, error) {
	text := []byte(s)
	if len(text) == 0 {
		return nil, fmt.Errorf("empty UART payload")
	}
	if len(text) > 200 {
		return nil, fmt.Errorf("UART payload too long (%d bytes)", len(text))
	}
	f := make([]byte, 0, 6+1+10+len(text)+1)
	f = append(f, 0x01, 0xFE, 0x00, 0x00, 0x53, uartKeyStr, byte(len(text)+18))
	f = append(f, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0x03)
	f = append(f, text...)
	f = append(f, 0)
	return f, nil
}

// NamedColors mirrors the CLI color enum with concrete RGB triples.
func NamedColors() map[string][3]byte {
	return map[string][3]byte{
		"white":  {0xFF, 0xFF, 0xFF},
		"red":    {0xFF, 0x00, 0x00},
		"orange": {0xFF, 0x88, 0x00},
		"yellow": {0xFF, 0xFF, 0x00},
		"green":  {0x00, 0xCC, 0x00},
		"cyan":   {0x00, 0xCC, 0xFF},
		"blue":   {0x00, 0x00, 0xFF},
		"purple": {0x88, 0x00, 0xFF},
		"pink":   {0xFF, 0x8F, 0xBF},
	}
}
