// Package schema is the single source of truth for the command surface:
// commands, flags, enums, workflows and agent guidance. Consistency tests
// enforce the vocabulary mechanically (cli/schema_test.go), and the
// agent-context command renders a machine-readable export from it.
package schema

const Version = 1

// Flag naming/type/semantics vocabulary.
type Flag struct {
	Name    string `json:"name"`
	Type    string `json:"type"`
	Default string `json:"default,omitempty"`
	Help    string `json:"help"`
	Enum    string `json:"enum,omitempty"`
}

// FlagRef lists a flag on a command, optionally overriding its default.
type FlagRef struct {
	Name    string `json:"name"`
	Default string `json:"default,omitempty"`
}

type Positional struct {
	Name string `json:"name"`
	Enum string `json:"enum,omitempty"`
	Help string `json:"help"`
}

// Command is one full invocation path, e.g. "profile list".
type Command struct {
	Name        string       `json:"name"`
	Op          string       `json:"op"`
	Group       string       `json:"group"` // meta | device | profile | job | feedback
	Mutating    bool         `json:"mutating"`
	Destructive bool         `json:"destructive,omitempty"`
	Positionals []Positional `json:"positionals,omitempty"`
	Flags       []FlagRef    `json:"flags"`
	Examples    []string     `json:"examples,omitempty"`
}

type Workflow struct {
	Name  string   `json:"name"`
	Steps []string `json:"steps"`
}

var Flags = map[string]Flag{
	"json":     {Name: "json", Type: "bool", Default: "false", Help: "Emit the machine-readable JSON envelope to stdout"},
	"deliver":  {Name: "deliver", Type: "string", Default: "stdout", Help: "Route the JSON artifact: stdout|file:<path>|webhook:<url>", Enum: "deliver"},
	"yes":      {Name: "yes", Type: "bool", Default: "false", Help: "Skip confirmation prompts; required when stdin is not a TTY"},
	"force":    {Name: "force", Type: "bool", Default: "false", Help: "Alias of --yes for consistency across CLIs"},
	"no-input": {Name: "no-input", Type: "bool", Default: "false", Help: "Hard-refuse any prompt instead of attempting it"},
	"dry-run":  {Name: "dry-run", Type: "bool", Default: "false", Help: "Validate and print the operation plan without touching the bulb"},
	"profile":  {Name: "profile", Type: "string", Help: "Named target bulb; overrides SSCLI_PROFILE and the default profile"},
	"mac":      {Name: "mac", Type: "string", Help: "MAC address AA:BB:CC:DD:EE:FF; on targets it overrides the profile, on discover it filters the scan"},
	"speed":    {Name: "speed", Type: "int", Default: "5", Help: "Effect speed 0-10 (lower = slower); effect changes only"},
	"limit":    {Name: "limit", Type: "int", Default: "20", Help: "Max rows returned (0 = unbounded)"},
	"offset":   {Name: "offset", Type: "int", Default: "0", Help: "Rows skipped after sorting"},
	"bg":       {Name: "bg", Type: "bool", Default: "false", Help: "Run as a background job in the durable local ledger"},
	"wait":     {Name: "wait", Type: "bool", Default: "false", Help: "With --bg: block until done/error so agents never poll by hand"},
	"timeout":  {Name: "timeout", Type: "duration", Default: "5s", Help: "Operation deadline (e.g. 5s, 30s, 2m)"},
	"interval": {Name: "interval", Type: "duration", Default: "150ms", Help: "Poll interval while waiting on a job"},
	"service":  {Name: "service", Type: "string", Help: "discover only: require this advertised GATT service UUID"},
	"source":   {Name: "source", Type: "string", Default: "auto", Help: "visualize audio source: auto, @DEFAULT_MONITOR@, @DEFAULT_SOURCE@, or a Pulse/PipeWire source name"},
	"rate":     {Name: "rate", Type: "int", Default: "10", Help: "visualize maximum frames per second (1-20)"},
	"sensitivity": {
		Name: "sensitivity", Type: "float", Default: "1", Help: "visualize audio sensitivity multiplier (0.1-10)",
	},
	"smoothing": {
		Name: "smoothing", Type: "float", Default: "0.65", Help: "visualize smoothing 0-0.95; higher is calmer",
	},
	"min-brightness": {
		Name: "min-brightness", Type: "int", Default: "6", Help: "visualize minimum brightness percent (0-100)",
	},
	"max-brightness": {
		Name: "max-brightness", Type: "int", Default: "80", Help: "visualize maximum brightness percent (0-100)",
	},
	"duration": {Name: "duration", Type: "duration", Default: "0s", Help: "visualize run length; 0 runs until Ctrl+C"},
	"restore":  {Name: "restore", Type: "string", Default: "white", Help: "visualize exit restore: white, off, or none", Enum: "visualizer_restore"},
	"name":     {Name: "name", Type: "string", Help: "discover: case-insensitive name substring; pair: advertised name to record"},
	"status":   {Name: "status", Type: "string", Help: "job list row filter", Enum: "job_status"},
	"note":     {Name: "note", Type: "string", Help: "Feedback text or profile note"},
}

