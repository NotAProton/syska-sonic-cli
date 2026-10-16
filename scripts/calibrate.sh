#!/usr/bin/env bash
# calibrate.sh — single-session probe battery (see cmd/probe).
# The module drops idle SPP sessions after ~12-15s, so do NOT probe with
# per-frame CLI calls; this runs all frames in one session, 1.2s apart.
# Watch the light and note which STEP number changes it.
set -eu
MAC="${1:-${SSCLI_MAC:-}}"
if [ -z "$MAC" ]; then
  echo "usage: calibrate.sh <classic-bt-mac>  (or set SSCLI_MAC)" >&2
  exit 2
fi
cd "$(dirname "$0")/.."
go run ./cmd/probe "$MAC"
