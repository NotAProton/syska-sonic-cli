// Package visualizer turns short PCM windows into safe, low-rate RGB
// updates for a single Syska Sonic lamp.
//
// The lamp is a single, relatively slow output device, so the package is
// deliberately conservative: windows are reduced to three frequency bands,
// changes are smoothed, RGB values are quantized, and the caller chooses a
// frame rate no higher than 20 fps.
package visualizer

import (
	"context"
	"encoding/binary"
	"errors"
	"fmt"
	"math"
	"time"
)

const (
	// DefaultSampleRate is the capture rate used by the CLI.
	DefaultSampleRate = 48000
	// FFTSize is the fixed analysis window. At 48 kHz it spans about 43 ms.
	FFTSize = 2048
	// MaxRate caps the lamp update rate even if a caller asks for more.
	MaxRate = 20
)

// Config controls analysis-to-light mapping.
type Config struct {
	Rate           int           // frames per second, 1..MaxRate
	Sensitivity    float64       // input multiplier, 0.1..10
	Smoothing      float64       // 0..0.95; higher is calmer
	MinBrightness  int           // percent, 0..100
	MaxBrightness  int           // percent, MinBrightness..100
	StatusInterval time.Duration // optional status callback interval
	OnStatus       func(Stats)   // optional; called from Run
}

// DefaultConfig returns the cautious defaults used by the CLI.
func DefaultConfig() Config {
	return Config{
		Rate:          10,
		Sensitivity:   1,
		Smoothing:     0.65,
		MinBrightness: 6,
		MaxBrightness: 80,
	}
}

func (c Config) withDefaults() Config {
	d := DefaultConfig()
	if c.Rate == 0 {
		c.Rate = d.Rate
	}
	if c.Sensitivity == 0 {
		c.Sensitivity = d.Sensitivity
	}
	if c.MinBrightness == 0 && c.MaxBrightness == 0 {
		c.MinBrightness = d.MinBrightness
		c.MaxBrightness = d.MaxBrightness
	}
	return c
}

// Validate reports whether the configuration is safe and coherent.
func (c Config) Validate() error {
	c = c.withDefaults()
	if c.Rate < 1 || c.Rate > MaxRate {
		return fmt.Errorf("rate %d is outside 1-%d fps", c.Rate, MaxRate)
	}
	if math.IsNaN(c.Sensitivity) || math.IsInf(c.Sensitivity, 0) || c.Sensitivity < 0.1 || c.Sensitivity > 10 {
		return fmt.Errorf("sensitivity %g is outside 0.1-10", c.Sensitivity)
	}
	if math.IsNaN(c.Smoothing) || math.IsInf(c.Smoothing, 0) || c.Smoothing < 0 || c.Smoothing > 0.95 {
		return fmt.Errorf("smoothing %g is outside 0-0.95", c.Smoothing)
	}
	if c.MinBrightness < 0 || c.MinBrightness > 100 {
		return fmt.Errorf("min brightness %d is outside 0-100", c.MinBrightness)
	}
	if c.MaxBrightness < c.MinBrightness || c.MaxBrightness > 100 {
		return fmt.Errorf("max brightness %d must be between min brightness and 100", c.MaxBrightness)
	}
	return nil
}

// RGB is one quantized lamp color.
type RGB struct {
	R int `json:"r"`
	G int `json:"g"`
	B int `json:"b"`
}

// DecodePCM16LE converts interleaved little-endian PCM16 into mono samples
// in the range [-1, 1]. A trailing partial sample frame is ignored so a
// stream reader can pass arbitrary read boundaries safely.
func DecodePCM16LE(data []byte, channels int) ([]float32, error) {
	if channels <= 0 {
		return nil, errors.New("channels must be positive")
	}
	frameBytes := 2 * channels
	frames := len(data) / frameBytes
	out := make([]float32, 0, frames)
	for frame := 0; frame < frames; frame++ {
		var sum float64
		base := frame * frameBytes
		for ch := 0; ch < channels; ch++ {
			v := int16(binary.LittleEndian.Uint16(data[base+ch*2:]))
			sum += float64(v) / 32768.0
		}
		out = append(out, float32(sum/float64(channels)))
	}
	return out, nil
}

