// Package config stores named bulb profiles and resolves the effective
// target for an invocation using strict precedence:
//
//	flag > env (SSCLI_PROFILE, SSCLI_MAC) > default profile > baked-in defaults
package config

import (
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"time"

	"github.com/notaproton/syska-sonic-cli/internal/clierrors"
)

// Profile is one named bulb target. MAC stays optional until discover/pair
// fills it in, so a profile can be created before the bulb is ever seen.
type Profile struct {
	Name      string `json:"name"`
	MAC       string `json:"mac,omitempty"`
	CreatedAt string `json:"created_at,omitempty"`
	Note      string `json:"note,omitempty"`
}

// Config is the on-disk file: the default profile name plus sorted profiles.
type Config struct {
	Active   string    `json:"active_profile,omitempty"`
	Profiles []Profile `json:"profiles,omitempty"`
}

func (c Config) profile(name string) (Profile, bool) {
	for _, p := range c.Profiles {
		if p.Name == name {
			return p, true
		}
	}
	return Profile{}, false
}

// Get returns one profile by name.
func (c Config) Get(name string) (Profile, bool) { return c.profile(name) }

// Names returns profile names, sorted.
func (c Config) Names() []string {
	out := make([]string, 0, len(c.Profiles))
	for _, p := range c.Profiles {
		out = append(out, p.Name)
	}
	sort.Strings(out)
	return out
}

// Set inserts or updates a profile, keeping the slice name-sorted.
func (c *Config) Set(p Profile) {
	for i := range c.Profiles {
		if c.Profiles[i].Name == p.Name {
			c.Profiles[i] = p
			return
		}
	}
	c.Profiles = append(c.Profiles, p)
	sort.Slice(c.Profiles, func(i, j int) bool { return c.Profiles[i].Name < c.Profiles[j].Name })
}

// Delete removes a profile; if it was the default, the default resets.
func (c *Config) Delete(name string) (existed, wasActive bool) {
	for i, p := range c.Profiles {
		if p.Name == name {
			wasActive = c.Active == name
			if wasActive {
				c.Active = ""
			}
			c.Profiles = append(c.Profiles[:i], c.Profiles[i+1:]...)
			return true, wasActive
		}
	}
	return false, false
}

// Target is the resolved bulb for one invocation. Source fields report where
// each value came from ("flag", "env", "default-profile") so users can audit
// surprising behavior instead of guessing.
type Target struct {
	Profile       string `json:"profile"`
	ProfileSet    bool   `json:"profile_set"`
	ProfileSource string `json:"profile_source,omitempty"`
	MAC           string `json:"mac,omitempty"`
	MACSource     string `json:"mac_source,omitempty"`
}

var (
	nameRE = regexp.MustCompile(`^[a-z][a-z0-9-]{0,31}$`)
	macRE  = regexp.MustCompile(`^[0-9a-f]{2}(:[0-9a-f]{2}){5}$`)
)

// ValidateName enforces short lowercase machine-friendly profile names.
func ValidateName(name string) error {
	if !nameRE.MatchString(name) {
		return clierrors.New(clierrors.CodeUsage, "invalid profile name %q", name).
			WithHint("use 1-32 lowercase letters, digits and '-', starting with a letter; e.g. desk, kitchen-strip-1")
	}
	return nil
}

// ValidateMAC normalizes and validates a MAC address (case-insensitive).
func ValidateMAC(mac string) (string, error) {
	mac = strings.ToLower(strings.TrimSpace(mac))
	if !macRE.MatchString(mac) {
		return "", clierrors.New(clierrors.CodeUsage, "invalid MAC address %q", mac).
			WithHint("expected 12 hex digits, colon separated, e.g. AA:BB:CC:DD:EE:FF")
	}
	return strings.ToUpper(mac), nil
}

// validate does a full-file sanity pass at load time.
func (c *Config) validate() error {
	for _, p := range c.Profiles {
		if err := ValidateName(p.Name); err != nil {
			return err
		}
		if p.MAC != "" {
			if _, err := ValidateMAC(p.MAC); err != nil {
				return err
			}
		}
	}
	if c.Active != "" {
		if _, ok := c.profile(c.Active); !ok {
			return clierrors.New(clierrors.CodeInternal, "%q is active but missing from profiles", c.Active).
				WithValid(c.Names()...).
				WithHint("fix or delete the config file")
		}
	}
	return nil
}

// Dir returns the per-user config directory for this CLI.
func Dir() (string, error) {
	base, err := os.UserConfigDir()
	if err != nil {
		return "", clierrors.New(clierrors.CodeDevice, "cannot determine user config directory: %v", err).
			WithHint("set the XDG_CONFIG_HOME environment variable to a writable directory")
	}
	return filepath.Join(base, "syska-sonic-cli"), nil
}

// Path returns the config file path.
func Path() (string, error) {
	d, err := Dir()
	if err != nil {
		return "", err
	}
	return filepath.Join(d, "config.json"), nil
}

