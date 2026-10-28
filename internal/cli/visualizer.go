package cli

import (
	"bytes"
	"context"
	"fmt"
	"os"
	"os/exec"
	"os/signal"
	"strconv"
	"strings"
	"syscall"
	"time"

	"github.com/spf13/cobra"

	"github.com/notaproton/syska-sonic-cli/internal/clierrors"
	"github.com/notaproton/syska-sonic-cli/internal/config"
	"github.com/notaproton/syska-sonic-cli/internal/output"
	"github.com/notaproton/syska-sonic-cli/internal/protocol"
	"github.com/notaproton/syska-sonic-cli/internal/transport"
	"github.com/notaproton/syska-sonic-cli/internal/visualizer"
)

type visualizeOptions struct {
	Source        string
	Rate          int
	Sensitivity   float64
	Smoothing     float64
	MinBrightness int
	MaxBrightness int
	Duration      time.Duration
	Restore       string
}

type visualizeIntent struct {
	Target         config.Target     `json:"target"`
	MAC            string            `json:"mac,omitempty"`
	Source         string            `json:"source"`
	ResolvedSource string            `json:"resolved_source,omitempty"`
	Rate           int               `json:"rate"`
	Sensitivity    float64           `json:"sensitivity"`
	Smoothing      float64           `json:"smoothing"`
	MinBrightness  int               `json:"min_brightness"`
	MaxBrightness  int               `json:"max_brightness"`
	Duration       string            `json:"duration,omitempty"`
	Restore        string            `json:"restore"`
	Stats          *visualizer.Stats `json:"stats,omitempty"`
	Reconnects     int               `json:"reconnects,omitempty"`
}

func (a *App) newVisualizeCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "visualize",
		Short: "Stream cautious audio-reactive color to the bulb",
		Long: `Capture a PulseAudio/PipeWire audio source and stream smoothed,
rate-limited solid colors to the bulb over one persistent SPP connection.

The default --source auto prefers the target speaker's Bluetooth monitor
when audio is routed to the Syska device, and otherwise uses the default
desktop monitor. Use --duration for a bounded run; without it, Ctrl+C stops.`,
		RunE: a.runVisualize,
	}
	cmd.Flags().String("source", "auto", "Audio source: auto, @DEFAULT_MONITOR@, @DEFAULT_SOURCE@, or a source name")
	cmd.Flags().Int("rate", 10, "Maximum frames per second (1-20)")
	cmd.Flags().Float64("sensitivity", 1, "Audio sensitivity multiplier (0.1-10)")
	cmd.Flags().Float64("smoothing", 0.65, "Visual smoothing 0-0.95; higher is calmer")
	cmd.Flags().Int("min-brightness", 6, "Minimum visual brightness percent (0-100)")
	cmd.Flags().Int("max-brightness", 80, "Maximum visual brightness percent (0-100)")
	cmd.Flags().Duration("duration", 0, "How long to run; 0 runs until Ctrl+C")
	cmd.Flags().String("restore", "white", "On exit: white, off, or none")
	return cmd
}

func visualizeOptionsFromCmd(cmd *cobra.Command) (visualizeOptions, error) {
	opts := visualizeOptions{
		Source:        stringFlagLocal(cmd, "source"),
		Rate:          intFlagLocal(cmd, "rate", 10),
		Sensitivity:   floatFlagLocal(cmd, "sensitivity", 1),
		Smoothing:     floatFlagLocal(cmd, "smoothing", 0.65),
		MinBrightness: intFlagLocal(cmd, "min-brightness", 6),
		MaxBrightness: intFlagLocal(cmd, "max-brightness", 80),
		Duration:      durationFlagLocal(cmd, "duration", 0),
		Restore:       strings.ToLower(strings.TrimSpace(stringFlagLocal(cmd, "restore"))),
	}
	cfg := opts.config()
	if err := cfg.Validate(); err != nil {
		return visualizeOptions{}, clierrors.New(clierrors.CodeUsage, "invalid visualizer option: %v", err).
			WithHint("keep --rate at 10 until the lamp response is known; hard cap is 20 fps")
	}
	if opts.Duration < 0 {
		return visualizeOptions{}, clierrors.New(clierrors.CodeUsage, "--duration must not be negative")
	}
	switch opts.Restore {
	case "white", "off", "none":
	default:
		return visualizeOptions{}, clierrors.New(clierrors.CodeUsage, "unknown restore mode %q", opts.Restore).
			WithValid("white", "off", "none")
	}
	return opts, nil
}

