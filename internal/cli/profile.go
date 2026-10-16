package cli

import (
	"fmt"
	"strings"

	"github.com/spf13/cobra"

	"github.com/notaproton/syska-sonic-cli/internal/clierrors"
	"github.com/notaproton/syska-sonic-cli/internal/config"
	"github.com/notaproton/syska-sonic-cli/internal/output"
)

func hiddenMAC(mac string) string {
	if strings.TrimSpace(mac) == "" {
		return "-"
	}
	return mac
}

func (a *App) newProfileListCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "list",
		Short: "List stored bulb profiles",
		RunE: func(cmd *cobra.Command, args []string) error {
			cfg, err := config.Load()
			if err != nil {
				return a.render(cmd, output.Outcome{Err: err})
			}
			limit, offset, lerr := limitOffset(cmd)
			if lerr != nil {
				return a.render(cmd, output.Outcome{Err: lerr})
			}
			win := cfg.Profiles
			if offset > 0 {
				if offset >= len(win) {
					win = nil
				} else {
					win = win[offset:]
				}
			}
			truncated := false
			if limit > 0 && len(win) > limit {
				win = win[:limit]
				truncated = true
			}
			total := len(cfg.Profiles)
			var warnings []string
			if truncated {
				warnings = []string{fmt.Sprintf("showing %d of %d profiles; use --limit 0 for all", limit, total)}
			}
			return a.render(cmd, output.Outcome{
				Human:    profileRowsHuman(cfg, win),
				Data:     dataOf(map[string]any{"profiles": win, "total": total, "truncated": truncated}),
				Warnings: warnings,
			})
		},
	}
	cmd.Flags().Int("limit", 20, "Max rows (0 = all)")
	cmd.Flags().Int("offset", 0, "Rows skipped after sorting")
	return cmd
}

func profileRowsHuman(cfg config.Config, rows []config.Profile) []string {
	if len(rows) == 0 {
		return []string{"no profiles yet; create one with: " + cliName + " profile add --name desk --mac AA:BB:CC:DD:EE:FF"}
	}
	table := make([][]string, 0, len(rows))
	for _, p := range rows {
		active := ""
		if cfg.Active == p.Name {
			active = "*"
		}
		table = append(table, []string{active, p.Name, hiddenMAC(p.MAC), p.Note})
	}
	lines := output.Table([]string{"DEFAULT", "NAME", "MAC", "NOTE"}, table)
	return append(lines, fmt.Sprintf("%d profile(s)", len(rows)))
}

func (a *App) newProfileAddCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "add <name>",
		Short: "Create a bulb profile",
		RunE: func(cmd *cobra.Command, args []string) error {
			var name string
			switch {
			case len(args) == 0:
				name = stringFlagLocal(cmd, "name")
				if name == "" {
					return a.render(cmd, output.Outcome{
						Err: clierrors.New(clierrors.CodeUsage, "profile add needs a name").
							WithHint("positional: syska-sonic-cli profile add desk --mac AA:BB:CC:DD:EE:FF, or flags: profile add --name desk --mac ..."),
					})
				}
			case len(args) == 1:
				name = args[0]
				if flagName := stringFlagLocal(cmd, "name"); flagName != "" && flagName != name {
					return a.render(cmd, output.Outcome{
						Err: clierrors.New(clierrors.CodeUsage, "name given twice: %q and %q", name, flagName).
							WithHint("pass the name once: positional or --name"),
					})
				}
			default:
				return a.render(cmd, output.Outcome{
					Err: clierrors.New(clierrors.CodeUsage, "profile add takes at most one name argument").
						WithHint("e.g. syska-sonic-cli profile add desk --mac AA:BB:CC:DD:EE:FF"),
				})
			}
			if err := config.ValidateName(name); err != nil {
				return a.render(cmd, output.Outcome{Err: err})
			}
			var mac string
			if v := stringFlagLocal(cmd, "mac"); v != "" {
				normalized, verr := config.ValidateMAC(v)
				if verr != nil {
					return a.render(cmd, output.Outcome{Err: verr})
				}
				mac = normalized
			}
			cfg, err := config.Load()
			if err != nil {
				return a.render(cmd, output.Outcome{Err: err})
			}
			if _, exists := cfg.Get(name); exists {
				return a.render(cmd, output.Outcome{
					Err: clierrors.New(clierrors.CodeConflict, "profile %q already exists", name).
						WithHint("replace it with: syska-sonic-cli profile rm %s --yes, then re-add", args[0]),
				})
			}
			p := config.Profile{Name: name, MAC: mac, Note: stringFlagLocal(cmd, "note"), CreatedAt: config.NowISO()}
			cfg.Set(p)
			if cfg.Active == "" {
				cfg.Active = p.Name
			}
			if err := cfg.Save(); err != nil {
				return a.render(cmd, output.Outcome{Err: err})
			}
			return a.render(cmd, output.Outcome{
				Human: []string{"profile added: " + p.Name},
				Data:  dataOf(map[string]any{"profile": p, "is_default": cfg.Active == p.Name}),
			})
		},
	}
	cmd.Flags().String("name", "", "Profile name (alternative to the positional form)")
	cmd.Flags().String("mac", "", "MAC address AA:BB:CC:DD:EE:FF (optional; pair/discover may fill it later)")
	cmd.Flags().String("note", "", "Optional human note")
	return cmd
}