// Bands is the reduced frequency view used by the mapper.
type Bands struct {
	RMS      float64 `json:"rms"`
	Bass     float64 `json:"bass"`
	Mid      float64 `json:"mid"`
	High     float64 `json:"high"`
	Centroid float64 `json:"centroid_hz"`
}

// Analyzer computes band levels with a fixed-size FFT. Band outputs are
// adaptively normalized to roughly 0..1 so quiet and loud program material
// both remain usable without manual gain staging.
type Analyzer struct {
	sampleRate int
	bassPeak   float64
	midPeak    float64
	highPeak   float64
}

// NewAnalyzer creates an analyzer for a PCM sample rate.
func NewAnalyzer(sampleRate int) *Analyzer {
	if sampleRate <= 0 {
		sampleRate = DefaultSampleRate
	}
	return &Analyzer{sampleRate: sampleRate, bassPeak: 0.05, midPeak: 0.05, highPeak: 0.05}
}

// Process analyzes up to FFTSize samples. Shorter windows are zero-padded;
// longer windows are truncated to the most recent FFTSize samples.
func (a *Analyzer) Process(samples []float32) Bands {
	if len(samples) == 0 {
		return Bands{}
	}
	if len(samples) > FFTSize {
		samples = samples[len(samples)-FFTSize:]
	}

	re := make([]float64, FFTSize)
	im := make([]float64, FFTSize)
	var squareSum float64
	for i := 0; i < FFTSize; i++ {
		var v float64
		if i < len(samples) {
			v = float64(samples[i])
		}
		squareSum += v * v
		// Hann window.
		w := 0.5 - 0.5*math.Cos(2*math.Pi*float64(i)/float64(FFTSize-1))
		re[i] = v * w
	}
	rms := math.Sqrt(squareSum / float64(FFTSize))
	if rms < 0.0008 {
		// Keep peaks slowly relaxing during silence so the next onset is
		// not normalized against a stale, much louder passage forever.
		a.bassPeak = decayPeak(a.bassPeak, 0)
		a.midPeak = decayPeak(a.midPeak, 0)
		a.highPeak = decayPeak(a.highPeak, 0)
		return Bands{RMS: rms}
	}

	fft(re, im)
	binHz := float64(a.sampleRate) / FFTSize
	var bassPower, midPower, highPower float64
	var bassCount, midCount, highCount int
	var centroidWeight, centroidSum float64
	scale := 4.0 / FFTSize
	for bin := 1; bin < FFTSize/2; bin++ {
		freq := float64(bin) * binHz
		if freq < 35 || freq > 14000 {
			continue
		}
		mag := math.Hypot(re[bin], im[bin]) * scale
		power := mag * mag
		switch {
		case freq < 250:
			bassPower += power
			bassCount++
		case freq < 3000:
			midPower += power
			midCount++
		default:
			highPower += power
			highCount++
		}
		if freq >= 40 && freq <= 12000 {
			centroidWeight += power
			centroidSum += freq * power
		}
	}

	bassRaw := bandRMS(bassPower, bassCount)
	midRaw := bandRMS(midPower, midCount)
	highRaw := bandRMS(highPower, highCount)
	a.bassPeak = decayPeak(a.bassPeak, bassRaw)
	a.midPeak = decayPeak(a.midPeak, midRaw)
	a.highPeak = decayPeak(a.highPeak, highRaw)

	centroid := 0.0
	if centroidWeight > 0 {
		centroid = centroidSum / centroidWeight
	}
	return Bands{
		RMS:      rms,
		Bass:     clamp01(bassRaw / a.bassPeak),
		Mid:      clamp01(midRaw / a.midPeak),
		High:     clamp01(highRaw / a.highPeak),
		Centroid: centroid,
	}
}

func bandRMS(power float64, count int) float64 {
	if count == 0 {
		return 0
	}
	return math.Sqrt(power / float64(count))
}

func decayPeak(peak, raw float64) float64 {
	if raw > peak {
		return raw
	}
	// Roughly a four-second release at 48 kHz / 2048-sample windows.
	peak *= 0.995
	if peak < 0.03 {
		return 0.03
	}
	return peak
}

