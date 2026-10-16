package cli

import (
	"context"
	"encoding/hex"
	"fmt"
	"os/exec"
	"strconv"
	"strings"
	"time"

	"github.com/spf13/cobra"

	"github.com/notaproton/syska-sonic-cli/internal/bulb"
	"github.com/notaproton/syska-sonic-cli/internal/clierrors"
	"github.com/notaproton/syska-sonic-cli/internal/config"
	"github.com/notaproton/syska-sonic-cli/internal/output"
	"github.com/notaproton/syska-sonic-cli/internal/protocol"
	"github.com/notaproton/syska-sonic-cli/internal/transport"
)

type intent struct {
	Target  config.Target `json:"target"`
	Plan    any           `json:"plan,omitempty"`
	MAC     string        `json:"mac,omitempty"`
	Name    string        `json:"name,omitempty"`
	Profile string        `json:"profile,omitempty"`
	Frames  []string      `json:"frames_hex,omitempty"`
}

// resolveTarget is shared by get/set/pair; when neither the flag, the env,
// nor the default profile yields a target, reject with a teachable error.
func (a *App) resolveTarget(mac string) (config.Target, error) {
	target, err := config.ResolveTarget(a.strFlag("profile"), mac)
	if err != nil {
		return target, err
	}
	if target.Profile == "" && target.MAC == "" {
		cfg, cerr := config.Load()
		if cerr != nil {
			return target, cerr
		}
		return target, clierrors.New(clierrors.CodeUsage, "no target: no default profile, no --profile, no --mac").
			WithValid(cfg.Names()...).
			WithHint("create a profile: syska-sonic-cli profile add --name desk --mac AA:BB:CC:DD:EE:FF; or pass --profile <name> / --mac per invocation")
	}
	return target, nil
}

// classicMAC picks the device MAC to reach: the flag/env/profile MAC itself.
// Covering MAC-type rotation is a future feature (LE identity resolution).
func needsTargetMAC(target config.Target, what string) (string, error) {
	if target.MAC == "" {
		return "", clierrors.New(clierrors.CodeUsage, "%s needs a MAC address", what).
			WithHint("the profile has no MAC yet: pair it (syska-sonic-cli pair --mac AA:BB:CC:DD:EE:FF) or set the profile MAC or env SSCLI_MAC")
	}
	return target.MAC, nil
}

// dialSPPWithLadder dials SPP channel 2; on failure it asks BlueZ to
// (re)establish the classic link (the module drops its ACL between
// sessions, and a just-closed socket can read as busy) and retries once.
func dialSPPWithLadder(ctx context.Context, mac string) (*transport.SPPConn, error) {
	conn, err := transport.DialSPP(ctx, mac, 2)
	if err == nil {
		return conn, nil
	}
	_, _ = exec.Command("bluetoothctl", "connect", mac).CombinedOutput()
	time.Sleep(900 * time.Millisecond)
	return transport.DialSPP(ctx, mac, 2)
}

// sendFrames dials SPP, writes the frames, drains responses, closes.
func sendFrames(ctx context.Context, mac string, frames [][]byte) (notes []string, err error) {
	if len(frames) == 0 {
		return nil, nil
	}
	conn, derr := dialSPPWithLadder(ctx, mac)
	if derr != nil {
		return nil, derr
	}
	defer conn.Close()

	// best-effort async reader for the module's status frames
	type respLine struct {
		data []byte
	}
	respCh := make(chan respLine, 16)
	done := make(chan struct{})
	defer close(done)
	go func() {
		buf := make([]byte, 512)
		for {
			n, rerr := conn.Read(buf)
			if rerr != nil || n == 0 {
				return
			}
			b := make([]byte, n)
			copy(b, buf[:n])
			select {
			case respCh <- respLine{data: b}:
			case <-done:
				return
			}
		}
	}()

	sent := make([]string, 0, len(frames))
	for _, frame := range frames {
		if _, werr := conn.Write(frame); werr != nil {
			return sent, werr
		}
		sent = append(sent, hex.EncodeToString(frame))
		time.Sleep(60 * time.Millisecond)
	}
	// drain 500ms for any ack
	deadline := time.Now().Add(500 * time.Millisecond)
	for {
		select {
		case r := <-respCh:
			notes = append(notes, "device: "+hex.EncodeToString(r.data))
		default:
			if time.Now().After(deadline) {
				return sent, nil
			}
			time.Sleep(25 * time.Millisecond)
		}
	}
}