var Enums = map[string][]string{
	"color":       {"white", "red", "orange", "yellow", "green", "cyan", "blue", "purple", "pink"},
	"power_state": {"on", "off"},
	"property":    {"power", "brightness", "color", "effect"},
	"effect":      {"solid", "rainbow", "wave", "breathing", "flash"},
	"job_status":  {"pending", "running", "done", "error"},
	"deliver":     {"stdout", "file:<path>", "webhook:<url>"},
	"visualizer_restore": {
		"white", "off", "none",
	},
}

var Commands = []Command{
	{
		Name: "version", Op: "meta.version", Group: "meta",
		Flags:    []FlagRef{{Name: "json"}},
		Examples: []string{"syska-sonic-cli version --json"},
	},
	{
		Name: "agent-context", Op: "meta.agent_context", Group: "meta",
		Flags:    []FlagRef{{Name: "json"}},
		Examples: []string{"syska-sonic-cli agent-context --json"},
	},
	{
		Name: "doctor", Op: "meta.doctor", Group: "meta",
		Flags: []FlagRef{{Name: "json"}, {Name: "deliver"}},
		Examples: []string{
			"syska-sonic-cli doctor",
			"syska-sonic-cli doctor --json --deliver file:doctor.json",
		},
	},
	{
		Name: "discover", Op: "device.discover", Group: "device",
		Flags: []FlagRef{
			{Name: "json"}, {Name: "deliver"}, {Name: "limit"}, {Name: "offset"},
			{Name: "timeout", Default: "8s"}, {Name: "service"}, {Name: "name"}, {Name: "mac"},
			{Name: "bg"}, {Name: "wait"},
		},
		Examples: []string{
			"syska-sonic-cli discover --json",
			"syska-sonic-cli discover --bg --wait --json",
		},
	},
	{
		Name: "pair", Op: "device.pair", Group: "device", Mutating: true,
		Positionals: []Positional{},
		Flags: []FlagRef{
			{Name: "json"}, {Name: "deliver"}, {Name: "dry-run"}, {Name: "yes"},
			{Name: "mac"}, {Name: "name"}, {Name: "profile"}, {Name: "timeout"},
		},
		Examples: []string{"syska-sonic-cli pair --mac AA:BB:CC:DD:EE:FF --name 'Desk bulb'"},
	},
	{
		Name: "get", Op: "device.get", Group: "device",
		Positionals: []Positional{{Name: "property", Enum: "property", Help: "Optional property; omit reads the full snapshot"}},
		Flags:       []FlagRef{{Name: "json"}, {Name: "deliver"}, {Name: "profile"}, {Name: "timeout"}},
		Examples:    []string{"syska-sonic-cli get --json", "syska-sonic-cli get color"},
	},
	{
		Name: "set", Op: "device.set", Group: "device", Mutating: true,
		Positionals: []Positional{
			{Name: "property", Enum: "property", Help: "Property to change"},
			{Name: "value", Help: "power=on|off, brightness=0-100, color=name|#hex|r,g,b, effect=enum"},
		},
		Flags: []FlagRef{
			{Name: "json"}, {Name: "deliver"}, {Name: "dry-run"}, {Name: "yes"},
			{Name: "profile"}, {Name: "speed"},
		},
		Examples: []string{
			"syska-sonic-cli set power on",
			"syska-sonic-cli set brightness 40 --json",
			"syska-sonic-cli set color #ff8800 --dry-run --json",
			"syska-sonic-cli set effect rainbow --speed 3 --json",
		},
	},
	{
		Name: "visualize", Op: "device.visualize", Group: "device", Mutating: true,
		Flags: []FlagRef{
			{Name: "json"}, {Name: "deliver"}, {Name: "dry-run"}, {Name: "yes"},
			{Name: "profile"}, {Name: "source"}, {Name: "rate"}, {Name: "sensitivity"}, {Name: "smoothing"},
			{Name: "min-brightness"}, {Name: "max-brightness"}, {Name: "duration"}, {Name: "restore"},
		},
		Examples: []string{
			"syska-sonic-cli visualize --dry-run --json",
			"syska-sonic-cli visualize --yes --duration 30s",
			"syska-sonic-cli visualize --yes --source @DEFAULT_MONITOR@ --rate 8",
		},
	},
	{
		Name: "sendraw", Op: "device.sendraw", Group: "device", Mutating: true,
		Positionals: []Positional{{Name: "hexbytes", Help: "Raw bytes to write on the SPP stream, e.g. a208ff000000"}},
		Flags: []FlagRef{
			{Name: "json"}, {Name: "deliver"}, {Name: "dry-run"}, {Name: "yes"},
			{Name: "profile"}, {Name: "timeout"},
		},
		Examples: []string{"syska-sonic-cli sendraw a208ff000000 --yes", "syska-sonic-cli sendraw 'a2 0b 64 00 00 00 40' --dry-run"},
	},
	{
		Name: "profile list", Op: "profile.list", Group: "profile",
		Flags:    []FlagRef{{Name: "json"}, {Name: "deliver"}, {Name: "limit"}, {Name: "offset"}},
		Examples: []string{"syska-sonic-cli profile list --json"},
	},
	{
		Name: "profile add", Op: "profile.add", Group: "profile", Mutating: true,
		Positionals: []Positional{{Name: "name", Help: "New profile name (lowercase letters/digits/-)"}},
		Flags:       []FlagRef{{Name: "json"}, {Name: "mac"}, {Name: "note"}},
		Examples:    []string{"syska-sonic-cli profile add --name desk --mac AA:BB:CC:DD:EE:FF"},
	},
	{
		Name: "profile get", Op: "profile.get", Group: "profile",
		Positionals: []Positional{{Name: "name", Help: "Profile to show, or 'default'"}},
		Flags:       []FlagRef{{Name: "json"}, {Name: "deliver"}},
		Examples:    []string{"syska-sonic-cli profile get default --json"},
	},
	{
		Name: "profile default", Op: "profile.default", Group: "profile", Mutating: true,
		Positionals: []Positional{{Name: "name", Help: "Profile to activate"}},
		Flags:       []FlagRef{{Name: "json"}},
		Examples:    []string{"syska-sonic-cli profile default desk"},
	},
	{
		Name: "profile rm", Op: "profile.rm", Group: "profile", Mutating: true, Destructive: true,
		Positionals: []Positional{{Name: "name", Help: "Profile to delete"}},
		Flags:       []FlagRef{{Name: "json"}, {Name: "yes"}},
		Examples:    []string{"syska-sonic-cli profile rm desk --yes"},
	},
	{
		Name: "job list", Op: "job.list", Group: "job",
		Flags:    []FlagRef{{Name: "json"}, {Name: "deliver"}, {Name: "limit"}, {Name: "offset"}, {Name: "status"}},
		Examples: []string{"syska-sonic-cli job list --json"},
	},
	{
		Name: "job get", Op: "job.get", Group: "job",
		Positionals: []Positional{{Name: "id", Help: "Job id from 'job list'"}},
		Flags:       []FlagRef{{Name: "json"}, {Name: "deliver"}},
		Examples:    []string{"syska-sonic-cli job get a1b2c3d4 --json"},
	},
	{
		Name: "job wait", Op: "job.wait", Group: "job",
		Positionals: []Positional{{Name: "id", Help: "Job to block on"}},
		Flags:       []FlagRef{{Name: "json"}, {Name: "deliver"}, {Name: "timeout", Default: "30s"}, {Name: "interval"}},
		Examples:    []string{"syska-sonic-cli job wait a1b2c3d4 --json --timeout 30s"},
	},
	{
		Name: "feedback add", Op: "feedback.add", Group: "feedback", Mutating: true,
		Positionals: []Positional{},
		Flags:       []FlagRef{{Name: "json"}, {Name: "note"}},
		Examples:    []string{"syska-sonic-cli feedback add --note 'set color fails after pairing'"},
	},
	{
		Name: "feedback list", Op: "feedback.list", Group: "feedback",
		Flags:    []FlagRef{{Name: "json"}, {Name: "deliver"}, {Name: "limit"}, {Name: "offset"}},
		Examples: []string{"syska-sonic-cli feedback list --json --limit 50"},
	},
	{
		Name: "feedback clear", Op: "feedback.clear", Group: "feedback", Mutating: true, Destructive: true,
		Flags:    []FlagRef{{Name: "json"}, {Name: "yes"}},
		Examples: []string{"syska-sonic-cli feedback clear --yes"},
	},
}