func (a *App) newProfileGetCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "get <name>",
		Short: "Show one profile ('default' resolves to the active profile)",
		RunE: func(cmd *cobra.Command, args []string) error {
			if len(args) != 1 {
				return a.render(cmd, output.Outcome{
					Err: clierrors.New(clierrors.CodeUsage, "profile get needs exactly one name argument").
						WithHint("e.g. syska-sonic-cli profile get desk, or 'default'"),
				})
			}
			cfg, err := config.Load()
			if err != nil {
				return a.render(cmd, output.Outcome{Err: err})
			}
			name := args[0]
			if name == "default" {
				name = cfg.Active
			}
			p, ok := cfg.Get(name)
			if !ok {
				return a.render(cmd, output.Outcome{
					Err: clierrors.New(clierrors.CodeUsage, "unknown profile %q", name).
						WithValid(cfg.Names()...).
						WithHint("see all with: syska-sonic-cli profile list"),
				})
			}
			return a.render(cmd, output.Outcome{
				Human: []string{
					fmt.Sprintf("profile %s (mac %s)", p.Name, hiddenMAC(p.MAC)),
					"is_default: " + fmt.Sprint(cfg.Active == p.Name),
				},
				Data: dataOf(map[string]any{"profile": p, "is_default": cfg.Active == p.Name}),
			})
		},
	}
}

func (a *App) newProfileDefaultCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "default <name>",
		Short: "Set the default (active) profile",
		RunE: func(cmd *cobra.Command, args []string) error {
			if len(args) != 1 {
				return a.render(cmd, output.Outcome{
					Err: clierrors.New(clierrors.CodeUsage, "profile default needs exactly one name argument").
						WithHint("e.g. syska-sonic-cli profile default desk"),
				})
			}
			cfg, err := config.Load()
			if err != nil {
				return a.render(cmd, output.Outcome{Err: err})
			}
			if _, ok := cfg.Get(args[0]); !ok {
				return a.render(cmd, output.Outcome{
					Err: clierrors.New(clierrors.CodeUsage, "unknown profile %q", args[0]).
						WithValid(cfg.Names()...).
						WithHint("see all with: syska-sonic-cli profile list"),
				})
			}
			cfg.Active = args[0]
			if err := cfg.Save(); err != nil {
				return a.render(cmd, output.Outcome{Err: err})
			}
			return a.render(cmd, output.Outcome{
				Human: []string{"default profile: " + cfg.Active},
				Data:  dataOf(map[string]any{"profile": cfg.Active}),
			})
		},
	}
}

func (a *App) newProfileRmCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "rm <name>",
		Short: "Delete a profile (destructive, needs --yes unless interactive)",
		RunE: func(cmd *cobra.Command, args []string) error {
			if len(args) != 1 {
				return a.render(cmd, output.Outcome{
					Err: clierrors.New(clierrors.CodeUsage, "profile rm needs exactly one name argument").
						WithHint("e.g. syska-sonic-cli profile rm desk --yes"),
				})
			}
			cfg, err := config.Load()
			if err != nil {
				return a.render(cmd, output.Outcome{Err: err})
			}
			if _, ok := cfg.Get(args[0]); !ok {
				return a.render(cmd, output.Outcome{
					Err: clierrors.New(clierrors.CodeUsage, "unknown profile %q", args[0]).
						WithValid(cfg.Names()...).
						WithHint("see all with: syska-sonic-cli profile list"),
				})
			}
			if err := a.confirm(fmt.Sprintf("delete profile %q", args[0])); err != nil {
				return a.render(cmd, output.Outcome{Err: err})
			}
			_, wasActive := cfg.Delete(args[0])
			if wasActive {
				cfg.Active = ""
			}
			if err := cfg.Save(); err != nil {
				return a.render(cmd, output.Outcome{Err: err})
			}
			warnings := []string{}
			if wasActive {
				warnings = []string{"deleted profile was the default; no default now (set one with profile default <name>)"}
			}
			return a.render(cmd, output.Outcome{
				Human:    []string{"profile deleted: " + args[0]},
				Data:     dataOf(map[string]any{"deleted": args[0]}),
				Warnings: warnings,
			})
		},
	}
}