func (a *App) newGetCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "get [property]",
		Short: "Read bulb state; no property = full snapshot",
		RunE: func(cmd *cobra.Command, args []string) error {
			if len(args) > 1 {
				return a.render(cmd, output.Outcome{
					Err: clierrors.New(clierrors.CodeUsage, "get takes at most one property").
						WithHint("e.g. 'syska-sonic-cli get color'; omit for the full snapshot"),
				})
			}
			property := ""
			if len(args) == 1 {
				p, perr := bulb.ParseGet(args[0])
				if perr != nil {
					return a.render(cmd, output.Outcome{Err: perr})
				}
				property = p
			}
			target, terr := a.resolveTarget("")
			if terr != nil {
				return a.render(cmd, output.Outcome{Err: terr})
			}
			warnings := []string{}
			if a.boolFlag("dry-run") {
				warnings = append(warnings, "--dry-run is a no-op for read commands")
			}
			payload := intent{Target: target}
			if property == "" {
				payload.Plan = bulb.Plan{Op: "device.snapshot"}
			} else {
				payload.Plan = bulb.Plan{Op: "device.get", Property: property}
			}
			return a.render(cmd, output.Outcome{
				Data:     dataOf(payload),
				Warnings: warnings,
				Err:      transport.NotImplemented(fmt.Sprintf("get %s", property)),
			})
		},
	}
}

func (a *App) newSetCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "set <property> <value>",
		Short: "Change one bulb property (power, brightness, color, effect)",
		RunE:  a.runSet,
	}
	cmd.Flags().Int("speed", 5, "Effect speed 0-10 (lower = slower); effect changes only")
	return cmd
}

func pairFailErr(mac string, out []byte) *clierrors.Error {
	ce := clierrors.New(clierrors.CodeDevice, "bluetoothctl pair %s failed", mac)
	ce.Hint = "run it manually to accept any prompt: bluetoothctl pair " + mac
	ce.WithCause(fmt.Errorf("%s", strings.TrimSpace(string(out))))
	return ce
}

func planSummary(plan bulb.Plan) string {
	value := fmt.Sprintf("%v", plan.Value)
	if plan.Property == "color" && plan.ColorHex != "" {
		value = plan.ColorHex
	}
	out := fmt.Sprintf("set %s %s", plan.Property, value)
	if plan.Speed > 0 {
		out += fmt.Sprintf(" --speed %d", plan.Speed)
	}
	return out
}

