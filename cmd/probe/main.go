package main

import (
	"context"
	"encoding/hex"
	"fmt"
	"os"
	"time"

	"github.com/notaproton/syska-sonic-cli/internal/protocol"
	"github.com/notaproton/syska-sonic-cli/internal/transport"
)

// Single-session calibration battery. The module drops SPP sessions after
// ~12-15s, so steps are ~1.4s apart and the whole run stays short. Watch
// the light and note which STEP number changes it (color, brightness,
// beep, nothing). Frames come from internal/protocol, which reproduces
// the app's XOR-obfuscated A2 framing (see protocol.go).
func main() {
	mac := os.Getenv("SSCLI_MAC")
	if len(os.Args) > 1 {
		mac = os.Args[1]
	}
	if mac == "" {
		fmt.Println("usage: probe <classic-bt-mac>  (or set SSCLI_MAC)")
		os.Exit(2)
	}

	type step struct {
		label  string
		frames [][]byte
		hold   time.Duration
	}
	steps := []step{
		{"sequenced OFF (brightness 0 + lamp switch)", [][]byte{protocol.Brightness(0, 0, 0), protocol.ModeSwitch(1)}, 3 * time.Second},
		{"solid BLUE — hold 6s", [][]byte{protocol.ModeSwitch(0), protocol.SolidColor(0, 0, 255)}, 6 * time.Second},
		{"restore WHITE + brightness up", [][]byte{protocol.SolidColor(255, 255, 255), protocol.Brightness(100, 1000, 0)}, 500 * time.Millisecond},
	}

	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	conn, err := transport.DialSPP(ctx, mac, 2)
	if err != nil {
		fmt.Println("DIAL:", err)
		os.Exit(1)
	}
	defer conn.Close()
	fmt.Println("connected (single session) — watch the light NOW")

	go func() {
		buf := make([]byte, 512)
		for {
			n, rerr := conn.Read(buf)
			if rerr != nil || n == 0 {
				return
			}
			fmt.Printf("      RESP % x\n", buf[:n])
		}
	}()

	for i, s := range steps {
		fmt.Printf("STEP %d: %s\n", i, s.label)
		for _, f := range s.frames {
			fmt.Printf("        -> %s\n", hex.EncodeToString(f))
			if _, werr := conn.Write(f); werr != nil {
				fmt.Println("write failed:", werr)
				return
			}
			time.Sleep(150 * time.Millisecond)
		}
		time.Sleep(s.hold)
	}
	fmt.Println("done — report which STEP numbers changed the light")
}
