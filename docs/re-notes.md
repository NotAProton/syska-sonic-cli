# reverse-engineering notes — Syska Sonic (BT speaker + LED module)

## Verdict (2026-10-16, from jadx of com.syskaled.application + live pairing)

The "Syska Sonic" is an **iBluz-based classic-Bluetooth speaker + light**
(Actions/Bluz module family). Control channel is **classic BT SPP (RFCOMM ch 2)**,
not GATT despite the LE advertisement we saw (`0x180d`, MF id 0 — likely a
companion beacon; iBluz GATT code in the app uses 6666-ish UUIDs but the app
routes via SPP).

- LE address:  `<LE MAC, redacted>` (advertises "Syska Sonic")
- Classic MAC: `<classic MAC, redacted>` ("Syska Sonic LED 1.1") → pair + SPP here
- Audio: A2DP (0x110B) + AVRCP (0x110E); data: SPP (0x1101), RFCOMM **channel 2**

## Wire protocol (from com.syskaled.bt.a.a.* + bt/a/b/*)

Shared RFCOMM stream carries two frame families:

1. **A2 light frames** — written verbatim by static
   `bt.a.b.b.a(dos, b, b2, b3, b4, b5, byte... mid)` in order
   `[b, b2, b3, b4, *mid, b5]`:

| op | class | app call | wire |
|---|---|---|---|
| 0x08 | g | g(cmd=8, R,G,B) | `A2 08 R G [00] B` |
| 0x12 | g | g(18, R,G,B) incl. `(false,true)` ctor | `A2 12 R G [00] B` |
| 0x0B | d | d(11, p1, hi, lo, am) | `A2 0B p1 hi [am] [00] lo` |
| 0x0C | f | f(12, x) | `A2 0C x [00] 00` |
| 0x11 | e | e(17, sub, val, bR, bG, bB) | `A2 11 sub val [bG] [bB] bR` |
| 0x0E | j | j(14, x) | `A2 0E x [00] 00` |
| 0x10 | k | k(16) | `A2 10 00 [00] 00` |
| 0x14 | h | h(20, x, y) | `A2 14 x y [00] 00` |
| 0x15 | l | l(21, b2..b6) | `A2 15 a b [d e] f` |

   Observed usages: `e(11-ish modes 1..4 + val)` = UI mix pages (brightness/hue),
   `f(12,1)` right before mix mode 4; brightness page d(0x0B, ‹p1›, hi, lo, am);
   sleep/wake h/l; tone via c (below).

2. **UART bridge** (module system ops):
   - tone:  `01 FE 00 00 53 9D 10 00 <param LE32> 00 00 00 00` (param 0/1)
   - ascii: `01 FE 00 00 53 89 <len=text+18> 00×9 03 <text> 00`

`0x53` = ASCII 'S'; the byte pair `53 9D/89` corresponds to
`BluzManager.buildKey(4, 0x9D)` (0x80..0xBF acceptance gate → 0x5300+val).

## Implemented in this CLI

- transport: RFCOMM dial (channel 2, retry-on-busy), write, response drain — `internal/transport/spp_linux.go`
- protocol builders for the whole A2 table + UART frames — `internal/protocol/protocol.go`
- CLI: `set color X` (all 4 candidate paths, `warnings` lists what was written),
  `set brightness N` (percent ×10 uint16 guess), `sendraw <hex>` calibration,
  `pair` (bluetoothctl non-interactive route + profile create)

## Calibration queue (next session)

1. Color still open: user confirmed `A2 0B` brightness works (light dims);
   no color from 0x08/0x12 (both 7-byte corrected), gate `A2 0C 01...` +
   mix-4 blue, even in one 2.5s-gapped session (link died at ~12s again —
   module never emits RESP frames; possibly needs iBluz keepalive — check
   `com/actions/ibluz/b/*` img + d.dart timers in decompiled sources).
2. btmon capture (root; password from user) while the vendor app drives the
   bulb from an Android phone → definitive frame map incl. any keepalive,
   then diff against our emission.
3. Then: map effects (j/k), wake/sleep (h/l), tone UART `c(0x9D)`, and the
   ASCII UART bridge `b(str)`; get/read semantics TBD (module is push-only
   so far).
4. Fix `scripts/calibrate.sh` to use the single-session probe flow instead of
   per-frame CLI calls (reconnect churn is flaky).
5. Add `protocol_test.go` pinning the byte tables here.


## APK facts

- Package: `com.syskaled.application` (BTMusicLight is the main screen).
- Radio stack: com.actions.ibluz SDK + com.syskaled.bt glue; classic profiles.

## Update (expert re-decompile, jadx 1.5.6, full call-site trace)

- **All A2 frames are 7 bytes.** Writer `bt.a.b.b.a(dos,b,b2,b3,b4,b5,mid...)`
  emits `[b,b2,b3,b4,*mid,b5]`. Earlier 6-byte builders for 0x0E/0x10/0x14
  were wrong; fixed in `protocol.go` + pinned in `protocol_test.go`.