// planFrames converts a validated set-plan into protocol frames.
func planFrames(plan bulb.Plan) [][]byte {
	switch plan.Property {
	case "color":
		r, g, b, _ := resolveRGB(plan)
		// Live-confirmed winner (2026-10-16): the control page's solid
		// color op g(0x08). The other candidate paths (0x12, mix modes)
		// are effect modes, not solid color; sending them after the
		// solid frame would override it.
		return [][]byte{protocol.SolidColor(r, g, b)}
	case "power":
		// The app's lamp switch j(0x0E, 1) is a true off (live-verified);
		// brightness/color frames alone only dim. Power-on re-enables
		// with j(0x0E, 0) and raises the white channel to full.
		if plan.Value == "off" {
			// Sequenced (live-verified from a color state): drop the
			// white channel first, then the lamp switch turns it off.
			return [][]byte{protocol.Brightness(0, 0, 0), protocol.ModeSwitch(1)}
		}
		return [][]byte{protocol.ModeSwitch(0), protocol.Brightness(100, 1000, 0)}
	case "brightness":
		n, ok := plan.Value.(int)
		if !ok {
			return nil
		}
		if n == 0 {
			// Zero must mean off (live-verified): sequence the white
			// channel down, then the lamp switch. Either frame alone
			// only dims from a color state.
			return [][]byte{protocol.Brightness(0, 0, 0), protocol.ModeSwitch(1)}
		}
		value := uint16(n) * 10 // observed live: percent*10 over uint16 dims/raises
		return [][]byte{
			protocol.ModeSwitch(0), // wake first if lamp was switched off
			protocol.Brightness(byte(n), value, 0x00),
		}
	case "effect":
		name, ok := plan.Value.(string)
		if !ok {
			return nil
		}
		val := byte(plan.Speed * 10) // CLI speed 0-10 -> app slider 0-100 (uncalibrated)
		switch name {
		case "solid":
			return [][]byte{protocol.SolidColor(0xFF, 0xFF, 0xFF)}
		case "rainbow": // app k.java: e(17, 3, val, blue) — color hardcoded
			return [][]byte{protocol.MixMode(0x03, val, 0x00, 0x00, 0xFF)}
		case "flash": // flicker page, sub 1
			return [][]byte{protocol.MixMode(0x01, val, 0xFF, 0xFF, 0xFF)}
		case "breathing": // pulsation page, sub 2
			return [][]byte{protocol.MixMode(0x02, val, 0xFF, 0xFF, 0xFF)}
		case "wave": // gradient page, sub 4 (app precedes it with gate f(12,1))
			return [][]byte{protocol.ModeGate(0x01), protocol.MixMode(0x04, val, 0xFF, 0xFF, 0xFF)}
		}
		return nil
	default:
		return nil
	}
}

func resolveRGB(plan bulb.Plan) (byte, byte, byte, bool) {
	if plan.ColorHex != "" {
		const off = len("#rrggbb") - 6
		if len(plan.ColorHex) < off+6 {
			return 0, 0, 0, false
		}
		v, err := strconv.ParseUint(plan.ColorHex[1:], 16, 32)
		if err != nil {
			return 0, 0, 0, false
		}
		return byte(v >> 16 & 0xff), byte(v >> 8 & 0xff), byte(v & 0xff), true
	}
	if name, ok := plan.Value.(string); ok {
		if rgb, found := protocol.NamedColors()[name]; found {
			return rgb[0], rgb[1], rgb[2], true
		}
	}
	return 0, 0, 0, false
}

