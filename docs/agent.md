# agent.md — long-form operating guide for agents

You are likely a tool-using assistant driving this CLI in a loop. This document
is the durable reference after `agent-context --json` (compact, versioned) —
prefer agent-context first; this file covers workflows and failure strategy.

## Response contract (memorize once)

- stdout carries either a human table OR one JSON envelope (`--json`): `{ok, command, op, schema_version, data, warnings, error{code,message,hint,valid}}`
- stderr carries warnings and diagnostics; never parse stderr for data
- exit codes are lowercase semantics: 0 ok · 1 internal · 2 usage (fix args; no retry) ·
  3 confirm (pass `--yes`) · 4 device (transport/environment) · 5 protocol (pending protocol
  work) · 6 timeout (retry with larger `--timeout`) · 7 conflict

## Decision guide

| Situation | Do |
|---|---|
| need machine data | `--json` on every data command |
| mutating action | `--dry-run` first, inspect intent, then run real |
| non-interactive | pass `--yes` (destructive ops), `--no-input` to hard-refuse any prompt |
| listing | `--limit 0` for everything; `--offset` to page |
| identity | profiles: `profile add` once, then rely on the default profile / env |
| artifacts | `--deliver file:<path>` or `webhook:<url>` |
| slow/plan-safe work | `--bg` + `job wait <id>` (or the command's `--wait`) |
| confusing failure | `doctor --json`, then `feedback add --note "..."` |

## Workflows

### 1. Onboard a bulb
```console
syska-sonic-cli discover --json
syska-sonic-cli profile add --name desk --mac AA:BB:CC:DD:EE:FF
syska-sonic-cli profile default desk
syska-sonic-cli doctor --json
```

### 2. Mutate safely
```console
syska-sonic-cli get --json              # snapshot (after protocol lands)
syska-sonic-cli set brightness 40 --dry-run --json
syska-sonic-cli set brightness 40 --json
syska-sonic-cli set color #ff8800 --json
syska-sonic-cli set effect rainbow --speed 3 --json
```

### 3. Audio-reactive visualizer
```console
syska-sonic-cli visualize --dry-run --json
syska-sonic-cli visualize --yes --duration 30s --json
```
The visualizer keeps one SPP session, defaults to 10 fps (hard cap 20),
smooths and quantizes colors, and deduplicates unchanged frames. `--source
auto` prefers the target speaker's Bluetooth monitor when present. Do not
drive the lamp with per-frame `set` invocations; each one reconnects.
All set ops are idempotent; identical repeats converge, so retry only on exit 6.

### 4. Async / background
```console
syska-sonic-cli discover --bg --wait --json
syska-sonic-cli job list --json
syska-sonic-cli job get <id> --json --deliver file:job.json
syska-sonic-cli job wait <id> --timeout 2m --json
```

### 5. Feedback loop
```console
syska-sonic-cli feedback add --note "black-box: set color fails with exit 4" --json
syska-sonic-cli feedback list --json --limit 20
```
No network. Logs live in the state dir listed by `agent-context → paths`.

## Failure strategy

1. exit 2/3 → fix invocation (enum errors list `valid` values).
2. exit 4 → check `doctor` output rows (`config`, `jobs_ledger`, `bluetooth_scan`).
3. exit 5 → the BLE protocol is not implemented for that op yet; `--dry-run`
   intent works today. Track: `agent-context --json → notes[*]` includes protocol status.
4. exit 6 → rerun with larger `--timeout`; jobs are durable, `job wait` reattaches.
5. exit 7 → resolve conflict first (e.g. duplicate profile): `profile list`.

## Updating the surface

The command surface is defined once in `internal/schema/schema.go`; tests enforce
cross-CLI consistency. If you extend the CLI, extend the schema, then implement;
`make check` fails until they agree.
