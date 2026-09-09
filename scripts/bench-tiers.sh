#!/usr/bin/env bash
# Re-measures the three go/ tier files in ONE bracketed invocation, unrestricted first and last.
#
# A run taken while a dev server or a test suite is up voids on the two incumbent slots rather than publishing a slow number, so unattended it is worth waiting for the operator to stop:
#   IDLE_MIN=300 QUIET_WAIT=3600 bash scripts/bench-tiers.sh
set -euo pipefail

REPO="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"

exec bash "$REPO/scripts/experiment.sh" \
  -i "${HYPOTHESIS:-go tier files}" \
  -p "${PREDICTION:-sweep: re-measure the three tier files the README tables quote, on their own binaries}" \
  -a 'unrestricted.a=@go/unrestricted/1brc.go' \
  -a 'idiomatic=@go/idiomatic/1brc.go' \
  -a 'portable=@go/portable/1brc.go' \
  -a 'unrestricted.b=@go/unrestricted/1brc.go' \
  "$@"
