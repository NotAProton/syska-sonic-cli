package config

import (
	"testing"
)

func useTempConfig(t *testing.T) {
	t.Helper()
	dir := t.TempDir()
	t.Setenv("XDG_CONFIG_HOME", dir)
	t.Setenv("XDG_STATE_HOME", dir)
}

func TestLoadEmptyWithoutFile(t *testing.T) {
	useTempConfig(t)
	cfg, err := Load()
	if err != nil {
		t.Fatalf("load: %v", err)
	}
	if cfg.Active != "" || len(cfg.Profiles) != 0 {
		t.Fatalf("expected empty config, got %+v", cfg)
	}
}

func TestProfileRoundTripAndOrdering(t *testing.T) {
	useTempConfig(t)
	cfg := Config{}
	cfg.Set(Profile{Name: "zeta"})
	cfg.Set(Profile{Name: "alpha", MAC: "aa:bb:cc:dd:ee:ff"})
	// ValidateMAC normalization
	mac, err := ValidateMAC("aa:bb:cc:dd:ee:ff")
	if err != nil {
		t.Fatalf("mac: %v", err)
	}
	if mac != "AA:BB:CC:DD:EE:FF" {
		t.Fatalf("normalization failed: %s", mac)
	}
	cfg.Set(Profile{Name: "alpha", MAC: mac})
	if err := cfg.Save(); err != nil {
		t.Fatalf("save: %v", err)
	}
	reloaded, err := Load()
	if err != nil {
		t.Fatalf("reload: %v", err)
	}
	if reloaded.Names()[0] != "alpha" || reloaded.Names()[1] != "zeta" {
		t.Fatalf("ordering wrong: %v", reloaded.Names())
	}
	p, ok := reloaded.Get("alpha")
	if !ok || p.MAC != "AA:BB:CC:DD:EE:FF" {
		t.Fatalf("roundtrip mismatch: %+v ok=%v", p, ok)
	}
}

func TestTargetPrecedenceFlagOverEnv(t *testing.T) {
	useTempConfig(t)
	cfg := Config{}
	cfg.Set(Profile{Name: "desk", MAC: "AA:BB:CC:DD:EE:01"})
	cfg.Active = "desk"
	if err := cfg.Save(); err != nil {
		t.Fatalf("save: %v", err)
	}
	t.Setenv("SSCLI_PROFILE", "desk")
	t.Setenv("SSCLI_MAC", "AA:BB:CC:DD:EE:02")

	// Flag wins over env.
	tgt, err := ResolveTarget("desk", "aa:bb:cc:dd:ee:03")
	if err != nil {
		t.Fatalf("resolve: %v", err)
	}
	if tgt.ProfileSource != "flag" || tgt.MACSource != "flag" {
		t.Fatalf("flag should win: %+v", tgt)
	}
	if tgt.MAC != "AA:BB:CC:DD:EE:03" {
		t.Fatalf("mac: %s", tgt.MAC)
	}

	// Env wins over default profile.
	cfg2, err := Load()
	if err != nil {
		t.Fatalf("load: %v", err)
	}
	_ = cfg2
	tgt2, err := ResolveTarget("", "")
	if err != nil {
		t.Fatalf("resolve2: %v", err)
	}
	if tgt2.ProfileSource != "env" {
		t.Fatalf("env should beat default: %+v", tgt2)
	}
	if tgt2.MACSource != "env" || tgt2.MAC != "AA:BB:CC:DD:EE:02" {
		t.Fatalf("env mac should win: %+v, %v", tgt2, tgt2.MAC)
	}
}

func TestTargetDefaultProfile(t *testing.T) {
	useTempConfig(t)
	cfg := Config{}
	cfg.Set(Profile{Name: "only"})
	cfg.Active = "only"
	if err := cfg.Save(); err != nil {
		t.Fatalf("save: %v", err)
	}
	tgt, err := ResolveTarget("", "")
	if err != nil {
		t.Fatalf("resolve: %v", err)
	}
	if tgt.Profile != "only" || tgt.ProfileSource != "default" || tgt.MACSource != "" {
		t.Fatalf("unexpected target: %+v", tgt)
	}
}

func TestUnknownProfileRejected(t *testing.T) {
	useTempConfig(t)
	cfg := Config{}
	cfg.Set(Profile{Name: "real"})
	if err := cfg.Save(); err != nil {
		t.Fatalf("save: %v", err)
	}
	_, err := ResolveTarget("nope", "")
	if err == nil {
		t.Fatal("expected unknown-profile error for flag")
	}
	t.Setenv("SSCLI_PROFILE", "ghost")
	_, err = ResolveTarget("", "")
	if err == nil {
		t.Fatal("expected unknown-profile error for env")
	}
}

func TestDeleteProfile(t *testing.T) {
	cfg := Config{}
	cfg.Set(Profile{Name: "a"})
	cfg.Set(Profile{Name: "b"})
	cfg.Active = "a"
	existed, wasActive := cfg.Delete("a")
	if !existed || !wasActive {
		t.Fatalf("expected delete of active: %v %v", existed, wasActive)
	}
	if cfg.Active != "" {
		t.Fatalf("active not reset: %q", cfg.Active)
	}
	if len(cfg.Profiles) != 1 || cfg.Profiles[0].Name != "b" {
		t.Fatalf("wrong remainder: %+v", cfg.Profiles)
	}
}

func TestValidateMACAndName(t *testing.T) {
	if _, err := ValidateMAC("AA-BB-CC-DD-EE-FF"); err == nil {
		t.Fatal("dash mac accepted")
	}
	if _, err := ValidateMAC("AA:BB:CC:DD:EE::FX"); err == nil {
		t.Fatal("junk mac accepted")
	}
	if err := ValidateName("OK_NAME"); err == nil {
		t.Fatal("uppercase name accepted")
	}
	if err := ValidateName("desk"); err != nil {
		t.Fatalf("simple name rejected: %v", err)
	}
}
