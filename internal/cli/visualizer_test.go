package cli

import (
	"testing"
	"time"

	"github.com/notaproton/syska-sonic-cli/internal/visualizer"
)

func TestBluetoothMonitorName(t *testing.T) {
	if got := bluetoothMonitorName("aa:bb:cc:dd:ee:ff"); got != "bluez_output.AA_BB_CC_DD_EE_FF.1.monitor" {
		t.Errorf("unexpected Bluetooth monitor name: %s", got)
	}
}

func TestSourceListHas(t *testing.T) {
	list := "53\talsa_output.example.monitor\tPipeWire\ts32le 2ch 48000Hz\tRUNNING\n" +
		"340\tbluez_output.AA_BB_CC_DD_EE_FF.1.monitor\tPipeWire\ts16le 2ch 48000Hz\tSUSPENDED\n"
	if !sourceListHas(list, "bluez_output.AA_BB_CC_DD_EE_FF.1.monitor") {
		t.Error("expected to find the target monitor")
	}
	if sourceListHas(list, "bluez_output.00_11_22_33_44_55.1.monitor") {
		t.Error("found a monitor that is not in the list")
	}
}

func TestVisualizerOptionsValidation(t *testing.T) {
	base := visualizeOptions{
		Source:        "auto",
		Rate:          10,
		Sensitivity:   1,
		Smoothing:     0.65,
		MinBrightness: 6,
		MaxBrightness: 80,
		Restore:       "white",
	}
	if err := base.config().Validate(); err != nil {
		t.Fatalf("base config should validate: %v", err)
	}

	badRate := base
	badRate.Rate = visualizer.MaxRate + 1
	if err := badRate.config().Validate(); err == nil {
		t.Error("rate above hard cap should not validate")
	}

	badRange := base
	badRange.MinBrightness = 70
	badRange.MaxBrightness = 60
	if err := badRange.config().Validate(); err == nil {
		t.Error("brightness range should not validate")
	}
}

func TestVisualizerOptionsFromCmdRejectsBadValues(t *testing.T) {
	tests := [][]string{
		{"--rate", "21"},
		{"--restore", "purple"},
		{"--duration", "-1s"},
	}
	for _, args := range tests {
		cmd := (&App{}).newVisualizeCmd()
		if err := cmd.Flags().Parse(args); err != nil {
			t.Fatalf("parse %v: %v", args, err)
		}
		if _, err := visualizeOptionsFromCmd(cmd); err == nil {
			t.Errorf("options %v should be rejected", args)
		}
	}

	cmd := (&App{}).newVisualizeCmd()
	if err := cmd.Flags().Parse([]string{"--duration", "10s", "--rate", "8"}); err != nil {
		t.Fatal(err)
	}
	opts, err := visualizeOptionsFromCmd(cmd)
	if err != nil {
		t.Fatal(err)
	}
	if opts.Duration != 10*time.Second || opts.Rate != 8 {
		t.Errorf("unexpected parsed options: %+v", opts)
	}
}
