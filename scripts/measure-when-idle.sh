#!/usr/bin/env bash
# experiment.sh for an unattended runner: waits for the operator to stop using the laptop rather than refusing after three minutes.
# QUIET_LOAD=6.0 admits a machine whose cores are busy rendering, which is how a browser at 97% got inside a timed run (E-37).
set -euo pipefail

# --self-test asserts the INTERACTIVE defaults, so it runs without these overrides or it fails on them.
if [[ ${1:-} == --self-test ]]; then exec bash "$(dirname "${BASH_SOURCE[0]}")/experiment.sh" "$@"; fi

export IDLE_MIN="${IDLE_MIN:-600}"
export QUIET_LOAD="${QUIET_LOAD:-4.2}"
export QUIET_WAIT="${QUIET_WAIT:-28800}"
# Never force: a runner that stamps NOT QUIET and carries on produces rows nobody may quote.
export QUIET_FORCE=0

exec bash "$(dirname "${BASH_SOURCE[0]}")/experiment.sh" "$@"
