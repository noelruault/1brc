#!/usr/bin/env bash
# The state a measurement belongs to, shared by bench.sh and experiment.sh so the two cannot drift.
# spec.md:42 makes power and load part of every recorded number, and battery numbers are provisional.

provenance_header() {
  local repo="$1"
  echo "commit:   $(cd "$repo" && git rev-parse HEAD)$(cd "$repo" && git diff --quiet || echo ' (DIRTY WORKING TREE)')"
  echo "go:       $(go version)"
  echo "machine:  $(sysctl -n machdep.cpu.brand_string), hw.ncpu $(sysctl -n hw.ncpu), $(($(sysctl -n hw.memsize) / 1024 / 1024 / 1024)) GiB, $(sw_vers -productVersion) $(uname -m)"
  echo "hyperfine: $(hyperfine --version)"
  echo "power:    $(pmset -g batt | sed -n '1s/^Now drawing from //p') $(pmset -g batt | sed -n "2s/.*[)]\s*//p")"
  echo "load:     $(uptime)"
  pmset -g batt | grep -q "AC Power" || echo "PROVISIONAL: measured on battery, spec.md:42 requires the headline on AC power"
}
