// Package cli wires the command tree, the shared standard flags, rendering
// and exit codes. Commands never print directly: they build an output.Outcome
// and call App.render, which enforces the stdout/stderr contract.
package cli

import (
	"encoding/json"
	"errors"
	"fmt"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/spf13/cobra"

	"github.com/notaproton/syska-sonic-cli/internal/clierrors"
	"github.com/notaproton/syska-sonic-cli/internal/output"
	"github.com/notaproton/syska-sonic-cli/internal/schema"
)

const (
	cliName      = "syska-sonic-cli"
	docsURL      = "https://github.com/notaproton/syska-sonic-cli/blob/main/README.md"
	agentDocName = "docs/agent.md"
)

// ErrRendered tells Execute that finish() already wrote envelopes/lines to
// the right sinks; Execute only needs the stored exit code.
var ErrRendered = errors.New("outcome already rendered")

// App owns the command tree, renderer settings and background workers.
type App struct {
	root    *cobra.Command
	version string
	exit    int
	wg      sync.WaitGroup
}

// Execute runs the CLI and returns the process exit code.
func Execute(args []string, version string) int {
	a := newApp(version)
	a.root.SetArgs(args)
	err := a.root.Execute()
	a.wg.Wait()
	switch {
	case err == nil:
		return 0
	case errors.Is(err, ErrRendered):
		return a.exit
	default:
		var ce *clierrors.Error
		if !errors.As(err, &ce) {
			ce = clierrors.New(clierrors.CodeUsage, "%v", err)
		}
		r := output.New(a.boolFlag("json"), a.strFlag("deliver"))
		return r.Finish(output.Outcome{Command: cliName, Err: ce})
	}
}

func newApp(version string) *App {
	a := &App{version: version}
	root := &cobra.Command{
		Use:   cliName,
		Short: "Agent-first Bluetooth control for Syska Sonic LED bulbs",
		Long: `Control a Syska Sonic LED bulb over Bluetooth LE. Non-interactive by default,
JSON envelopes on demand (--json), bounded listings, durable background jobs,
profiles for identity, and a machine-readable contract via agent-context.`,
		SilenceUsage:  true,
		SilenceErrors: true,
		RunE: func(cmd *cobra.Command, args []string) error {
			if len(args) == 0 {
				return a.renderNoCommand()
			}
			suggestions := cmd.SuggestionsFor(args[0])
			hint := "run '" + cliName + " --help'; for agents: 'syska-sonic-cli agent-context --json'"
			if len(suggestions) > 0 {
				hint = "did you mean: " + strings.Join(suggestions, ", ") + "? " + hint
			}
			ce := clierrors.New(clierrors.CodeUsage, "unknown command %q", args[0]).WithValid(topLevelCommands(cmd)...)
			ce.Hint = hint
			return a.render(cmd, output.Outcome{Err: ce})
		},
	}

	pf := root.PersistentFlags()
	pf.Bool("json", false, "Emit the machine-readable JSON envelope to stdout")
	pf.String("deliver", "stdout", "Route the JSON artifact: stdout|file:<path>|webhook:<url>")
	pf.Bool("yes", false, "Skip confirmation prompts (required when stdin is not a TTY)")
	pf.Bool("force", false, "Alias of --yes")
	pf.Bool("no-input", false, "Refuse all prompts instead of attempting them")
	pf.Bool("dry-run", false, "Validate and print the operation plan without touching the bulb")
	pf.String("profile", "", "Named target bulb profile; overrides SSCLI_PROFILE and the default profile")
	pf.Duration("timeout", 5*time.Second, "Overall deadline for the operation (e.g. 5s, 30s, 2m)")

	profileCmd := &cobra.Command{
		Use:   "profile",
		Short: "Manage named bulb profiles (add, list, set default, remove)",
		RunE: func(cmd *cobra.Command, args []string) error {
			return a.render(cmd, output.Outcome{
				Err: clierrors.New(clierrors.CodeUsage, "profile needs a subcommand").
					WithValid("add", "list", "get", "default", "rm").
					WithHint("e.g. syska-sonic-cli profile add --name desk --mac AA:BB:CC:DD:EE:FF"),
			})
		},
	}
	profileCmd.AddCommand(a.newProfileListCmd(), a.newProfileAddCmd(), a.newProfileGetCmd(),
		a.newProfileDefaultCmd(), a.newProfileRmCmd())

	jobCmd := &cobra.Command{
		Use:   "job",
		Short: "Inspect background jobs in the durable ledger",
		RunE: func(cmd *cobra.Command, args []string) error {
			return a.render(cmd, output.Outcome{
				Err: clierrors.New(clierrors.CodeUsage, "job needs a subcommand").
					WithValid("list", "get", "wait").
					WithHint("e.g. syska-sonic-cli job list --json"),
			})
		},
	}
	jobCmd.AddCommand(a.newJobListCmd(), a.newJobGetCmd(), a.newJobWaitCmd())

	feedbackCmd := &cobra.Command{
		Use:   "feedback",
		Short: "Record/list friction reports (local only)",
		RunE: func(cmd *cobra.Command, args []string) error {
			return a.render(cmd, output.Outcome{
				Err: clierrors.New(clierrors.CodeUsage, "feedback needs a subcommand").
					WithValid("add", "list", "clear").
					WithHint("e.g. syska-sonic-cli feedback add --note 'set color hangs'"),
			})
		},
	}
	feedbackCmd.AddCommand(a.newFeedbackAddCmd(), a.newFeedbackListCmd(), a.newFeedbackClearCmd())

	root.AddCommand(
		a.newVersionCmd(),
		a.newAgentContextCmd(),
		a.newDoctorCmd(),
		a.newDiscoverCmd(),
		a.newPairCmd(),
		a.newGetCmd(),
		a.newSetCmd(),
		a.newVisualizeCmd(),
		a.newSendRawCmd(),
		profileCmd,
		jobCmd,
		feedbackCmd,
	)
	a.root = root
	return a
}

