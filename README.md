# syska-sonic-cli

Agent-first Bluetooth control for Syska Sonic LED bulbs on Linux.

```console
$ make                 # builds bin/syska-sonic-cli
$ ./bin/syska-sonic-cli discover
$ ./bin/syska-sonic-cli profile add --name desk --mac AA:BB:CC:DD:EE:FF
$ ./bin/syska-sonic-cli profile default desk
$ ./bin/syska-sonic-cli set power on --dry-run --json
$ ./bin/syska-sonic-cli visualize --dry-run --json
```

## Design (for humans and agents)

Tier 1 — table stakes:
- non-interactive by default: no hanging prompts; with no TTY, mutating ops need `--yes`
- structured output: every data command supports `--json` (one envelope on stdout, warnings/stderr separate)
- errors teach: `code`, `message`, `hint`, and `valid:` value lists; stable exit codes (0 ok, 2 usage, 3 confirm, 4 device, 5 protocol, 6 timeout, 7 conflict)
- safe mutation boundaries: `--dry-run` on all device mutations, idempotent ops, durable background jobs
- bounded listings: `--limit` (default 20) and `--offset` everywhere rows are returned

Tier 2 — compounding:
- one vocabulary enforced mechanically (`internal/schema` + consistency tests): same flags everywhere
- three-layer introspection: `--help` (human), `agent-context --json` (versioned machine contract), `docs/agent.md` (long-form)
- async-aware: `--bg` jobs in a durable ledger plus built-in `job wait`/`--wait` polling (agents write no loops)
- persistent identity via profiles: precedence `flag > env (SSCLI_PROFILE/SSCLI_MAC) > default profile`
- two-way I/O: `--deliver stdout|file:<path>|webhook:<url>` for artifacts, `feedback add/list/clear` for friction reports

## Status

- Decoded (jadx of `com.syskaled.application`): the "Syska Sonic" is an **iBluz-based
  classic-Bluetooth speaker + LED module**. Control channel = **SPP RFCOMM (channel 2)**;
  audio over A2DP. Full wire map: `docs/re-notes.md`.
- Working today: `discover` (LE), `pair` (classic, non-interactive), `set color`
  (solid `g(0x08)`, live-confirmed blue hold), `set brightness`, `set effect`
  (mix modes, speed uncalibrated), `visualize` (cautious audio-reactive
  streaming over one persistent SPP session), `sendraw` (byte-level
  calibration), profiles, jobs, feedback, agent-context, doctor. Frames use
  the app's XOR-obfuscated A2 framing (see `docs/re-notes.md`).
- Live-calibrated (2026-10-16): `set power` (lamp switch on/off) and `set effect` (mix modes; speed x10 mapping,
  direction unverified). State `get` is unimplementable: the module never
  sends response frames.

## Environment

- config: `~/.config/syska-sonic-cli/config.json` (XDG_CONFIG_HOME aware)
- state (jobs ledger, feedback log): `~/.local/state/syska-sonic-cli/` (XDG_STATE_HOME aware)
- resolution: `--profile/--mac` > `SSCLI_PROFILE`/`SSCLI_MAC` env > default profile

## Development

```console
$ make build | test | vet | fmt | tidy | check
$ go test ./...
```

Next steps and agent workflows: see `docs/agent.md`.
