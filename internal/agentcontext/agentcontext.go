// Package agentcontext renders the machine-readable control surface for
// agents: one versioned JSON document built mechanically from the schema
// package, so docs and reality can never drift.
package agentcontext

import (
	"encoding/json"
	"fmt"
	"sort"
	"strings"

	"github.com/notaproton/syska-sonic-cli/internal/clierrors"
	"github.com/notaproton/syska-sonic-cli/internal/config"
	"github.com/notaproton/syska-sonic-cli/internal/schema"
)

// Meta is the complete agent-facing contract (schema_version 1).
type Meta struct {
	SchemaVersion int               `json:"schema_version"`
	Cli           CliInfo           `json:"cli"`
	Contract      Contract          `json:"contract"`
	ExitCodes     map[string]int    `json:"exit_codes"`
	Flags         orderFlags        `json:"flags"`
	Enums         map[string]string `json:"enums"`
	Commands      []CommandInfo     `json:"commands"`
	Workflows     []schema.Workflow `json:"workflows"`
	Notes         []string          `json:"notes"`
	Paths         Paths             `json:"paths"`
}

type CliInfo struct {
	Name         string `json:"name"`
	Version      string `json:"version"`
	DocsURL      string `json:"docs_url,omitempty"`
	BinaryName   string `json:"binary"`
	AgentDocName string `json:"agent_doc"`
}

type Contract struct {
	Stdout     string   `json:"stdout"`
	Stderr     string   `json:"stderr"`
	Envelope   []string `json:"envelope_keys"`
	Delivery   string   `json:"delivery"`
	Idempotent bool     `json:"idempotent_ops"`
	Paging     string   `json:"paging"`
	ErrorModel string   `json:"error_model"`
}

type Paths struct {
	ConfigPath string `json:"config_path"`
	StateDir   string `json:"state_dir"`
	LedgerPath string `json:"ledger_path"`
	Feedback   string `json:"feedback_log"`
}

type CommandInfo struct {
	Name        string    `json:"name"`
	Op          string    `json:"op"`
	Group       string    `json:"group"`
	Mutating    bool      `json:"mutating"`
	Destructive bool      `json:"destructive,omitempty"`
	Positionals []string  `json:"positionals,omitempty"`
	Flags       []FlagUse `json:"flags"`
	Examples    []string  `json:"examples,omitempty"`
}

type FlagUse struct {
	Name    string `json:"name"`
	Type    string `json:"type"`
	Default string `json:"default,omitempty"`
}

type orderFlags struct {
	list []schema.Flag
}

// MarshalJSON keeps flag order deterministic (registry map order is random).
func (o orderFlags) MarshalJSON() ([]byte, error) {
	names := make([]string, 0, len(o.list))
	byName := map[string]schema.Flag{}
	for _, f := range o.list {
		byName[f.Name] = f
		names = append(names, f.Name)
	}
	sort.Strings(names)
	out := make(map[string]schema.Flag, len(names))
	for _, n := range names {
		out[n] = byName[n]
	}
	return json.Marshal(out)
}

// Build pulls the contract from the schema plus runtime paths.
func Build(cliName, version, docsURL, agentDoc string) (*Meta, error) {
	cfgPath, err := config.Path()
	if err != nil {
		return nil, err
	}
	stateDir, err := config.StateDir()
	if err != nil {
		return nil, err
	}
	ledgerPath, err := config.StatePath("jobs.json")
	if err != nil {
		return nil, err
	}
	feedbackPath, err := config.StatePath("feedback.log")
	if err != nil {
		return nil, err
	}

	commands := make([]CommandInfo, 0, len(schema.Commands))
	for _, c := range schema.Commands {
		ci := CommandInfo{
			Name:        c.Name,
			Op:          c.Op,
			Group:       c.Group,
			Mutating:    c.Mutating,
			Destructive: c.Destructive,
			Examples:    c.Examples,
		}
		for _, p := range c.Positionals {
			ci.Positionals = append(ci.Positionals, p.Name)
		}
		for _, ref := range c.Flags {
			def, ok := schema.Flags[ref.Name]
			if !ok {
				continue
			}
			d := def.Default
			if ref.Default != "" {
				d = ref.Default
			}
			ci.Flags = append(ci.Flags, FlagUse{Name: ref.Name, Type: def.Type, Default: d})
		}
		commands = append(commands, ci)
	}

	enums := make(map[string]string, len(schema.Enums))
	for k, v := range schema.Enums {
		enums[k] = strings.Join(v, ", ")
	}

	exit := make(map[string]int)
	for code, value := range clierrors.All() {
		exit[string(code)] = value
	}

	return &Meta{
		SchemaVersion: schema.Version,
		Cli: CliInfo{
			Name: cliName, Version: version, DocsURL: docsURL,
			BinaryName: cliName, AgentDocName: agentDoc,
		},
		Contract: Contract{
			Stdout: "either a human table OR one JSON envelope (--json); never both",
			Stderr: "warnings and diagnostics; never JSON",
			Envelope: []string{
				"ok", "command", "op", "schema_version",
				"data", "warnings[]", "error{code,message,hint,valid}",
			},
			Delivery:   "--deliver stdout|file:<path>|webhook:<url> routes the JSON artifact",
			Idempotent: true,
			Paging:     "listings use --limit (default 20) and --offset; consider --limit 0 for full lists",
			ErrorModel: "code+message+hint+valid travel in the envelope and as stderr lines; see exit_codes",
		},
		ExitCodes: exit,
		Flags:     orderFlags{list: flagList()},
		Enums:     enums,
		Commands:  commands,
		Workflows: schema.Workflows,
		Notes:     schema.Notes,
		Paths: Paths{
			ConfigPath: cfgPath,
			StateDir:   stateDir,
			LedgerPath: ledgerPath,
			Feedback:   feedbackPath,
		},
	}, nil
}

func flagList() []schema.Flag {
	out := make([]schema.Flag, 0, len(schema.Flags))
	for _, f := range schema.Flags {
		out = append(out, f)
	}
	return out
}

// RawJSON is the pretty-encoded context document for --json output.
func RawJSON(m *Meta) (json.RawMessage, error) {
	b, err := json.MarshalIndent(m, "", "  ")
	if err != nil {
		return nil, fmt.Errorf("encode agent context: %w", err)
	}
	return b, nil
}