// fft is an iterative radix-2 FFT. len(re) must be a power of two.
func fft(re, im []float64) {
	n := len(re)
	for i, j := 1, 0; i < n; i++ {
		bit := n >> 1
		for ; j&bit != 0; bit >>= 1 {
			j &^= bit
		}
		j |= bit
		if i < j {
			re[i], re[j] = re[j], re[i]
			im[i], im[j] = im[j], im[i]
		}
	}
	for size := 2; size <= n; size <<= 1 {
		half := size / 2
		angle := -2 * math.Pi / float64(size)
		wRe, wIm := math.Cos(angle), math.Sin(angle)
		for base := 0; base < n; base += size {
			curRe, curIm := 1.0, 0.0
			for k := 0; k < half; k++ {
				i0 := base + k
				i1 := i0 + half
				tRe := re[i1]*curRe - im[i1]*curIm
				tIm := re[i1]*curIm + im[i1]*curRe
				re[i1] = re[i0] - tRe
				im[i1] = im[i0] - tIm
				re[i0] += tRe
				im[i0] += tIm
				curRe, curIm = curRe*wRe-curIm*wIm, curRe*wIm+curIm*wRe
			}
		}
	}
}

// Mapper converts band levels to RGB with attack/release smoothing, a
// slowly moving hue, and a restrained beat accent.
type Mapper struct {
	cfg    Config
	bass   float64
	mid    float64
	high   float64
	level  float64
	hue    float64
	beat   float64
	last   RGB
	seeded bool
}

// NewMapper creates a mapper. Invalid values are replaced by defaults;
// callers that accept user input should call Config.Validate first.
func NewMapper(cfg Config) *Mapper {
	cfg = cfg.withDefaults()
	if cfg.Validate() != nil {
		cfg = DefaultConfig()
	}
	return &Mapper{cfg: cfg, hue: 265}
}

// Update consumes one band snapshot and returns the next quantized color.
func (m *Mapper) Update(b Bands) RGB {
	bass := clamp01(b.Bass * m.cfg.Sensitivity)
	mid := clamp01(b.Mid * m.cfg.Sensitivity)
	high := clamp01(b.High * m.cfg.Sensitivity)

	previousBass := m.bass
	m.bass = smooth(m.bass, bass, m.cfg.Smoothing)
	m.mid = smooth(m.mid, mid, m.cfg.Smoothing)
	m.high = smooth(m.high, high, m.cfg.Smoothing)

	loudness := clamp01(0.60*m.bass + 0.30*m.mid + 0.10*m.high)
	m.level = smooth(m.level, loudness, m.cfg.Smoothing)

	if b.Centroid >= 50 && (bass+mid+high) > 0.03 {
		ratio := math.Log(clamp(b.Centroid, 50, 10000)/50) / math.Log(10000.0/50)
		targetHue := 265 - 265*clamp01(ratio)
		m.hue = smoothHue(m.hue, targetHue, 0.08+0.25*(1-m.cfg.Smoothing))
	}

	// A bass onset adds a brief lift and a small hue nudge rather than a
	// full strobe. The envelope decays at the render rate.
	if bass > previousBass+0.18 && bass > 0.45 {
		m.beat = 1
		m.hue = math.Mod(m.hue+18, 360)
	}
	m.beat *= 0.90

	span := float64(m.cfg.MaxBrightness - m.cfg.MinBrightness)
	brightness := float64(m.cfg.MinBrightness) + span*math.Pow(m.level, 0.75) + span*0.15*m.beat
	brightness = clamp(brightness, 0, float64(m.cfg.MaxBrightness))
	rgb := hsvToRGB(m.hue, 0.88, brightness/100)
	rgb = RGB{
		R: quantize(rgb.R),
		G: quantize(rgb.G),
		B: quantize(rgb.B),
	}
	if !m.seeded {
		m.seeded = true
		m.last = rgb
		return rgb
	}
	m.last = rgb
	return rgb
}

func smooth(current, target, smoothing float64) float64 {
	var alpha float64
	if target > current {
		alpha = 1 - smoothing*0.65 // faster attack
	} else {
		alpha = 1 - smoothing*0.92 // slower release
	}
	return current + (target-current)*alpha
}

func smoothHue(current, target, alpha float64) float64 {
	delta := math.Mod(target-current+540, 360) - 180
	return math.Mod(current+delta*alpha+360, 360)
}

func quantize(v int) int {
	if v <= 0 {
		return 0
	}
	if v >= 255 {
		return 255
	}
	return ((v + 2) / 4) * 4
}

