package cli

import (
	"fmt"
	"strings"
	"time"

	"github.com/spf13/cobra"

	"github.com/notaproton/syska-sonic-cli/internal/clierrors"
	"github.com/notaproton/syska-sonic-cli/internal/jobs"
	"github.com/notaproton/syska-sonic-cli/internal/output"
)

func terminalStatus(s string) bool {
	return s == jobs.StatusDone || s == jobs.StatusError
}

func (a *App) newJobListCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "list",
		Short: "List background jobs (newest first)",
		RunE: func(cmd *cobra.Command, args []string) error {
			status := strings.TrimSpace(stringFlagLocal(cmd, "status"))
			if status != "" {
				valid := false
				for _, s := range []string{jobs.StatusPending, jobs.StatusRunning, jobs.StatusDone, jobs.StatusError} {
					if s == status {
						valid = true
					}
				}
				if !valid {
					return a.render(cmd, output.Outcome{
						Err: clierrors.New(clierrors.CodeUsage, "unknown --status %q", status).
							WithValid(jobs.StatusPending, jobs.StatusRunning, jobs.StatusDone, jobs.StatusError),
					})
				}
			}
			limit, offset, lerr := limitOffset(cmd)
			if lerr != nil {
				return a.render(cmd, output.Outcome{Err: lerr})
			}
			ledger, err := jobs.Load()
			if err != nil {
				return a.render(cmd, output.Outcome{Err: err})
			}
			rows := ledger.List(status, limit, offset)
			total := len(ledger.Jobs)
			if len(rows) == 0 {
				return a.render(cmd, output.Outcome{
					Human: []string{"no job rows" + statusSuffix(status)},
					Data:  dataOf(map[string]any{"jobs": rows, "total": total}),
				})
			}
			table := make([][]string, 0, len(rows))
			for _, j := range rows {
				table = append(table, []string{j.ID, j.Status, j.Kind, j.UpdatedAt})
			}
			lines := output.Table([]string{"ID", "STATUS", "KIND", "UPDATED"}, table)
			return a.render(cmd, output.Outcome{
				Human: append(lines, fmt.Sprintf("%d job(s), %d total", len(rows), total)),
				Data:  dataOf(map[string]any{"jobs": rows, "total": total}),
			})
		},
	}
	cmd.Flags().Int("limit", 20, "Max rows (0 = all)")
	cmd.Flags().Int("offset", 0, "Rows skipped after sorting")
	cmd.Flags().String("status", "", "Filter by job status")
	return cmd
}

func statusSuffix(status string) string {
	if status == "" {
		return ""
	}
	return " (" + status + ")"
}

func (a *App) newJobGetCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "get <id>",
		Short: "Show one job row",
		RunE: func(cmd *cobra.Command, args []string) error {
			if len(args) != 1 {
				return a.render(cmd, output.Outcome{
					Err: clierrors.New(clierrors.CodeUsage, "job get needs exactly one id").
						WithHint("find ids with: syska-sonic-cli job list --json"),
				})
			}
			ledger, err := jobs.Load()
			if err != nil {
				return a.render(cmd, output.Outcome{Err: err})
			}
			j, ok := ledger.Get(args[0])
			if !ok {
				return a.render(cmd, output.Outcome{
					Err: clierrors.New(clierrors.CodeUsage, "unknown job id %q", args[0]).
						WithHint("find ids with: syska-sonic-cli job list --json --limit 0"),
				})
			}
			return a.render(cmd, output.Outcome{
				Human: jobHuman(j),
				Data:  dataOf(map[string]any{"job": j}),
			})
		},
	}
}

func jobHuman(j jobs.Job) []string {
	lines := []string{
		fmt.Sprintf("job %s [%s] kind=%s updated=%s", j.ID, j.Status, j.Kind, j.UpdatedAt),
	}
	if j.ErrorMessage != "" {
		lines = append(lines, "error: "+j.ErrorMessage)
	}
	if j.Status == jobs.StatusDone && len(j.Result) > 0 {
		lines = append(lines, fmt.Sprintf("result bytes: %d", len(j.Result)))
	}
	return lines
}

func (a *App) newJobWaitCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "wait <id>",
		Short: "Block until a job reaches done/error (no client polling needed)",
		RunE: func(cmd *cobra.Command, args []string) error {
			if len(args) != 1 {
				return a.render(cmd, output.Outcome{
					Err: clierrors.New(clierrors.CodeUsage, "job wait needs exactly one id").
						WithHint("find ids with: syska-sonic-cli job list --json"),
				})
			}
			dur, derr := a.durFlagOrDefault(30 * time.Second)
			if derr != nil {
				return a.render(cmd, output.Outcome{Err: derr})
			}
			ledger, lerr := jobs.Load()
			if lerr != nil {
				return a.render(cmd, output.Outcome{Err: lerr})
			}
			if _, ok := ledger.Get(args[0]); !ok {
				return a.render(cmd, output.Outcome{
					Err: clierrors.New(clierrors.CodeUsage, "unknown job id %q", args[0]).
						WithHint("find ids with: syska-sonic-cli job list --json --limit 0"),
				})
			}
			iv := durationFlagLocal(cmd, "interval", 200*time.Millisecond)
			if iv < 50*time.Millisecond {
				iv = 50 * time.Millisecond
			}
			j, werr := a.waitJob(args[0], dur, iv)
			if werr != nil {
				return a.render(cmd, output.Outcome{Err: werr})
			}
			out := output.Outcome{Human: jobHuman(j), Data: dataOf(map[string]any{"job": j})}
			if j.Status == jobs.StatusError {
				je := clierrors.New(clierrors.CodeDevice, "job %s finished with error", j.ID)
				je.Hint = "inspection: syska-sonic-cli job get " + j.ID
				out.Err = je
			}
			return a.render(cmd, out)
		},
	}
	cmd.Flags().Duration("interval", 200*time.Millisecond, "Poll interval while waiting")
	return cmd
}

func (a *App) waitJob(id string, timeout, interval time.Duration) (jobs.Job, error) {
	deadline := time.Now().Add(timeout)
	for {
		ledger, err := jobs.Load()
		if err != nil {
			return jobs.Job{}, err
		}
		if j, ok := ledger.Get(id); ok && terminalStatus(j.Status) {
			return j, nil
		}
		if !time.Now().Before(deadline) {
			te := clierrors.New(clierrors.CodeTimeout, "job %s did not finish within %s", id, timeout)
			te.Hint = "re-run: syska-sonic-cli job wait " + id + " --timeout 2m; or check state with job get " + id
			return jobs.Job{}, te
		}
		time.Sleep(interval)
	}
}