func (a *App) renderNoCommand() error {
	r := output.New(a.boolFlag("json"), a.strFlag("deliver"))
	a.exit = r.Finish(output.Outcome{
		Command: cliName,
		Err: clierrors.New(clierrors.CodeUsage, "no command given").
			WithValid(topLevelCommands(a.root)...).
			WithHint("'" + cliName + " --help' for humans; '" + cliName + " agent-context --json' for agents"),
	})
	return ErrRendered
}

func (a *App) render(cmd *cobra.Command, o output.Outcome) error {
	if o.Command == "" {
		o.Command = cmdPath(cmd)
	}
	if o.Op == "" {
		if op, err := schemaOp(cmd); err == nil {
			o.Op = op
		}
	}
	r := output.New(a.boolFlag("json"), a.strFlag("deliver"))
	a.exit = r.Finish(o)
	return ErrRendered
}

func cmdPath(cmd *cobra.Command) string {
	p := cmd.CommandPath()
	if p == cliName {
		return ""
	}
	return strings.TrimPrefix(p, cliName+" ")
}

func schemaOp(cmd *cobra.Command) (string, error) {
	name := cmdPath(cmd)
	c, ok := schema.LookupCommand(name)
	if !ok {
		return "", fmt.Errorf("command %q missing from schema", name)
	}
	return c.Op, nil
}

func topLevelCommands(root *cobra.Command) []string {
	var out []string
	for _, c := range root.Commands() {
		out = append(out, c.Name())
	}
	return out
}

func (a *App) boolFlag(name string) bool {
	if f := a.root.PersistentFlags().Lookup(name); f != nil {
		return f.Value.String() == "true"
	}
	return false
}

func (a *App) strFlag(name string) string {
	if f := a.root.PersistentFlags().Lookup(name); f != nil {
		return f.Value.String()
	}
	return ""
}

// durFlagOrDefault reads the shared --timeout; callers pass their per-command
// fallback (used when the user did not set --timeout explicitly).
func (a *App) durFlagOrDefault(fallback time.Duration) (time.Duration, error) {
	f := a.root.PersistentFlags().Lookup("timeout")
	if f == nil || !f.Changed {
		return fallback, nil
	}
	d, err := parseDurationFlag(f.Value.String())
	if err != nil {
		return 0, err
	}
	return d, nil
}

func parseDurationFlag(raw string) (time.Duration, error) {
	d, err := time.ParseDuration(raw)
	if err != nil {
		return 0, clierrors.New(clierrors.CodeUsage, "--timeout %q is not a duration", raw).
			WithHint("valid formats: 5s, 30s, 1m30s, 2m")
	}
	return d, nil
}

func dataOf(v any) json.RawMessage { b, _ := json.Marshal(v); return b }

// Local (command-scoped) flag readers below. They tolerate missing flags so
// helpers can be shared across commands, and surfaces parse errors as
// usage-class teachable errors where necessary.

func stringFlagLocal(cmd *cobra.Command, name string) string {
	if f := cmd.Flags().Lookup(name); f != nil {
		return f.Value.String()
	}
	return ""
}

func boolFlagLocal(cmd *cobra.Command, name string) bool {
	if f := cmd.Flags().Lookup(name); f != nil {
		return f.Value.String() == "true"
	}
	return false
}

func intFlagLocal(cmd *cobra.Command, name string, fallback int) int {
	if f := cmd.Flags().Lookup(name); f != nil {
		if n, err := strconv.Atoi(f.Value.String()); err == nil {
			return n
		}
	}
	return fallback
}

func durationFlagLocal(cmd *cobra.Command, name string, fallback time.Duration) time.Duration {
	if f := cmd.Flags().Lookup(name); f != nil {
		if d, err := time.ParseDuration(f.Value.String()); err == nil {
			return d
		}
	}
	return fallback
}

// limitOffset validates and returns the bounded-listing pair: default 20 rows.
func limitOffset(cmd *cobra.Command) (int, int, error) {
	limit := intFlagLocal(cmd, "limit", 20)
	if limit < 0 {
		return 0, 0, clierrors.New(clierrors.CodeUsage, "--limit %d is negative", limit).
			WithHint("--limit 0 means 'all rows'; the default is 20")
	}
	offset := intFlagLocal(cmd, "offset", 0)
	if offset < 0 {
		return 0, 0, clierrors.New(clierrors.CodeUsage, "--offset %d is negative", offset).
			WithHint("skip rows forward with a positive --offset")
	}
	return limit, offset, nil
}
