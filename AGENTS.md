# AGENTS.md — session handoff, syska-sonic-cli

Read this first. Repo: `~/Projects/syska-sonic-cli` (Go + cobra; `make check`
must stay green: gofmt + vet + tests + build). Nothing is committed yet —
`git init` done, first commit pending user go-ahead.

## What the device is (verified)

"Syska Sonic" = **iBluz-based classic-Bluetooth speaker + LED module**
(Actions Bluz chip family). NOT a GATT bulb.

- LE MAC:    `<LE MAC, redacted>`  name "Syska Sonic" (advertises `0x180D`, MF-id 0; companion beacon, not the control channel)
- Classic MAC: `<classic MAC, redacted>`  name "Syska Sonic LED 1.1" (bytes of the LE MAC reversed) — pair/trust **done** on this PC
- SPP channel **2** (RFCOMM); audio over A2DP (0x110B) + AVRCP (0x110E)
- A paired device must have a bluetoothd ACL for a raw RFCOMM dial to work,
  and the SPP session's lifetime is the key flakiness: sessions die after
  ~12-15s and the module requires re-establishing (see ladder below)
- APK decompiled with jadx: `~/Projects/syska-sonic-cli` knowledge comes from
  `com.syskaled.application` (`com.syskaled.bt.*` + `com.actions.ibluz.*`).
  Decompiled sources are in the *session scratchpad*, not the repo — re-run
  `jadx -d <out> --no-res <apk>` if needed (apk: `~/Downloads/com-syskaled-application-7-*.apk`).

## Wire protocol (byte-verified from app classes)

Two frame families share the SPP stream. App's static writer
`bt.a.b.b.a(dos, h0, h1, h2, h3, tail, byte... mid)` emits
`[h0, h1, h2, h3, *mid, tail]`.

**A2 light frames** (`op = 0xA2`):

| cmd | builder (protocol pkg) | wire |
|---|---|---|
| 0x08 solid color | `SolidColor(R,G,B)` | `A2 08 R G 00 00 B` (7B) |
| 0x12 quick color | `ColorQuick(R,G,B)` | `A2 12 R G 00 00 B` (7B) |
| 0x0B brightness | `Brightness(p1, value u16, am)` | `A2 0B p1 hi am 00 lo` (7B) |
| 0x0C gate | `ModeGate(x)` | `A2 0C x 00 00 00 00` (7B) |
| 0x11 mix family | `MixMode(sub,val,b4,b5,b6)` | `A2 11 sub val b5 b6 b4` (7B) |
| 0x0E | `EffectStep(x)` | `A2 0E x 00 00 00` (6B) |
| 0x10 | `EffectPos()` | `A2 10 00 00 00 00` (6B) |
| 0x14 wake | `WakeMode(x,y)` | `A2 14 x y 00 00` (6B) |
| 0x15 | `SleepMode(cmd,a,b,d,e,f)` | `A2 15 a b e f d` (7B) |

**UART bridge** (module system): tone `01 FE 00 00 53 9D 10 00 <param LE32> 00 00 00 00`;
ASCII passthrough `01 FE 00 00 53 89 <len=text+18> 00×9 03 <text> 00`.

Source of the app mappings: g.java (color), d.java (brightness), e-family
(sub 1..4 = UI pages w/ `val` = slider progress), f.java gate `f(12,1)`
emitted immediately before mix-4 color;

## Live calibration log (user-eye observations)

- `A2 0B …` **brightness WORKS** — user saw the light dim (white light).
- **No color yet** from `0x08`(7B corrected), `0x12`(7B), `A2 0C gate` +
  `0x11` mix-4, even 2.5s-gapped single sessions. Light stayed white, dim.
- Module **beeps** on frames (likely feedback tone / or error beep for ignored ops).
- Module never answers on SPP (no RESP frames observed at all).
- SPP session dies ~12-15s in. First-ever session (45s+, 7 frames) survived, so
  rapid reconnect churn may trip it; consider a single long-lived session or
  look for an iBluz keepalive (UNVERIFIED: check `com/actions/ibluz/b/*` and
  `d.java` timers for heartbeat/ping).
- Last probe battery (2026-10-16, single session, 2.5s gaps): dim → gate →
  0x08-blue → 0x12-blue → mix4-blue, then EPIPE. User watching; awaiting
  their latest impression (did 0..4 do anything?).

