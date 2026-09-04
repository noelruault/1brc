#!/usr/bin/env bash
# The state a measurement belongs to, shared by bench.sh and experiment.sh so the two cannot drift.
# spec.md:42 makes power and load part of every recorded number, and battery numbers are provisional.

# Recording the machine's state is not the same as refusing to measure in a bad one, and on 2026-09-01 that gap voided two attempts in ten minutes: an orphaned invocation overlapping new runs, then an AC-power housekeeping storm at load 12.79 (see 1brc/bench/2026-09-01T17*.txt).
MEASURE_LOCK="${MEASURE_LOCK:-/tmp/1brc-measure.lock}"
# This machine idles at 3.0-4.1 one-minute load with its resident agents (Defender's three daemons, a VM, WindowServer), and the storm that tripled a wall clock hit 12.79, so the line sits between them.
QUIET_LOAD="${QUIET_LOAD:-6.0}"
# The storm subsides on its own, so an unattended loop should wait for it rather than burn its cycle.
QUIET_WAIT="${QUIET_WAIT:-180}"
# A seam, so the self-test can pin the waiting without spending it: every test that set QUIET_WAIT=0 to stay fast left the wait-then-refuse path unpinned, and a mutant that refused immediately survived.
QUIET_SLEEP="${QUIET_SLEEP:-sleep}"

# Seconds since the last keyboard or mouse event. Load average cannot answer "is the operator using this laptop": a browser rendering keeps load high with nobody typing, and a user reading a page keeps load low while every measurement they trigger lands mid-run.
# 0 disables the gate, which is the default so an interactive run is unchanged.
IDLE_MIN="${IDLE_MIN:-0}"
hid_idle() { ioreg -c IOHIDSystem 2>/dev/null | awk '/HIDIdleTime/ { print int($NF / 1000000000); exit }'; }

load1() { sysctl -n vm.loadavg | awk '{print $2}'; }
# over_quiet_load compares in awk because bc is not guaranteed and the rest of this lib already needs awk.
over_quiet_load() { awk -v l="$1" -v t="$QUIET_LOAD" 'BEGIN { exit !(l > t) }'; }

busiest() { ps -Ao pcpu,comm -r | sed -n '2,4p' | awk '{ printf "%s%% %s ", $1, $2 }'; }

# measure_lock_acquire takes the exclusive right to time something on this machine.
# It sets no EXIT trap: both callers already own theirs, and a second trap would silently replace it.
measure_lock_acquire() {
  local who="$1" holder
  while ! mkdir "$MEASURE_LOCK" 2>/dev/null; do
    holder="$(cat "$MEASURE_LOCK/pid" 2>/dev/null || echo '?')"
    if [[ $holder =~ ^[0-9]+$ ]] && kill -0 "$holder" 2>/dev/null; then
      echo "measure: pid $holder is already timing '$(cat "$MEASURE_LOCK/what" 2>/dev/null)'; refusing to overlap it" >&2
      return 3
    fi
    echo "measure: clearing a stale lock from pid $holder (no such process)" >&2
    rm -rf "$MEASURE_LOCK"
  done
  printf '%s\n' "$$" > "$MEASURE_LOCK/pid"
  printf '%s\n' "$who" > "$MEASURE_LOCK/what"
}

# measure_lock_release removes only a lock this process owns.
# MEASURE_LOCK is env-overridable and this is an `rm -rf`, so it refuses anything that is not a directory holding our own pid: a typo'd override must not be able to delete a real directory, and a trap firing late must not delete the next run's lock.
measure_lock_release() {
  [[ -f $MEASURE_LOCK/pid && $(cat "$MEASURE_LOCK/pid" 2>/dev/null) == "$$" ]] || return 0
  rm -rf "$MEASURE_LOCK"
}

# require_quiet waits for the machine to settle and refuses if it does not, unless QUIET_FORCE=1.
# A forced run is not blocked but IS stamped by provenance_header, so the number carries its confound.
require_quiet() {
  local waited=0 l idle why
  while :; do
    l="$(load1)"
    idle="$(hid_idle)"
    why=""
    if over_quiet_load "$l"; then
      why="load $l over $QUIET_LOAD"
    elif ((IDLE_MIN > 0)) && [[ -n $idle ]] && ((idle < IDLE_MIN)); then
      why="operator active ${idle}s ago, wants ${IDLE_MIN}s idle"
    else
      return 0
    fi
    if ((waited >= QUIET_WAIT)); then
      if [[ ${QUIET_FORCE:-0} == 1 ]]; then
        echo "measure: $why after ${waited}s, measuring anyway (QUIET_FORCE=1)" >&2
        return 0
      fi
      echo "measure: $why after ${waited}s of waiting; busiest: $(busiest)" >&2
      echo "measure: refusing — spec.md:42 wants the machine otherwise quiet. Set QUIET_FORCE=1 to measure anyway (the output gets stamped)." >&2
      return 4
    fi
    echo "measure: $why, waiting 15s (${waited}/${QUIET_WAIT}s); busiest: $(busiest)" >&2
    $QUIET_SLEEP 15
    waited=$((waited + 15))
  done
}

provenance_header() {
  local repo="$1" l
  l="$(load1)"
  echo "commit:   $(cd "$repo" && git rev-parse HEAD)$(cd "$repo" && git diff --quiet || echo ' (DIRTY WORKING TREE)')"
  echo "go:       $(go version)"
  echo "machine:  $(sysctl -n machdep.cpu.brand_string), hw.ncpu $(sysctl -n hw.ncpu), $(($(sysctl -n hw.memsize) / 1024 / 1024 / 1024)) GiB, $(sw_vers -productVersion) $(uname -m)"
  echo "hyperfine: $(hyperfine --version)"
  echo "power:    $(pmset -g batt | sed -n '1s/^Now drawing from //p') $(pmset -g batt | sed -n "2s/.*[)]\s*//p")"
  echo "load:     $(uptime)"
  pmset -g batt | grep -q "AC Power" || echo "PROVISIONAL: measured on battery, spec.md:42 requires the headline on AC power"
  if over_quiet_load "$l"; then
    echo "NOT QUIET: one-minute load $l is over $QUIET_LOAD at the start of this measurement; busiest: $(busiest)"
  fi
}