var Workflows = []Workflow{
	{
		Name: "onboard",
		Steps: []string{
			"syska-sonic-cli discover --json",
			"syska-sonic-cli profile add --name desk --mac AA:BB:CC:DD:EE:FF",
			"syska-sonic-cli profile default desk",
			"syska-sonic-cli doctor --json",
		},
	},
	{
		Name: "state",
		Steps: []string{
			"syska-sonic-cli get --json",
			"syska-sonic-cli set brightness 40 --json",
			"syska-sonic-cli set color #ff8800 --json",
			"syska-sonic-cli set effect rainbow --speed 3",
		},
	},
	{
		Name: "visualize",
		Steps: []string{
			"syska-sonic-cli visualize --dry-run --json",
			"syska-sonic-cli visualize --yes --duration 30s --rate 10",
			"syska-sonic-cli set color white --json",
		},
	},
	{
		Name: "async",
		Steps: []string{
			"syska-sonic-cli discover --bg --wait --json",
			"syska-sonic-cli job list --json",
			"syska-sonic-cli job get <id> --json",
		},
	},
	{
		Name: "feedback",
		Steps: []string{
			"syska-sonic-cli feedback add --note 'what broke/proposed fix' --json",
			"syska-sonic-cli feedback list --json --limit 20",
		},
	},
}