// StateDir returns the per-user state directory (jobs ledger, feedback log),
// honoring XDG_STATE_HOME.
func StateDir() (string, error) {
	if v := strings.TrimSpace(os.Getenv("XDG_STATE_HOME")); v != "" {
		return filepath.Join(v, "syska-sonic-cli"), nil
	}
	home, err := os.UserHomeDir()
	if err != nil {
		return "", clierrors.New(clierrors.CodeDevice, "cannot determine home directory: %v", err)
	}
	return filepath.Join(home, ".local", "state", "syska-sonic-cli"), nil
}

// StatePath returns a path inside the state directory.
func StatePath(name string) (string, error) {
	d, err := StateDir()
	if err != nil {
		return "", err
	}
	return filepath.Join(d, name), nil
}

// Load reads config.json; a missing file yields an empty Config.
func Load() (Config, error) {
	cfg := Config{}
	p, err := Path()
	if err != nil {
		return cfg, err
	}
	b, err := os.ReadFile(p)
	if errors.Is(err, fs.ErrNotExist) {
		return cfg, nil
	}
	if err != nil {
		return cfg, clierrors.New(clierrors.CodeDevice, "read %s", p).WithCause(err)
	}
	if err := json.Unmarshal(b, &cfg); err != nil {
		return cfg, clierrors.New(clierrors.CodeUsage, "config file %s is not valid JSON: %v", p, err).
			WithHint("fix or delete the file to reset all profiles")
	}
	if err := cfg.validate(); err != nil {
		return Config{}, err
	}
	return cfg, nil
}

// Save writes config.json atomically with restrictive permissions.
func (c *Config) Save() error {
	p, err := Path()
	if err != nil {
		return err
	}
	d, _ := Dir()
	if err := os.MkdirAll(d, 0o700); err != nil {
		return clierrors.New(clierrors.CodeDevice, "create config dir %s", d).WithCause(err)
	}
	b, err := json.MarshalIndent(c, "", "  ")
	if err != nil {
		return clierrors.New(clierrors.CodeInternal, "marshal config").WithCause(err)
	}
	tmp := p + ".tmp"
	if err := os.WriteFile(tmp, append(b, '\n'), 0o600); err != nil {
		return clierrors.New(clierrors.CodeDevice, "write %s", tmp).WithCause(err)
	}
	if err := os.Rename(tmp, p); err != nil {
		return clierrors.New(clierrors.CodeDevice, "replace %s", p).WithCause(err)
	}
	return nil
}

// ResolveTarget applies the precedence ladder. It deliberately re-reads the
// on-disk config every call so profile edits are visible immediately to
// concurrent agents.
func ResolveTarget(profileFlag, macFlag string) (Target, error) {
	target := Target{ProfileSet: profileFlag != "", Profile: profileFlag}
	switch {
	case profileFlag != "":
		target.Profile, target.ProfileSource = profileFlag, "flag"
	case os.Getenv("SSCLI_PROFILE") != "":
		target.Profile = os.Getenv("SSCLI_PROFILE")
		target.ProfileSource = "env"
	default:
		cfg, err := Load()
		if err != nil {
			return target, err
		}
		if cfg.Active != "" {
			target.Profile, target.ProfileSource = cfg.Active, "default"
		}
	}
	if target.Profile != "" {
		cfg, err := Load()
		if err != nil {
			return target, err
		}
		p, ok := cfg.Get(target.Profile)
		if !ok {
			return target, clierrors.New(clierrors.CodeUsage, "unknown profile %q", target.Profile).
				WithValid(cfg.Names()...).
				WithHint("create it with: syska-sonic-cli profile add --name <name> --mac AA:BB:CC:DD:EE:FF")
		}
		// profile supplies the MAC below flag/env in the precedence ladder
		if target.MAC == "" && p.MAC != "" {
			target.MAC, target.MACSource = p.MAC, "profile:"+target.Profile
		}
	}
	switch {
	case macFlag != "":
		normalized, err := ValidateMAC(macFlag)
		if err != nil {
			return target, err
		}
		target.MAC, target.MACSource = normalized, "flag"
	case os.Getenv("SSCLI_MAC") != "":
		normalized, err := ValidateMAC(os.Getenv("SSCLI_MAC"))
		if err != nil {
			return target, clierrors.New(clierrors.CodeUsage, "SSCLI_MAC is invalid: %v", err)
		}
		target.MAC, target.MACSource = normalized, "env"
	}
	return target, nil
}

// NowISO stamps the current time in UTC RFC3339.
func NowISO() string { return time.Now().UTC().Format(time.RFC3339) }

// String renders the config for human-readable inspection.
func (c Config) String() string {
	var sb strings.Builder
	fmt.Fprintf(&sb, "active_profile: %s\n", c.Active)
	for _, p := range c.Profiles {
		fmt.Fprintf(&sb, "  %s\n", p.Name)
		if p.MAC != "" {
			fmt.Fprintf(&sb, "    mac: %s\n", p.MAC)
		}
		if p.CreatedAt != "" {
			fmt.Fprintf(&sb, "    created_at: %s\n", p.CreatedAt)
		}
		if p.Note != "" {
			fmt.Fprintf(&sb, "    note: %s\n", p.Note)
		}
	}
	return sb.String()
}