func (o visualizeOptions) config() visualizer.Config {
	return visualizer.Config{
		Rate:          o.Rate,
		Sensitivity:   o.Sensitivity,
		Smoothing:     o.Smoothing,
		MinBrightness: o.MinBrightness,
		MaxBrightness: o.MaxBrightness,
	}
}

func floatFlagLocal(cmd *cobra.Command, name string, fallback float64) float64 {
	if f := cmd.Flags().Lookup(name); f != nil {
		if v, err := strconv.ParseFloat(f.Value.String(), 64); err == nil {
			return v
		}
	}
	return fallback
}

// bluetoothMonitorName returns the PipeWire/Pulse monitor name normally
// created for audio routed to the target's A2DP sink.
func bluetoothMonitorName(mac string) string {
	clean := strings.NewReplacer(":", "_", "-", "_").Replace(strings.ToUpper(strings.TrimSpace(mac)))
	return "bluez_output." + clean + ".1.monitor"
}

func sourceListHas(list, want string) bool {
	for _, line := range strings.Split(list, "\n") {
		fields := strings.Fields(line)
		if len(fields) >= 2 && fields[1] == want {
			return true
		}
	}
	return false
}

func resolveVisualizerSource(requested, mac string) string {
	requested = strings.TrimSpace(requested)
	if requested != "" && !strings.EqualFold(requested, "auto") {
		return requested
	}
	candidate := bluetoothMonitorName(mac)
	out, err := exec.Command("pactl", "list", "short", "sources").Output()
	if err == nil && sourceListHas(string(out), candidate) {
		return candidate
	}
	return "@DEFAULT_MONITOR@"
}

type cappedBuffer struct {
	buf bytes.Buffer
	max int
}

func (b *cappedBuffer) Write(p []byte) (int, error) {
	if b.max > 0 && b.buf.Len() < b.max {
		remaining := b.max - b.buf.Len()
		if len(p) > remaining {
			_, _ = b.buf.Write(p[:remaining])
		} else {
			_, _ = b.buf.Write(p)
		}
	}
	return len(p), nil
}

func (b *cappedBuffer) String() string { return strings.TrimSpace(b.buf.String()) }

type pcmCapture struct {
	Blocks <-chan []float32
	Err    <-chan error
	stop   func()
	stderr *cappedBuffer
}

