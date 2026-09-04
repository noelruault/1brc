#!/usr/bin/env bash
# experiment.sh for an unattended runner: waits for the operator to stop using the laptop rather than refusing after three minutes.
# QUIET_LOAD=6.0 admits a machine whose cores are busy rendering, which is how a browser at 97% got inside a timed run (E-37).
set -euo pipefail

# --self-test asserts the INTERACTIVE defaults, so it runs without these overrides or it fails on them.
if [[ ${1:-} == --self-test ]]; then exec bash "$(dirname "${BASH_SOURCE[0]}")/experiment.sh" "$@"; fi

# Operator-set 2026-09-04: measure NOW, never block. The idle gate is off and a busy machine is stamped rather than refused, so the bracket reports the confound instead of the run withholding a number.
export IDLE_MIN="${IDLE_MIN:-0}"
export QUIET_LOAD="${QUIET_LOAD:-6.0}"
export QUIET_WAIT="${QUIET_WAIT:-120}"
export QUIET_FORCE="${QUIET_FORCE:-1}"

exec bash "$(dirname "${BASH_SOURCE[0]}")/experiment.sh" "$@"