func hsvToRGB(h, s, v float64) RGB {
	h = math.Mod(h+360, 360)
	c := v * s
	x := c * (1 - math.Abs(math.Mod(h/60, 2)-1))
	m := v - c
	var r, g, b float64
	switch {
	case h < 60:
		r, g, b = c, x, 0
	case h < 120:
		r, g, b = x, c, 0
	case h < 180:
		r, g, b = 0, c, x
	case h < 240:
		r, g, b = 0, x, c
	case h < 300:
		r, g, b = x, 0, c
	default:
		r, g, b = c, 0, x
	}
	return RGB{
		R: int(math.Round((r + m) * 255)),
		G: int(math.Round((g + m) * 255)),
		B: int(math.Round((b + m) * 255)),
	}
}

func clamp01(v float64) float64 { return clamp(v, 0, 1) }

func clamp(v, lo, hi float64) float64 {
	if math.IsNaN(v) {
		return lo
	}
	if v < lo {
		return lo
	}
	if v > hi {
		return hi
	}
	return v
}

// Sink receives quantized RGB updates.
type Sink interface {
	Send(RGB) error
}

// SinkFunc adapts a function to Sink.
type SinkFunc func(RGB) error

func (f SinkFunc) Send(rgb RGB) error { return f(rgb) }

// Stats reports what the streamer processed and sent.
type Stats struct {
	SampleBlocks   int   `json:"sample_blocks"`
	FramesAnalyzed int   `json:"frames_analyzed"`
	FramesSent     int   `json:"frames_sent"`
	FramesSkipped  int   `json:"frames_skipped"`
	DurationMS     int64 `json:"duration_ms"`
	Last           RGB   `json:"last"`
}

// Run consumes PCM sample blocks until ctx ends or the block channel
// closes. It renders at cfg.Rate, coalescing all audio analysis that
// arrives between renders and skipping RGB values that did not change.
func Run(ctx context.Context, cfg Config, blocks <-chan []float32, sink Sink) (Stats, error) {
	cfg = cfg.withDefaults()
	if err := cfg.Validate(); err != nil {
		return Stats{}, err
	}
	if sink == nil {
		return Stats{}, errors.New("visualizer sink is nil")
	}

	started := time.Now()
	stats := Stats{}
	analyzer := NewAnalyzer(DefaultSampleRate)
	mapper := NewMapper(cfg)
	var latest Bands
	haveBands := false
	var lastSent RGB
	haveSent := false

	period := time.Second / time.Duration(cfg.Rate)
	if period <= 0 {
		period = time.Second / time.Duration(DefaultConfig().Rate)
	}
	ticker := time.NewTicker(period)
	defer ticker.Stop()

	var statusTicker *time.Ticker
	var statusC <-chan time.Time
	if cfg.OnStatus != nil && cfg.StatusInterval > 0 {
		statusTicker = time.NewTicker(cfg.StatusInterval)
		statusC = statusTicker.C
		defer statusTicker.Stop()
	}

	for {
		select {
		case <-ctx.Done():
			stats.DurationMS = time.Since(started).Milliseconds()
			return stats, nil
		case block, ok := <-blocks:
			if !ok {
				stats.DurationMS = time.Since(started).Milliseconds()
				return stats, errors.New("audio capture ended")
			}
			stats.SampleBlocks++
			latest = analyzer.Process(block)
			haveBands = true
		case <-ticker.C:
			// Drain every queued analysis window so rendering always uses
			// the freshest spectrum without constructing a backlog.
		drain:
			for {
				select {
				case block, ok := <-blocks:
					if !ok {
						stats.DurationMS = time.Since(started).Milliseconds()
						return stats, errors.New("audio capture ended")
					}
					stats.SampleBlocks++
					latest = analyzer.Process(block)
					haveBands = true
				default:
					break drain
				}
			}
			if !haveBands {
				continue
			}
			stats.FramesAnalyzed++
			frame := mapper.Update(latest)
			stats.Last = frame
			if haveSent && frame == lastSent {
				stats.FramesSkipped++
				continue
			}
			if err := sink.Send(frame); err != nil {
				stats.DurationMS = time.Since(started).Milliseconds()
				return stats, err
			}
			lastSent = frame
			haveSent = true
			stats.FramesSent++
		case <-statusC:
			stats.DurationMS = time.Since(started).Milliseconds()
			cfg.OnStatus(stats)
		}
	}
}