// startPCM launches parec and converts its stereo PCM stream to mono
// sample blocks. It intentionally uses parec because it works with both
// PulseAudio and PipeWire's Pulse server.
func startPCM(ctx context.Context, source string) (*pcmCapture, error) {
	if _, err := exec.LookPath("parec"); err != nil {
		return nil, clierrors.New(clierrors.CodeDevice, "parec not found in PATH").
			WithCause(err).
			WithHint("install pulseaudio-utils (parec works with PipeWire's Pulse server)")
	}

	runCtx, cancel := context.WithCancel(ctx)
	args := []string{
		"--record", "--raw",
		"--format=s16le",
		"--rate=48000",
		"--channels=2",
		"--latency-msec=50",
		"--client-name=syska-sonic-cli-visualizer",
		"--device=" + source,
	}
	cmd := exec.CommandContext(runCtx, "parec", args...)
	stderr := &cappedBuffer{max: 4096}
	cmd.Stderr = stderr
	stdout, err := cmd.StdoutPipe()
	if err != nil {
		cancel()
		return nil, clierrors.New(clierrors.CodeDevice, "open parec stdout failed").WithCause(err)
	}
	if err := cmd.Start(); err != nil {
		cancel()
		return nil, clierrors.New(clierrors.CodeDevice, "start parec for source %q failed", source).
			WithCause(err).
			WithHint("check the source name with: pactl list short sources")
	}

	blocks := make(chan []float32, 8)
	errCh := make(chan error, 1)
	go func() {
		defer close(blocks)
		defer close(errCh)
		tmp := make([]byte, 8192)
		var pending []byte
		for {
			n, readErr := stdout.Read(tmp)
			if n > 0 {
				pending = append(pending, tmp[:n]...)
				complete := len(pending) / 4 * 4 // one stereo s16 frame = 4 bytes
				if complete > 0 {
					samples, derr := visualizer.DecodePCM16LE(pending[:complete], 2)
					if derr != nil {
						if runCtx.Err() == nil {
							errCh <- derr
						}
						cancel()
						_ = cmd.Wait()
						return
					}
					// Keep only a bounded remainder for the next read.
					rest := append([]byte(nil), pending[complete:]...)
					pending = rest
					select {
					case blocks <- samples:
					case <-runCtx.Done():
						_ = cmd.Wait()
						return
					}
				}
			}
			if readErr != nil {
				waitErr := cmd.Wait()
				if runCtx.Err() == nil {
					if waitErr != nil {
						msg := stderr.String()
						if msg != "" {
							errCh <- fmt.Errorf("parec exited: %w: %s", waitErr, msg)
						} else {
							errCh <- fmt.Errorf("parec exited: %w", waitErr)
						}
					} else if readErr.Error() != "EOF" {
						errCh <- readErr
					}
				}
				return
			}
		}
	}()

	capture := &pcmCapture{Blocks: blocks, Err: errCh, stop: cancel, stderr: stderr}
	return capture, nil
}

// visualizerSink owns one SPP connection and reconnects once per failed
// write. The lamp receives only solid-color frames.
type visualizerSink struct {
	mac        string
	conn       *transport.SPPConn
	reconnects int
}

func (s *visualizerSink) connect() error {
	ctx, cancel := context.WithTimeout(context.Background(), 25*time.Second)
	defer cancel()
	conn, err := dialSPPWithLadder(ctx, s.mac)
	if err != nil {
		return err
	}
	s.conn = conn
	return nil
}

func (s *visualizerSink) Close() {
	if s.conn != nil {
		_ = s.conn.Close()
		s.conn = nil
	}
}

func (s *visualizerSink) write(frame []byte) error {
	if s.conn == nil {
		if err := s.connect(); err != nil {
			return err
		}
	}
	if _, err := s.conn.Write(frame); err == nil {
		return nil
	}
	s.Close()
	s.reconnects++
	if err := s.connect(); err != nil {
		return err
	}
	_, err := s.conn.Write(frame)
	return err
}

func (s *visualizerSink) Send(rgb visualizer.RGB) error {
	return s.write(protocol.SolidColor(byte(rgb.R), byte(rgb.G), byte(rgb.B)))
}

func (s *visualizerSink) restore(mode string) error {
	switch mode {
	case "none":
		return nil
	case "off":
		if err := s.write(protocol.Brightness(0, 0, 0)); err != nil {
			return err
		}
		return s.write(protocol.ModeSwitch(1))
	default: // white
		if err := s.write(protocol.ModeSwitch(0)); err != nil {
			return err
		}
		return s.write(protocol.SolidColor(0xFF, 0xFF, 0xFF))
	}
}