## Next steps (order)

1. Ask user what the last battery showed; then decide color path (candidates:
   mix-4 with different `sub`s 1..4, or pending gate semantics from h/l/wake).
2. Get root btmon capture ("sudo" w/ password from user): `sudo btmon -w
   capture.pcapng` while the REAL app runs on an Android phone — confirms
   real frames incl. any keepalive; diff vs our emission.
3. Map j(0x0E)/k(0x10)/h(0x14)/l(0x15) = effects/power candidates (sleep/wake
   per app settings UI), then implement `set power`/`set effect` + `get`.
4. Fix SPP flakiness: single persistent session? Probe tool already demonstrates
   single-session flow; consider a CLI-managed local daemon ("sonicd") if the
   app's model needs it — but only if the vendor app does connect-per-op too.
5. Tests: protocol frames have no unit test yet — add `protocol_test.go` with
   expected byte arrays from the table above.
6. Commit initial tree once user approves (git is initialized, uncommitted).

## Repo map (what's where)

- `cmd/syska-sonic-cli/main.go` — entry (version ldflag injected)
- `cmd/probe/` — calibration tool (single-session sequences; not shipped)
- `internal/cli/` — cobra tree; `device.go` (get/set/pair/sendraw + `sendFrames`
  dial ladder incl. `ensureClassicLink` bluetoothctl fallback), `discover.go`
  (bg jobs + wait ladder), `profile.go`, `jobs.go` (ledger wait), `feedback.go`,
  `doctor.go`, `confirm.go`, `execute.go` (flags + exit codes + rendering)
- `internal/protocol/protocol.go` — byte builders (the table above)
- `internal/transport/` — `discover_linux.go` (BlueZ D-Bus LE scan),
  `spp_linux.go` (RFCOMM dial via x/sys/unix, channel 2), other-platform stubs
- `internal/schema/` — single source of truth; `schema_test.go` enforces
  vocabulary mechanically (mutating ops must have --dry-run/--yes, listings
  must have --limit/--offset, etc.)
- `internal/agentcontext/` — versioned JSON contract (temporarily agent-context)
- `internal/config/` — profile store + `ResolveTarget` precedence: flag → env
  (SSCLI_PROFILE/SSCLI_MAC) → default profile; resolved profile now fills MAC
- `internal/jobs/` — durable ledger at `~/.local/state/syska-sonic-cli/jobs.json`
  (pid-liveness stale-row repair; flock on unix)
- `internal/output/` — envelope renderer; `--deliver` file/webhook routing
- `internal/bulb/` — parse/validation of properties/enums (brightness 0-100,
  colors, effects)
- `scripts/calibrate.sh` — user-run spaced probes (needs `bin`)
- `docs/re-notes.md` — evolving protocol decode; `docs/agent.md` — machine contract
- `README.md` — user-facing status

## Operating gotchas

- btmon needs sudo (no passwordless sudo for this user; ask before probing).
- `bluetoothctl connect <classic MAC>` before raw dials if "host is down"/
  "busy"; ladder does it automatically on attempt 2.
- Timeouts: `--timeout` governs connect+send envelope; SPP channel pinned to 2
  in `sendFrames`/probe. Reading responses happens on a goroutine; module sends
  nothing today.
- `set color` sends exactly one frame (solid g 0x08, XOR-framed, live-confirmed);
  `set power` rides the brightness frame; `set effect` uses mix subs 1-4.
  Root cause of the long color hunt: A2 frames are XOR-obfuscated
  ([len,key,^key...,checksum]) — jadx renders that writer as plaintext;
  trust the smali, not jadx, for bt/a/b/b.
- Envelope discipline: stdout = ONE envelope (`--json`) or human table; stderr
  = warnings/errors. Exit codes fixed (0/1/2/3/4/5/6/7) — see `agent-context
  --json`.

## Environment (already installed)

- go 1.27 (Arch), bluez (bluetoothctl/btmon), jadx 1.5.6, apktool, Ghidra.
- Local Firecrawl/SearXNG stack exists (start on demand via `firecrawl-stack up`).
- Scratchpad for session artifacts; keep decompiled APKs out of the repo.
