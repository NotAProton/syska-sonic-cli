package cli

import (
	"bufio"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/spf13/cobra"

	"github.com/notaproton/syska-sonic-cli/internal/clierrors"
	"github.com/notaproton/syska-sonic-cli/internal/config"
	"github.com/notaproton/syska-sonic-cli/internal/output"
)

// feedbackRow is one JSON line in feedback.log (state dir).
type feedbackRow struct {
	At   string `json:"at"`
	Note string `json:"note"`
}

func feedbackPath() (string, error) {
	return config.StatePath("feedback.log")
}

func (a *App) newFeedbackAddCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "add",
		Short: "Record friction/feedback locally (no network, no telemetry)",
		RunE: func(cmd *cobra.Command, args []string) error {
			note := strings.TrimSpace(stringFlagLocal(cmd, "note"))
			if note == "" {
				return a.render(cmd, output.Outcome{
					Err: clierrors.New(clierrors.CodeUsage, "--note is required").
						WithHint("e.g. syska-sonic-cli feedback add --note 'set color fades after pairing'"),
				})
			}
			p, err := feedbackPath()
			if err != nil {
				return a.render(cmd, output.Outcome{Err: err})
			}
			if err := os.MkdirAll(filepath.Dir(p), 0o700); err != nil {
				return a.render(cmd, output.Outcome{
					Err: clierrors.New(clierrors.CodeDevice, "create state dir for feedback log").WithCause(err),
				})
			}
			f, err := os.OpenFile(p, os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0o600)
			if err != nil {
				return a.render(cmd, output.Outcome{
					Err: clierrors.New(clierrors.CodeDevice, "open feedback log %s", p).WithCause(err).
						WithHint("the state dir should exist; run syska-sonic-cli doctor"),
				})
			}
			defer f.Close()
			row := feedbackRow{At: time.Now().UTC().Format(time.RFC3339Nano), Note: note}
			line, merr := json.Marshal(row)
			if merr != nil {
				return a.render(cmd, output.Outcome{
					Err: clierrors.New(clierrors.CodeInternal, "encode feedback row").WithCause(merr),
				})
			}
			if _, werr := f.Write(append(line, '\n')); werr != nil {
				return a.render(cmd, output.Outcome{
					Err: clierrors.New(clierrors.CodeDevice, "append feedback log").WithCause(werr),
				})
			}
			return a.render(cmd, output.Outcome{
				Human: []string{"feedback recorded in " + p},
				Data:  dataOf(map[string]any{"entry": row}),
			})
		},
	}
	cmd.Flags().String("note", "", "Feedback text (required)")
	return cmd
}

func (a *App) newFeedbackListCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "list",
		Short: "List recorded feedback (newest first)",
		RunE: func(cmd *cobra.Command, args []string) error {
			limit, offset, lerr := limitOffset(cmd)
			if lerr != nil {
				return a.render(cmd, output.Outcome{Err: lerr})
			}
			p, perr := feedbackPath()
			if perr != nil {
				return a.render(cmd, output.Outcome{Err: perr})
			}
			f, oerr := os.Open(p)
			if os.IsNotExist(oerr) {
				return a.render(cmd, output.Outcome{
					Human: []string{"no feedback recorded yet"},
					Data:  dataOf(map[string]any{"entries": []feedbackRow{}}),
				})
			}
			if oerr != nil {
				return a.render(cmd, output.Outcome{
					Err: clierrors.New(clierrors.CodeDevice, "read feedback log %s", p).WithCause(oerr),
				})
			}
			defer f.Close()

			var rows []feedbackRow
			scanner := bufio.NewScanner(f)
			scanner.Buffer(make([]byte, 0, 64*1024), 1024*1024)
			for scanner.Scan() {
				var row feedbackRow
				if json.Unmarshal(scanner.Bytes(), &row) == nil && row.Note != "" {
					rows = append(rows, row)
				}
			}
			if serr := scanner.Err(); serr != nil {
				return a.render(cmd, output.Outcome{
					Err: clierrors.New(clierrors.CodeDevice, "scan feedback log").WithCause(serr),
				})
			}
			// newest first
			total := len(rows)
			reversed := make([]feedbackRow, 0, total)
			for i := total - 1; i >= 0; i-- {
				reversed = append(reversed, rows[i])
			}
			sel := reversed
			if offset > 0 {
				if offset >= len(sel) {
					sel = nil
				} else {
					sel = sel[offset:]
				}
			}
			truncated := false
			if limit > 0 && len(sel) > limit {
				sel = sel[:limit]
				truncated = true
			}
			var warnings []string
			if truncated {
				warnings = []string{fmt.Sprintf("showing %d of %d rows; use --limit 0 for all", limit, total)}
			}
			human := make([]string, 0, len(sel))
			for _, r := range sel {
				human = append(human, fmt.Sprintf("%s  %s", r.At, r.Note))
			}
			if len(human) == 0 {
				human = []string{"no feedback recorded yet"}
			}
			return a.render(cmd, output.Outcome{
				Human:    human,
				Data:     dataOf(map[string]any{"entries": sel, "total": total, "truncated": truncated}),
				Warnings: warnings,
			})
		},
	}
	cmd.Flags().Int("limit", 20, "Max rows (0 = all)")
	cmd.Flags().Int("offset", 0, "Rows skipped after sorting")
	return cmd
}

func (a *App) newFeedbackClearCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "clear",
		Short: "Delete the feedback log (destructive, needs --yes unless interactive)",
		RunE: func(cmd *cobra.Command, args []string) error {
			p, err := feedbackPath()
			if err != nil {
				return a.render(cmd, output.Outcome{Err: err})
			}
			if err := a.confirm("delete all locally recorded feedback (" + p + ")"); err != nil {
				return a.render(cmd, output.Outcome{Err: err})
			}
			if rerr := os.Remove(p); rerr != nil && !os.IsNotExist(rerr) {
				return a.render(cmd, output.Outcome{
					Err: clierrors.New(clierrors.CodeDevice, "remove feedback log %s", p).WithCause(rerr),
				})
			}
			return a.render(cmd, output.Outcome{
				Human: []string{"feedback log cleared: " + p},
				Data:  dataOf(map[string]any{"cleared": true}),
			})
		},
	}
}