func (a *App) runVisualize(cmd *cobra.Command, args []string) error {
	if len(args) != 0 {
		return a.render(cmd, output.Outcome{
			Err: clierrors.New(clierrors.CodeUsage, "visualize takes no arguments").
				WithHint("choose audio with --source; stop with Ctrl+C or --duration"),
		})
	}
	opts, oerr := visualizeOptionsFromCmd(cmd)
	if oerr != nil {
		return a.render(cmd, output.Outcome{Err: oerr})
	}
	target, terr := a.resolveTarget("")
	if terr != nil {
		return a.render(cmd, output.Outcome{Err: terr})
	}
	mac, merr := needsTargetMAC(target, "visualize")
	if merr != nil {
		return a.render(cmd, output.Outcome{Err: merr})
	}

	resolvedSource := resolveVisualizerSource(opts.Source, mac)
	payload := visualizeIntent{
		Target:         target,
		MAC:            mac,
		Source:         opts.Source,
		ResolvedSource: resolvedSource,
		Rate:           opts.Rate,
		Sensitivity:    opts.Sensitivity,
		Smoothing:      opts.Smoothing,
		MinBrightness:  opts.MinBrightness,
		MaxBrightness:  opts.MaxBrightness,
		Restore:        opts.Restore,
	}
	if opts.Duration > 0 {
		payload.Duration = opts.Duration.String()
	}

	if a.boolFlag("dry-run") {
		human := fmt.Sprintf("would stream %s to %s at %d fps (sensitivity %.2f, smoothing %.2f, brightness %d-%d%%, restore %s)",
			resolvedSource, mac, opts.Rate, opts.Sensitivity, opts.Smoothing, opts.MinBrightness, opts.MaxBrightness, opts.Restore)
		return a.render(cmd, output.Outcome{Human: []string{human}, Data: dataOf(payload)})
	}
	if err := a.confirm("stream audio-reactive frames to " + mac); err != nil {
		return a.render(cmd, output.Outcome{Err: err})
	}

	runCtx, stopSignals := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stopSignals()
	if opts.Duration > 0 {
		var cancel context.CancelFunc
		runCtx, cancel = context.WithTimeout(runCtx, opts.Duration)
		defer cancel()
	}

	sink := &visualizerSink{mac: mac}
	defer sink.Close()
	if err := sink.connect(); err != nil {
		return a.render(cmd, output.Outcome{Data: dataOf(payload), Err: clierrors.New(clierrors.CodeDevice, "visualizer could not connect to %s", mac).WithCause(err)})
	}
	if err := sink.write(protocol.ModeSwitch(0)); err != nil {
		return a.render(cmd, output.Outcome{Data: dataOf(payload), Err: clierrors.New(clierrors.CodeDevice, "visualizer could not wake %s", mac).WithCause(err)})
	}

	capture, cerr := startPCM(runCtx, resolvedSource)
	if cerr != nil {
		return a.render(cmd, output.Outcome{Data: dataOf(payload), Err: cerr})
	}
	defer capture.stop()

	fmt.Fprintf(os.Stderr, "visualizing %s at %d fps; Ctrl+C to stop\n", resolvedSource, opts.Rate)
	stats, runErr := visualizer.Run(runCtx, opts.config(), capture.Blocks, sink)
	capture.stop()

	var captureErr error
	select {
	case captureErr = <-capture.Err:
	default:
	}
	payload.Stats = &stats
	payload.Reconnects = sink.reconnects

	warnings := []string{}
	if rerr := sink.restore(opts.Restore); rerr != nil {
		warnings = append(warnings, "restore "+opts.Restore+" failed: "+rerr.Error())
	}
	payload.Reconnects = sink.reconnects

	if runErr != nil {
		if captureErr != nil {
			runErr = fmt.Errorf("%v (capture: %v)", runErr, captureErr)
		}
		return a.render(cmd, output.Outcome{
			Data:     dataOf(payload),
			Warnings: warnings,
			Err:      clierrors.New(clierrors.CodeDevice, "visualizer stopped").WithCause(runErr),
		})
	}
	if captureErr != nil {
		warnings = append(warnings, "audio capture ended: "+captureErr.Error())
	}

	duration := time.Duration(stats.DurationMS) * time.Millisecond
	human := fmt.Sprintf("visualized %s for %s; sent %d frames (%d analyzed, %d skipped)",
		resolvedSource, duration.Round(time.Millisecond), stats.FramesSent, stats.FramesAnalyzed, stats.FramesSkipped)
	return a.render(cmd, output.Outcome{
		Human:    []string{human},
		Data:     dataOf(payload),
		Warnings: warnings,
	})
}
