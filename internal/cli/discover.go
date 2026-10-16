package cli

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"regexp"
	"strings"
	"time"

	"github.com/spf13/cobra"

	"github.com/notaproton/syska-sonic-cli/internal/clierrors"
	"github.com/notaproton/syska-sonic-cli/internal/config"
	"github.com/notaproton/syska-sonic-cli/internal/jobs"
	"github.com/notaproton/syska-sonic-cli/internal/output"
	"github.com/notaproton/syska-sonic-cli/internal/transport"
)

var uuidRE = regexp.MustCompile(`^[0-9a-fA-F]{4}$|^[0-9a-fA-F]{8}-[0-9a-fA-F]{4}-[0-9a-fA-F]{4}-[0-9a-fA-F]{4}-[0-9a-fA-F]{12}$`)

func normalizeUUID(spec string) (string, error) {
	if uuidRE.MatchString(spec) {
		s := strings.ToLower(spec)
		if len(s) == 4 {
			return "0000" + s + "-0000-1000-8000-00805f9b34fb", nil
		}
		return s, nil
	}
	return "", clierrors.New(clierrors.CodeUsage, "service UUID %q is not valid", spec).
		WithHint("16-bit forms like 'ff00' expand to 0000ff00-0000-1000-8000-00805f9b34fb")
}

type discoverResult struct {
	Devices   []transport.Device `json:"devices"`
	Total     int                `json:"total"`
	Truncated bool               `json:"truncated"`
}

func (a *App) newDiscoverCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "discover",
		Short: "Scan for nearby BLE devices, including the bulb",
		Long: `Bounded LE scan through BlueZ. Rows sort by signal strength.
Filters narrow after scanning; listing bounds via --limit/--offset.

--bg runs through the durable job ledger; add --wait to block until
done/error so agents never write their own polling loop.`,
		RunE: a.runDiscover,
	}
	f := cmd.Flags()
	f.String("service", "", "Only devices advertising this GATT service UUID; short form 'ff00' allowed")
	f.String("name", "", "Case-insensitive substring filter on advertised names")
	f.String("mac", "", "Exact match on a MAC address (scan filter)")
	f.Int("limit", 20, "Max rows (0 = all)")
	f.Int("offset", 0, "Rows skipped after sorting")
	f.Bool("bg", false, "Run as a background job in the durable ledger")
	f.Bool("wait", false, "With --bg: block until the job reaches done/error")
	return cmd
}

func (a *App) runDiscover(cmd *cobra.Command, args []string) error {
	f := transport.Filter{}
	if spec := stringFlagLocal(cmd, "service"); spec != "" {
		u, err := normalizeUUID(spec)
		if err != nil {
			return a.render(cmd, output.Outcome{Err: err})
		}
		f.ServiceUUID = u
	}
	f.NameContains = stringFlagLocal(cmd, "name")
	if mac := stringFlagLocal(cmd, "mac"); mac != "" {
		normalized, verr := config.ValidateMAC(mac)
		if verr != nil {
			return a.render(cmd, output.Outcome{Err: verr})
		}
		f.MAC = normalized
	}
	limit, offset, lerr := limitOffset(cmd)
	if lerr != nil {
		return a.render(cmd, output.Outcome{Err: lerr})
	}
	scanDur, derr := a.durFlagOrDefault(5 * time.Second)
	if derr != nil {
		return a.render(cmd, output.Outcome{Err: derr})
	}

	switch {
	case boolFlagLocal(cmd, "bg") && boolFlagLocal(cmd, "wait"):
		return a.discoverBackground(cmd, f, scanDur, limit, offset, true)
	case boolFlagLocal(cmd, "bg"):
		return a.discoverBackground(cmd, f, scanDur, limit, offset, false)
	default:
		ctx, cancel := context.WithTimeout(context.Background(), scanDur)
		defer cancel()
		rows, err := transport.Discover(ctx, f)
		if err != nil {
			return a.render(cmd, output.Outcome{Err: err})
		}
		return a.renderDiscovery(cmd, rows, limit, offset, nil)
	}
}

func (a *App) renderDiscovery(cmd *cobra.Command, all []transport.Device, limit, offset int, warnings []string) error {
	total := len(all)
	sel := all
	if offset > 0 {
		if offset >= total {
			sel = nil
		} else {
			sel = all[offset:]
		}
	}
	truncated := false
	if limit > 0 && len(sel) > limit {
		sel = sel[:limit]
		truncated = true
	}
	if truncated {
		warnings = append(warnings, fmt.Sprintf("showing %d of %d rows; use --limit 0 for all", limit, total))
	}
	return a.render(cmd, output.Outcome{
		Human:    discoverHuman(sel),
		Data:     dataOf(discoverResult{Devices: sel, Total: total, Truncated: truncated}),
		Warnings: warnings,
	})
}

func discoverHuman(rows []transport.Device) []string {
	if len(rows) == 0 {
		return []string{"no BLE devices visible in the scan window"}
	}
	table := make([][]string, 0, len(rows))
	for _, r := range rows {
		state := "idle"
		if r.Connected {
			state = "connected"
		}
		rssi := fmt.Sprintf("%d", r.RSSI)
		if r.RSSI == 0 {
			rssi = "n/a"
		}
		table = append(table, []string{r.Address, r.Name, rssi, state})
	}
	lines := output.Table([]string{"ADDRESS", "NAME", "RSSI", "STATE"}, table)
	return append(lines, fmt.Sprintf("%d device(s)", len(rows)))
}

func (a *App) discoverBackground(cmd *cobra.Command, f transport.Filter, scanDur time.Duration, limit, offset int, wait bool) error {
	job, err := jobs.Submit("discover")
	if err != nil {
		return a.render(cmd, output.Outcome{Err: err})
	}
	a.wg.Add(1)
	go a.discoverJobWorker(job.ID, f, scanDur)

	if !wait {
		return a.render(cmd, output.Outcome{
			Human: []string{"submitted job " + job.ID + " (runs to completion; exit when done)"},
			Data:  dataOf(map[string]any{"job": *job}),
			Warnings: []string{
				"the process stays alive until the background job finishes",
				"poll-free retrieval: syska-sonic-cli job wait " + job.ID,
			},
		})
	}

	j, werr := a.waitJob(job.ID, scanDur+15*time.Second, 200*time.Millisecond)
	if werr != nil {
		return a.render(cmd, output.Outcome{Err: werr})
	}
	if j.Status == jobs.StatusError {
		return a.render(cmd, output.Outcome{
			Err: clierrors.New(clierrors.CodeDevice, "background discover failed").
				WithCause(errors.New(j.ErrorMessage)),
			Data: dataOf(map[string]any{"job": j}),
		})
	}
	var rows []transport.Device
	_ = json.Unmarshal(j.Result, &rows)
	return a.renderDiscovery(cmd, rows, limit, offset, []string{"rendered from ledger job " + j.ID})
}

func (a *App) discoverJobWorker(id string, f transport.Filter, scanDur time.Duration) {
	defer a.wg.Done()
	_, _ = jobs.MarkRunning(id)
	ctx, cancel := context.WithTimeout(context.Background(), scanDur)
	defer cancel()
	rows, derr := transport.Discover(ctx, f)
	if derr != nil {
		_, _ = jobs.Finish(id, jobs.StatusError, derr.Error(), nil)
		return
	}
	_, _ = jobs.Finish(id, jobs.StatusDone, "", transport.RowsJSON(rows))
}