func (a *App) runSet(cmd *cobra.Command, args []string) error {
	if len(args) != 2 {
		return a.render(cmd, output.Outcome{
			Err: clierrors.New(clierrors.CodeUsage, "set needs exactly '<property> <value>', got %d argument(s)", len(args)).
				WithValid(bulb.Properties...).
				WithHint("examples: syska-sonic-cli set power on | set brightness 40 | set color #ff8800 | set effect rainbow --speed 3"),
		})
	}
	speed := intFlagLocal(cmd, "speed", 5)
	speedChanged := cmd.Flags().Lookup("speed").Changed
	plan, perr := bulb.ParseSet(args[0], args[1], speed, speedChanged)
	if perr == nil && speedChanged && plan.Property != "effect" {
		perr = clierrors.New(clierrors.CodeUsage, "--speed applies only to effect changes").
			WithHint("set effect ... --speed 0-10")
	}
	if perr != nil {
		return a.render(cmd, output.Outcome{Err: perr})
	}

	target, terr := a.resolveTarget("")
	if terr != nil {
		return a.render(cmd, output.Outcome{Err: terr})
	}
	mac, merr := needsTargetMAC(target, "set "+plan.Property)
	if merr != nil {
		return a.render(cmd, output.Outcome{Err: merr})
	}

	frames := planFrames(plan)
	if len(frames) == 0 {
		return a.render(cmd, output.Outcome{
			Err: clierrors.New(clierrors.CodeProtocol, "protocol calibration pending for %s", planSummary(plan)).
				WithHint("sendraw <hex> probes bytes directly; 'docs/re-notes.md' tracks the mapping"),
		})
	}
	hexf := make([]string, 0, len(frames))
	for _, f := range frames {
		hexf = append(hexf, strings.ToLower(hex.EncodeToString(f)))
	}
	payload := intent{Target: target, MAC: mac, Plan: plan, Frames: hexf}
	if a.boolFlag("dry-run") {
		human := []string{"would: " + summaryOfFrames(plan, frames)}
		return a.render(cmd, output.Outcome{Human: human, Data: dataOf(payload)})
	}

	dur, derr := a.durFlagOrDefault(5 * time.Second)
	if derr != nil {
		return a.render(cmd, output.Outcome{Err: derr})
	}
	ctx, cancel := context.WithTimeout(context.Background(), dur)
	defer cancel()
	sent, serr := sendFrames(ctx, mac, frames)
	warnings := []string{}
	for _, s := range sent {
		warnings = append(warnings, "sent "+s)
	}
	if serr != nil && len(sent) == 0 {
		return a.render(cmd, output.Outcome{
			Data: dataOf(payload),
			Err:  clierrors.New(clierrors.CodeDevice, "send to %s failed", mac).WithCause(serr),
		})
	}
	if serr != nil {
		warnings = append(warnings, "partial send: "+serr.Error())
	}
	return a.render(cmd, output.Outcome{
		Human:    []string{summaryOfFrames(plan, frames) + " sent to " + mac},
		Data:     dataOf(payload),
		Warnings: warnings,
	})
}

func summaryOfFrames(plan bulb.Plan, frames [][]byte) string {
	parts := make([]string, 0, len(frames))
	for _, f := range frames {
		parts = append(parts, hex.EncodeToString(f))
	}
	return planSummary(plan) + " (" + strings.Join(parts, " ") + ")"
}

func (a *App) newPairCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "pair",
		Short: "Pair the bulb over classic Bluetooth and record it",
		Long: `Classic SPP pairing. Action: run 'bluetoothctl pair <mac>'
locally, then 'connect' to open the SPP channel.`,
		RunE: a.runPair,
	}
	cmd.Flags().String("mac", "", "MAC address of the bulb to claim (classic-BT one from 'discover --classic')")
	cmd.Flags().String("name", "", "Human-readable label recorded in the profile")
	cmd.Flags().String("profile-name", "", "Create a profile with this name after pairing")
	return cmd
}

