package visualizer

import (
	"context"
	"math"
	"sync"
	"testing"
	"time"
)

func sine(freq, amplitude float64) []float32 {
	out := make([]float32, FFTSize)
	for i := range out {
		out[i] = float32(amplitude * math.Sin(2*math.Pi*freq*float64(i)/DefaultSampleRate))
	}
	return out
}

func TestDecodePCM16LEStereoDownmix(t *testing.T) {
	// Stereo frames: (32767, -32768), (16384, 16384), plus a trailing
	// partial byte that must be ignored.
	data := []byte{
		0xff, 0x7f, 0x00, 0x80,
		0x00, 0x40, 0x00, 0x40,
		0x12,
	}
	got, err := DecodePCM16LE(data, 2)
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 2 {
		t.Fatalf("want 2 mono frames, got %d (%v)", len(got), got)
	}
	if math.Abs(float64(got[0])) > 0.001 {
		t.Errorf("first frame should cancel to ~0, got %v", got[0])
	}
	if math.Abs(float64(got[1])-0.5) > 0.001 {
		t.Errorf("second frame should be ~0.5, got %v", got[1])
	}
	if _, err := DecodePCM16LE(data, 0); err == nil {
		t.Error("channels=0 should error")
	}
}

func TestAnalyzerDetectsDominantBands(t *testing.T) {
	low := NewAnalyzer(DefaultSampleRate).Process(sine(117.1875, 0.8))
	if low.Bass < 0.8 || low.Bass <= low.Mid || low.Bass <= low.High {
		t.Errorf("117 Hz should dominate bass, got %+v", low)
	}

	high := NewAnalyzer(DefaultSampleRate).Process(sine(4687.5, 0.8))
	if high.High < 0.8 || high.High <= high.Bass || high.High <= high.Mid {
		t.Errorf("4687 Hz should dominate high, got %+v", high)
	}

	silence := NewAnalyzer(DefaultSampleRate).Process(make([]float32, FFTSize))
	if silence.Bass != 0 || silence.Mid != 0 || silence.High != 0 || silence.RMS != 0 {
		t.Errorf("silence should produce zero bands, got %+v", silence)
	}
}

func TestMapperRespectsBrightnessCapAndQuantizes(t *testing.T) {
	cfg := Config{
		Rate:          10,
		Sensitivity:   2,
		Smoothing:     0.2,
		MinBrightness: 10,
		MaxBrightness: 50,
	}
	mapper := NewMapper(cfg)
	var rgb RGB
	for i := 0; i < 12; i++ {
		rgb = mapper.Update(Bands{RMS: 0.8, Bass: 1, Mid: 1, High: 1, Centroid: 300})
	}
	maxAllowed := int(math.Ceil(0.50*255)) + 3
	for _, channel := range []int{rgb.R, rgb.G, rgb.B} {
		if channel < 0 || channel > maxAllowed {
			t.Fatalf("channel %d exceeds max brightness allowance %d in %+v", channel, maxAllowed, rgb)
		}
		if channel%4 != 0 && channel != 255 {
			t.Fatalf("channel %d is not quantized to a step of 4 in %+v", channel, rgb)
		}
	}
}

func TestMapperSilenceStaysAtFloor(t *testing.T) {
	mapper := NewMapper(Config{
		Rate:          10,
		Sensitivity:   1,
		Smoothing:     0.65,
		MinBrightness: 6,
		MaxBrightness: 80,
	})
	rgb := mapper.Update(Bands{})
	if rgb.R == 0 && rgb.G == 0 && rgb.B == 0 {
		t.Fatalf("silence should render the dim floor color, got black")
	}
	// 6% of 255 is ~15; allow the strongest channel a small margin.
	if rgb.R > 20 && rgb.G > 20 && rgb.B > 20 {
		t.Fatalf("silence floor is unexpectedly bright: %+v", rgb)
	}
}

func TestRunSendsAtMostOneFrameForUnchangingSilence(t *testing.T) {
	blocks := make(chan []float32, 8)
	for i := 0; i < 8; i++ {
		blocks <- make([]float32, FFTSize)
	}

	var mu sync.Mutex
	sent := 0
	sink := SinkFunc(func(RGB) error {
		mu.Lock()
		defer mu.Unlock()
		sent++
		return nil
	})

	ctx, cancel := context.WithTimeout(context.Background(), 140*time.Millisecond)
	defer cancel()
	stats, err := Run(ctx, Config{Rate: 20, Sensitivity: 1, Smoothing: 0.65, MinBrightness: 6, MaxBrightness: 80}, blocks, sink)
	if err != nil {
		t.Fatal(err)
	}
	mu.Lock()
	defer mu.Unlock()
	if sent != 1 || stats.FramesSent != 1 {
		t.Fatalf("unchanging silence should be deduplicated to 1 frame, sent=%d stats=%+v", sent, stats)
	}
	if stats.SampleBlocks == 0 || stats.FramesAnalyzed == 0 {
		t.Fatalf("streamer did not process audio: %+v", stats)
	}
}

func TestConfigValidation(t *testing.T) {
	if err := (Config{Rate: MaxRate + 1}).Validate(); err == nil {
		t.Error("rate above the hard cap should error")
	}
	if err := (Config{Rate: 10, Sensitivity: 0.01}).Validate(); err == nil {
		t.Error("sensitivity below range should error")
	}
	if err := (Config{Rate: 10, MinBrightness: 50, MaxBrightness: 40}).Validate(); err == nil {
		t.Error("max below min should error")
	}
	if err := DefaultConfig().Validate(); err != nil {
		t.Errorf("default config should validate: %v", err)
	}
}