- **No handshake, keepalive, or ACK.** On SPP connect the app only starts a
  reader whose parser (`bt.a.b.b.a(DataInputStream)`) reads one byte and
  throws — responses are discarded. Sends are fire-and-forget (~100ms apart,
  2s watchdog only resets a stuck local write). The ~12-15s session death is
  a module-side idle timeout: send promptly, keep sessions short.
- **Connect:** SDP UUID 00001101 (fallback: RFCOMM channel 1 via reflection).
  Our channel 2 works (brightness verified live), so SDP resolves to 2 here.
- **Send queue collapses:** `SppCommandManager` keeps only the LAST queued
  packet when busy — slider drags are last-wins, single shots unaffected.
- **Semantics corrected:** 0x0E j(14,x) = lamp mode-switch toggle;
  0x10 k(16) = factory ageing-test step; 0x13 i(19,on,min) = sleep timer;
  0x14 h(20,on,min) = saving-power timer; 0x15 l(21,...) = wake-up mode;
  mix subs: 1=flicker, 2=?, 3=?, 4=gradient (preceded by f(12,1)), 5=candle.
- **0x0B is not plain brightness:** app sends d(11, X, hi, lo, am) where
  hi/lo is a 0..360 angle and X is a gamma-table (128..4096, byte-truncated)
  or cool/warm (255-p) value. CLI `percent*10` mapping is a guess that
  visibly dims but is not the app's encoding — revisit after color lands.
- **Old `calibrate.sh` hex was wrong** (6-byte color frames, rotated mix
  bytes). Replaced by the single-session `cmd/probe` battery.

## BREAKTHROUGH (smali verification): frames are XOR-obfuscated

jadx misrenders the packet send methods. In smali every A2 packet class
(d/e/f/g/h/i/j/k/l) calls the VIRTUAL writer
`bt.a.b.b.a(DataOutputStream, byte, byte, byte...)`, which:
- key = random byte; logical [A2, cmd, p0..p4] XORed with key;
- wire = [len=10, key, A2^key, cmd^key, p0^key..p4^key, checksum],
  checksum = -(sum of first 9 bytes) mod 256.
Logical payloads are in constructor order (e: [sub, val, R, G, B] — the
[G,B,R] "rotation" was a jadx artifact). UART frames stay plaintext.
All earlier plaintext batteries were therefore noise; the observed
"brightness" fades were connect/disconnect artifacts. SDP confirms SPP
service "JL_SPP" on RFCOMM channel 2. Fixed in protocol.go (frame()),
pinned in protocol_test.go (key=0 and key=0x5A vectors).

## Live confirmation (2026-10-16, XOR framing)

Single-session hold test: brightness-off faded the light down/off, solid
BLUE g(0x08) held blue for 6s, restore returned bright white. Solid color
is the control page's g(0x08) — `set color` now sends exactly that one
frame. Mix subs remain effect modes (`set effect`, speed mapping
uncalibrated). `set power` is still unimplemented: no app call site maps
to a plain power on/off yet (candidates: brightness 0/restore, sleep
timer 0x13).

## Power + effects live calibration (2026-10-16)

Via the CLI, all user-confirmed: `set power off` fades out, `power on`
restores (module retains color across the toggle — off→on returned
blue), `set color` holds, effects: rainbow=sub3 (app-hardcoded blue),
flash=sub1, breathing=sub2, wave=gate+sub4 all animate as named. Speed
maps CLI 0-10 -> app slider 0-100 (x10), direction not yet compared
against the app. `get` stays unimplemented by design: the module never
transmits (the app's own response parser discards/throws on any byte).
Send path hardened: dial ladder (bluetoothctl connect + one redial) and
runSet now fails (exit device-error) when zero frames were sent.

## Brightness-zero fix (2026-10-16)

Live finding: the d/brightness frame drives only the white channel — with
blue lit, `brightness 0` left blue fully visible, while `color #000000`
turned the light fully off and a later `brightness 100` came back WHITE.
So `brightness 0` and `power off` now send [SolidColor(0,0,0),
Brightness(0,0,0)]; non-zero brightness remains the single d frame.
Trade-off: after brightness 0, raising brightness yields white (the
prior RGB color is not restored — the CLI is stateless).

## True-off correction (2026-10-16)

Earlier "true off" claims were wrong: brightness-0 and black frames only
dim (white glow remains; brightness drives the white channel, RGB is
independent). The actual off is the lamp switch j(0x0E, 1) — user saw the
light turn off only on that frame; j(0x0E, 0) alone shows nothing but
re-enables output for following frames. `set power off` and
`set brightness 0` now send ModeSwitch(1); non-zero brightness and
`power on` send ModeSwitch(0) first to wake.

Correction to the correction: from a COLOR state, ModeSwitch(1) alone
only dims (first frame appears to exit color mode). The verified true
off sequence is [Brightness(0,0,0), ModeSwitch(1)] — blue -> off in one
session. `set power off` / `set brightness 0` now send that sequence;
repeating `power off` from white stays off.