func (a *App) runPair(cmd *cobra.Command, args []string) error {
	macFlag := stringFlagLocal(cmd, "mac")
	target, terr := a.resolveTarget(macFlag)
	if terr != nil {
		return a.render(cmd, output.Outcome{Err: terr})
	}
	mac, merr := needsTargetMAC(target, "pair")
	if merr != nil {
		return a.render(cmd, output.Outcome{Err: merr})
	}
	name := stringFlagLocal(cmd, "name")
	profileName := stringFlagLocal(cmd, "profile-name")

	payload := intent{Target: target, MAC: mac, Name: name, Profile: profileName}
	if a.boolFlag("dry-run") {
		return a.render(cmd, output.Outcome{
			Human: []string{
				mac + ": would run bluetoothctl pair + connect",
				"validated plan only; nothing was paired",
			},
			Data: dataOf(payload),
		})
	}
	if err := a.confirm(fmt.Sprintf("pair classic-BT device %s", mac)); err != nil {
		return a.render(cmd, output.Outcome{Err: err})
	}

	out, perr := exec.Command("bluetoothctl", "pair", mac).CombinedOutput()
	if perr != nil || !strings.Contains(strings.ToLower(string(out)), "successful") {
		return a.render(cmd, output.Outcome{
			Err:  pairFailErr(mac, out),
			Data: dataOf(payload),
		})
	}
	time.Sleep(300 * time.Millisecond)
	_, _ = exec.Command("bluetoothctl", "connect", mac).CombinedOutput()

	if profileName == "" {
		return a.render(cmd, output.Outcome{
			Human: []string{"paired " + mac + " (no profile created; add one with profile add --mac " + mac + ")"},
			Data:  dataOf(payload),
		})
	}
	cfg, lerr := config.Load()
	if lerr != nil {
		return a.render(cmd, output.Outcome{Err: lerr})
	}
	if _, exists := cfg.Get(profileName); exists {
		return a.render(cmd, output.Outcome{
			Err: clierrors.New(clierrors.CodeConflict, "profile %q already exists; not auto-created", profileName).
				WithHint("pick another name or remove it first"),
		})
	}
	p := config.Profile{Name: profileName, MAC: mac, Note: name, CreatedAt: config.NowISO()}
	cfg.Set(p)
	if cfg.Active == "" {
		cfg.Active = p.Name
	}
	if serr := cfg.Save(); serr != nil {
		return a.render(cmd, output.Outcome{Err: serr})
	}
	return a.render(cmd, output.Outcome{
		Human: []string{"paired " + mac + " → profile " + profileName},
		Data:  dataOf(payload),
	})
}

// newSendRawCmd is the calibration tool: agent-friendly byte-level send.
func (a *App) newSendRawCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "sendraw <hexbytes>",
		Short: "Send a raw frame over SPP (byte-level calibration)",
		RunE: func(cmd *cobra.Command, args []string) error {
			if len(args) != 1 {
				return a.render(cmd, output.Outcome{
					Err: clierrors.New(clierrors.CodeUsage, "sendraw takes exactly one hex string").
						WithHint("e.g. syska-sonic-cli sendraw a208ff000000 --mac A0:B1:C2:D3:E4:F5"),
				})
			}
			frame, ferr := hex.DecodeString(strings.ReplaceAll(strings.ReplaceAll(args[0], " ", ""), ":", ""))
			if ferr != nil || len(frame) == 0 {
				return a.render(cmd, output.Outcome{
					Err: clierrors.New(clierrors.CodeUsage, "invalid hex payload %q", args[0]),
				})
			}
			target, terr := a.resolveTarget("")
			if terr != nil {
				return a.render(cmd, output.Outcome{Err: terr})
			}
			mac, merr := needsTargetMAC(target, "sendraw")
			if merr != nil {
				return a.render(cmd, output.Outcome{Err: merr})
			}
			payload := intent{Target: target, MAC: mac, Frames: []string{hex.EncodeToString(frame)}}
			if a.boolFlag("dry-run") {
				return a.render(cmd, output.Outcome{Human: []string{"would send: " + hex.EncodeToString(frame)}, Data: dataOf(payload)})
			}
			if err := a.confirm("send raw bytes to " + mac); err != nil {
				return a.render(cmd, output.Outcome{Err: err})
			}
			dur, derr := a.durFlagOrDefault(5 * time.Second)
			if derr != nil {
				return a.render(cmd, output.Outcome{Err: derr})
			}
			ctx, cancel := context.WithTimeout(context.Background(), dur)
			defer cancel()
			sent, serr := sendFrames(ctx, mac, [][]byte{frame})
			warnings := []string{}
			for _, s := range sent {
				warnings = append(warnings, "sent "+s)
			}
			out := output.Outcome{Data: dataOf(payload), Warnings: warnings}
			if serr != nil {
				out.Err = clierrors.New(clierrors.CodeDevice, "sendraw failed").WithCause(serr)
			} else {
				out.Human = []string{"sent " + hex.EncodeToString(frame)}
			}
			return a.render(cmd, out)
		},
	}
	return cmd
}
