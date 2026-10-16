package cli

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/spf13/cobra"

	"github.com/notaproton/syska-sonic-cli/internal/agentcontext"
	"github.com/notaproton/syska-sonic-cli/internal/clierrors"
	"github.com/notaproton/syska-sonic-cli/internal/config"
	"github.com/notaproton/syska-sonic-cli/internal/jobs"
	"github.com/notaproton/syska-sonic-cli/internal/output"
	"github.com/notaproton/syska-sonic-cli/internal/transport"
)

func (a *App) newVersionCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "version",
		Short: "Print the CLI version",
		RunE: func(cmd *cobra.Command, args []string) error {
			return a.render(cmd, output.Outcome{
				Human: []string{"syska-sonic-cli version " + a.version},
				Data:  dataOf(map[string]any{"version": a.version}),
			})
		},
	}
}

func (a *App) newAgentContextCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "agent-context",
		Short: "Machine-readable description of this CLI for agents",
		RunE: func(cmd *cobra.Command, args []string) error {
			meta, err := agentcontext.Build(cliName, a.version, docsURL, agentDocName)
			if err != nil {
				return err
			}
			raw, err := agentcontext.RawJSON(meta)
			if err != nil {
				return err
			}
			return a.render(cmd, output.Outcome{
				Human: []string{
					fmt.Sprintf("schema_version %d; %d commands documented", meta.SchemaVersion, len(meta.Commands)),
					"JSON: " + cliName + " agent-context --json [--deliver file:<path>]",
					"long-form: " + agentDocName,
				},
				Data: raw,
			})
		},
	}
}

type check struct {
	Name   string `json:"name"`
	OK     bool   `json:"ok"`
	Detail string `json:"detail"`
	Hint   string `json:"hint,omitempty"`
}

func checkFromErr(name string, err error) check {
	var ce *clierrors.Error
	if errors.As(err, &ce) {
		hint := ce.Hint
		if hint == "" {
			hint = ce.Error()
		}
		return check{Name: name, OK: false, Detail: ce.Message, Hint: hint}
	}
	return check{Name: name, OK: false, Detail: err.Error()}
}

func (a *App) newDoctorCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "doctor",
		Short: "Check the local environment: config, state, Bluetooth",
		RunE: func(cmd *cobra.Command, args []string) error {
			checks := make([]check, 0, 5)

			checks = append(checks, check{Name: "version", OK: true, Detail: a.version})

			cfgPath, _ := config.Path()
			if _, err := config.Load(); err != nil {
				checks = append(checks, checkFromErr("config", err))
			} else {
				checks = append(checks, check{Name: "config", OK: true, Detail: cfgPath})
			}

			stateDir, _ := config.StateDir()
			if ledger, err := jobs.Load(); err != nil {
				checks = append(checks, checkFromErr("jobs_ledger", err))
			} else {
				checks = append(checks, check{
					Name:   "jobs_ledger",
					OK:     true,
					Detail: fmt.Sprintf("%d job(s) on record (%s)", len(ledger.Jobs), stateDir),
				})
			}

			dur, derr := a.durFlagOrDefault(3 * time.Second)
			if derr != nil {
				return derr
			}
			scanCtx, cancel := context.WithTimeout(context.Background(), dur)
			rows, berr := transport.Discover(scanCtx, transport.Filter{})
			cancel()
			if berr != nil {
				checks = append(checks, checkFromErr("bluetooth_scan", berr))
			} else {
				checks = append(checks, check{
					Name:   "bluetooth_scan",
					OK:     true,
					Detail: fmt.Sprintf("%d BLE device(s) visible", len(rows)),
				})
			}

			healthy := true
			for _, c := range checks {
				if !c.OK {
					healthy = false
				}
			}

			human := make([]string, 0, len(checks))
			for _, c := range checks {
				status := "ok"
				if !c.OK {
					status = "FAIL"
				}
				human = append(human, fmt.Sprintf("[%-4s] %s: %s", status, c.Name, c.Detail))
				if c.Hint != "" {
					human = append(human, "        hint: "+c.Hint)
				}
			}

			out := output.Outcome{
				Human: human,
				Data:  dataOf(map[string]any{"checks": checks, "healthy": healthy}),
			}
			if !healthy {
				out.Err = clierrors.New(clierrors.CodeDevice, "%d environment check(s) failed", countFailed(checks)).
					WithHint("fix the failing checks; device ops need a healthy environment per this command")
			}
			return a.render(cmd, out)
		},
	}
}

func countFailed(checks []check) int {
	n := 0
	for _, c := range checks {
		if !c.OK {
			n++
		}
	}
	return n
}