// Notes is the terse agent-facing behavioral contract.
var Notes = []string{
	"stdout carries the actionable payload (human table or ONE JSON envelope); stderr carries warnings/errors; never mix channels",
	"envelope keys: ok, command, op, schema_version, data, warnings, error{code,message,hint,valid}",
	"exit codes: 0 ok, 1 internal, 2 usage (fix args, no retry), 3 confirm (pass --yes), 4 device, 5 protocol, 6 timeout (retry with larger --timeout), 7 conflict",
	"device set ops are idempotent; safe to retry after --timeout",
	"all mutating device commands accept --dry-run; run it first when unsure",
	"listings are bounded: --limit default 20, --limit 0 for all rows",
	"discover --bg --wait returns the durable job instead of blocking; no client polling loops needed",
	"profiles carry identity (flag --profile > env SSCLI_PROFILE/SSCLI_MAC > default profile) so agents never re-specify flags",
	"protocol status: the device speaks classic-BT SPP (RFCOMM ch2, JL_SPP), NOT GATT, with XOR-obfuscated A2 frames; set color (g 0x08, live-confirmed), brightness, power (lamp switch j 0x0E), effect (mix modes, speed uncalibrated), and visualize (audio-reactive, rate-capped at 20 fps) are wired; get is not implementable (module never responds); sendraw <hex> probes bytes deterministically; docs/re-notes.md holds the decoded frames",
	"profiles and feedback live in XDG config/state dirs; see docs/agent.md",
}

// LookupCommand by full name ("profile rm").
func LookupCommand(name string) (Command, bool) {
	for _, c := range Commands {
		if c.Name == name {
			return c, true
		}
	}
	return Command{}, false
}

// Validate enforces the mechanical consistency rules. Returns the list of
// violations; an empty slice means the surface is consistent.
func Validate() []string {
	var bad []string
	for _, c := range Commands {
		if part, _ := LookupCommand(c.Name); part.Name != c.Name || part.Op != c.Op {
			bad = append(bad, "duplicate command name: "+c.Name)
		}
		local := localMutation(c)
		for _, f := range c.Flags {
			def, ok := Flags[f.Name]
			if !ok {
				bad = append(bad, c.Name+": unknown flag "+f.Name)
				continue
			}
			if f.Name != "timeout" && f.Name != "interval" && f.Default != "" && def.Default != f.Default {
				bad = append(bad, c.Name+": flag "+f.Name+" default drift (registry='"+def.Default+"' override='"+f.Default+"')")
			}
		}
		if c.Mutating && c.Group == "device" && !has(c.Flags, "dry-run") {
			bad = append(bad, c.Name+": mutating device command without --dry-run")
		}
		if c.Destructive && !has(c.Flags, "yes") {
			bad = append(bad, c.Name+": destructive without --yes")
		}
		if c.Mutating && !c.Destructive && !local && !has(c.Flags, "yes") {
			bad = append(bad, c.Name+": mutating remote state without --yes")
		}
		if listing := isListing(c.Name); listing && (!has(c.Flags, "limit") || !has(c.Flags, "offset")) {
			bad = append(bad, c.Name+": listing without --limit/--offset")
		}
		if !(c.Group == "meta" && c.Name == "version") && !has(c.Flags, "json") {
			bad = append(bad, c.Name+": missing --json")
		}
	}
	return bad
}

// localMutation marks changes confined to local config files (reversible with
// standard commands), exempt from the --yes/--dry-run device doctrine.
func localMutation(c Command) bool {
	return c.Group == "profile" || c.Name == "feedback add"
}

func isListing(name string) bool {
	switch name {
	case "discover", "profile list", "job list", "feedback list":
		return true
	}
	return false
}

func has(refs []FlagRef, name string) bool {
	for _, r := range refs {
		if r.Name == name {
			return true
		}
	}
	return false
}
