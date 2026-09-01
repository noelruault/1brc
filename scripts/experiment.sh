#!/usr/bin/env bash
# One autoresearch experiment: N arms of the same binary, correctness-gated, timed in ONE hyperfine invocation, written to a dated file under 1brc/bench/ with a pre-filled ledger row.
# One invocation is necessary and not sufficient (E-16, CORRECTIONS.md C6): without the cooldown below, eight IDENTICAL arms rank monotonically over 21%, so an arm's slot decides its verdict.
# The rules it refuses to let you break are in 07-experiment-ledger.md's header.
set -euo pipefail

REPO="$(cd "$(dirname "${BASH_SOURCE[0]}")/../.." && pwd)"
ASSETS="${ASSETS:-/Users/noelruault/Downloads/1brc/1brc-assets}"
BIN="$REPO/1brc/code/go/bin/1brc"
# shellcheck source=lib-provenance.sh
source "$REPO/1brc/scripts/lib-provenance.sh"

die() { echo "experiment: $*" >&2; exit 2; }

usage() {
  cat <<'USAGE'
usage: experiment.sh -i <hypothesis-id> -p <prediction> -a <name>=<flags> -a <name>=<flags> [...]
                     [-f 1b|100m|10m|10k-stations-10m] [-r runs] [-w warmup] [--mechanism-only]

  -i  hypothesis id, e.g. H-14 or "queue item 6". Names the row this run will fill.
  -p  the prediction, made BEFORE the run, and it must contain a number.
      A sweep with no hypothesis is allowed only as -p 'sweep: <why>', which labels the row honestly.
  -a  one arm, name=flags. At least two: a delta needs something to be a delta from.
      The first arm should be the incumbent, because a percentage is relative to the arm being replaced.
  -f  measurement file, default 1b. Anything else needs --mechanism-only (E-09).
  -c  cooldown seconds slept before every timed run, default 20 at 1b and 0 elsewhere.
      E-16: without it, eight IDENTICAL arms rise monotonically by 21% and the arm named
      first wins whatever it is. 0 is allowed and stamps the file NOT SLOT-CORRECTED.

  --self-test  run the argument and invariant checks and exit; touches no data file.

example:
  experiment.sh -i 'queue item 6' -p 'oversubscription hides read stalls, 3-8%' \
    -a 'workers15=-workers 15' -a 'workers20=-workers 20' -a 'workers30=-workers 30'
USAGE
}

parse_args() {
  HYP=""; PRED=""; FILE="1b"; RUNS="${RUNS:-5}"; WARMUP="${WARMUP:-1}"; MECHANISM_ONLY=0
  COOLDOWN=""
  ARM_NAMES=(); ARM_FLAGS=()
  while (($#)); do
    case "$1" in
      -i) [[ $# -ge 2 ]] || die "-i needs a value"; HYP="$2"; shift 2 ;;
      -p) [[ $# -ge 2 ]] || die "-p needs a value"; PRED="$2"; shift 2 ;;
      -f) [[ $# -ge 2 ]] || die "-f needs a value"; FILE="$2"; shift 2 ;;
      -r) [[ $# -ge 2 ]] || die "-r needs a value"; RUNS="$2"; shift 2 ;;
      -w) [[ $# -ge 2 ]] || die "-w needs a value"; WARMUP="$2"; shift 2 ;;
      -c) [[ $# -ge 2 ]] || die "-c needs a value"; COOLDOWN="$2"; shift 2 ;;
      -a)
        [[ $# -ge 2 ]] || die "-a needs a value"
        [[ $2 == *=* ]] || die "arm must be name=flags, got: $2"
        [[ ${2%%=*} != "" ]] || die "arm needs a name, got: $2"
        ARM_NAMES+=("${2%%=*}"); ARM_FLAGS+=("${2#*=}"); shift 2 ;;
      --mechanism-only) MECHANISM_ONLY=1; shift ;;
      --help) usage; exit 0 ;;
      *) die "unknown argument: $1" ;;
    esac
  done
}

# validate enforces the four rules this study paid for; each rejection names the row that taught it.
validate() {
  [[ -n $HYP ]] || die "no hypothesis id (-i). A run with no hypothesis has no row to fill."
  # One rule, not two: a separate -z check for the prediction masked this one and neither was pinned.
  if [[ $PRED != sweep:* && $PRED != *[0-9]* ]]; then
    die "prediction ('$PRED') must carry a number (-p); 07-method-what-worked.md: a run that only measures produces a number nobody can interpret. A knob-turn with no mechanism is -p 'sweep: <why>' (E-06)."
  fi
  ((${#ARM_NAMES[@]} >= 2)) || die "need at least two arms (-a); one arm is a bench, use bench.sh."
  local i j
  for ((i = 0; i < ${#ARM_NAMES[@]}; i++)); do
    for ((j = i + 1; j < ${#ARM_NAMES[@]}; j++)); do
      [[ ${ARM_NAMES[i]} != "${ARM_NAMES[j]}" ]] || die "duplicate arm name: ${ARM_NAMES[i]}"
    done
  done
  if [[ $FILE != 1b && $MECHANISM_ONLY -eq 0 ]]; then
    die "E-09: seven arms, seven disagreements between $FILE and 1b. Pass --mechanism-only to record a non-verdict."
  fi
  [[ -n $COOLDOWN ]] || { [[ $FILE == 1b ]] && COOLDOWN=20 || COOLDOWN=0; }
  [[ $COOLDOWN =~ ^[0-9]+$ ]] || die "cooldown ('$COOLDOWN') must be a whole number of seconds (-c); E-16: it is what stops an arm's slot deciding its rank."
}

# arm_command builds the timed command; the correctness check runs the same ARM_FLAGS entry, so a flag can never be measured without having been checked.
arm_command() { echo "$BIN -in $DATA ${ARM_FLAGS[$1]} > /dev/null"; }

run() {
  DATA="$ASSETS/measurements-$FILE.txt"
  [[ -f $DATA ]] || die "missing $DATA (regenerate with the command in 02-baseline-data.txt)"
  command -v hyperfine >/dev/null || die "hyperfine not installed (brew install hyperfine)"

  # Taken before the correctness gate, not after: those runs are themselves 15-core load, so holding the lock across them is what stops one experiment's checks landing inside another's timing.
  measure_lock_acquire "experiment.sh $HYP"
  # Installed with the lock, not with $md below, so an arm failing the correctness gate still releases it.
  trap 'rm -f "$md"; measure_lock_release' EXIT

  cd "$REPO/1brc/code/go"
  go build -o bin/1brc .

  local i
  for ((i = 0; i < ${#ARM_NAMES[@]}; i++)); do
    echo "experiment: correctness gate for arm '${ARM_NAMES[i]}' (${ARM_FLAGS[i]:-no flags})"
    ARM="${ARM_FLAGS[i]}" ASSETS="$ASSETS" bash "$REPO/1brc/scripts/check-correctness.sh" \
      || die "arm '${ARM_NAMES[i]}' fails the byte-compare. spec.md: that is a bug, not a result."
  done

  mkdir -p "$REPO/1brc/bench"
  local stamp out slug
  stamp="$(date -u +%Y-%m-%dT%H%M%SZ)"
  slug="$(echo "$HYP" | tr -cs '[:alnum:]' '-' | sed 's/^-//;s/-$//')"
  out="$REPO/1brc/bench/$stamp-$slug.txt"
  md="$(mktemp)"
  require_quiet

  {
    echo "# experiment $stamp"
    echo "hypothesis: $HYP"
    echo "prediction: $PRED"
    echo "file:     measurements-$FILE.txt   runs: $RUNS   warmup: $WARMUP   cooldown: ${COOLDOWN}s"
    [[ $FILE == 1b ]] || echo "NOT A VERDICT: --mechanism-only on the $FILE file, E-09 forbids ranking arms here"
    ((COOLDOWN)) || echo "NOT SLOT-CORRECTED: cooldown 0, so E-16's monotonic drift is in these numbers and the first arm is flattered"
    provenance_header "$REPO"
    echo
  } > "$out"

  local -a args=(--warmup "$WARMUP" --runs "$RUNS" --style basic --export-markdown "$md")
  if ((COOLDOWN)); then args+=(--prepare "sleep $COOLDOWN"); fi
  for ((i = 0; i < ${#ARM_NAMES[@]}; i++)); do
    echo "\$ ${ARM_NAMES[i]}: $(arm_command "$i")" >> "$out"
    args+=(-n "${ARM_NAMES[i]}" "$(arm_command "$i")")
  done
  echo >> "$out"

  hyperfine "${args[@]}" 2>&1 | tee -a "$out"

  {
    echo
    echo "== ledger row skeleton, paste into 07-experiment-ledger.md and write the verdict =="
    echo
    echo "### E-NN — $HYP"
    echo
    echo "- **Idea:** "
    echo "- **Prediction:** $PRED"
    echo "- **Measured, invocation $stamp ($FILE, $RUNS runs, one hyperfine invocation):**"
    echo
    sed 's/^/  /' "$md"
    echo
    echo "- **Verdict:** "
  } >> "$out"

  echo "experiment: wrote $out"
}

self_test() {
  local fails=0
  # expect <want-status> <description> <args...>
  expect() {
    local want="$1" desc="$2"; shift 2
    local got=0
    ( parse_args "$@" && validate ) >/dev/null 2>&1 || got=$?
    if [[ $got -ne $want ]]; then echo "FAIL (want $want, got $got): $desc" >&2; fails=$((fails + 1));
    else echo "ok: $desc"; fi
  }
  # expect_msg also pins WHICH rule rejected, so a sibling guard cannot stand in for the one under test.
  expect_msg() {
    local want="$1" substr="$2" desc="$3"; shift 3
    local got=0 out
    out="$( ( parse_args "$@" && validate ) 2>&1 )" || got=$?
    if [[ $got -ne $want || $out != *"$substr"* ]]; then
      echo "FAIL (want $want/'$substr', got $got/'$out'): $desc" >&2; fails=$((fails + 1))
    else echo "ok: $desc"; fi
  }
  local two=(-a 'a=-workers 15' -a 'b=-workers 20')

  expect 0 "a hypothesis, a numeric prediction and two arms is a valid experiment" \
    -i H-99 -p 'wins by 5%' "${two[@]}"
  expect 2 "no hypothesis id is refused" -p 'wins by 5%' "${two[@]}"
  # These two assert the MESSAGE, not only the status: a sibling -z guard here passed its own mutation while masking this rule, so status alone pinned neither.
  expect_msg 2 'must carry a number' "no prediction is refused, by the numeric rule" -i H-99 "${two[@]}"
  expect_msg 2 'must carry a number' "a prediction with no number is refused" \
    -i H-99 -p 'it should be faster' "${two[@]}"
  expect 0 "a labelled sweep may carry no number" -i H-99 -p 'sweep: no mechanism, just a knob' "${two[@]}"
  expect 2 "one arm is refused: a delta needs something to be a delta from" \
    -i H-99 -p 'wins by 5%' -a 'a=-workers 15'
  expect 2 "duplicate arm names are refused" -i H-99 -p 'wins by 5%' -a 'a=-workers 15' -a 'a=-workers 20'
  expect 2 "an arm without a name is refused" -i H-99 -p 'wins by 5%' -a '=-workers 15' -a 'b=-workers 20'
  expect 2 "an arm that is not name=flags is refused" -i H-99 -p 'wins by 5%' -a 'workers 15' -a 'b=-workers 20'
  expect 2 "E-09: the 100m file is refused as a verdict" -i H-99 -p 'wins by 5%' -f 100m "${two[@]}"
  expect 0 "the 100m file is allowed once labelled a non-verdict" \
    -i H-99 -p 'wins by 5%' -f 100m --mechanism-only "${two[@]}"
  expect 2 "an unknown flag is refused rather than ignored" -i H-99 -p 'wins by 5%' -z "${two[@]}"
  expect_msg 2 'must be a whole number' "a non-numeric cooldown is refused" \
    -i H-99 -p 'wins by 5%' -c later "${two[@]}"
  expect 0 "cooldown 0 is allowed, and stamps the file NOT SLOT-CORRECTED" \
    -i H-99 -p 'wins by 5%' -c 0 "${two[@]}"

  # E-16's default is the rule this script exists to enforce, so assert the value, not just that it parses.
  parse_args -i H-99 -p 'wins by 5%' "${two[@]}"; validate
  if [[ $COOLDOWN != 20 ]]; then
    echo "FAIL: a 1b run defaulted to cooldown '$COOLDOWN', not 20 (E-16)" >&2; fails=$((fails + 1))
  else echo "ok: a 1b run defaults to a 20 s cooldown"; fi
  parse_args -i H-99 -p 'wins by 5%' -f 100m --mechanism-only "${two[@]}"; validate
  if [[ $COOLDOWN != 0 ]]; then
    echo "FAIL: a non-1b run defaulted to cooldown '$COOLDOWN', not 0" >&2; fails=$((fails + 1))
  else echo "ok: a non-1b run defaults to no cooldown"; fi

  # The flags reaching hyperfine must be the flags the correctness gate checked, verbatim.
  parse_args -i H-99 -p 'wins by 5%' -a 'incumbent=' -a 'oversubscribed=-workers 30'
  DATA=/tmp/measurements-1b.txt
  local cmd; cmd="$(arm_command 1)"
  if [[ $cmd != *"-workers 30"* || $cmd != *"$DATA"* ]]; then
    echo "FAIL: arm_command dropped the arm's flags or the data file: $cmd" >&2; fails=$((fails + 1))
  else echo "ok: the timed command carries the arm's flags and the measured file"; fi
  if [[ ${ARM_FLAGS[0]} != "" || ${ARM_NAMES[0]} != "incumbent" ]]; then
    echo "FAIL: an incumbent arm with no flags did not parse" >&2; fails=$((fails + 1))
  else echo "ok: an incumbent arm with no flags parses"; fi

  # The preflight, which exists because recording the machine's state did not stop two void attempts.
  # QUIET_WAIT=0 so the refusal path never sleeps here; QUIET_LOAD=0 and =999 stand in for a busy and an idle machine.
  local out got
  MEASURE_LOCK="$(mktemp -d)/lock"

  measure_lock_acquire "the self-test" || { echo "FAIL: a free lock was not acquired" >&2; fails=$((fails + 1)); }
  if [[ -f $MEASURE_LOCK/pid && $(cat "$MEASURE_LOCK/pid") == "$$" ]]; then echo "ok: acquiring the measurement lock records the holding pid"
  else echo "FAIL: the lock did not record this pid" >&2; fails=$((fails + 1)); fi

  got=0; out="$(measure_lock_acquire "a second measurement" 2>&1)" || got=$?
  if [[ $got -eq 3 && $out == *"already timing"* ]]; then echo "ok: a live holder makes a second measurement refuse rather than overlap"
  else echo "FAIL (want 3/'already timing', got $got/'$out'): the lock let a second measurement in" >&2; fails=$((fails + 1)); fi

  # A crashed cycle leaves its lock behind, and refusing forever on a dead pid would park the loop for good.
  printf '%s\n' 99999999 > "$MEASURE_LOCK/pid"
  got=0; out="$(measure_lock_acquire "after a crash" 2>&1)" || got=$?
  if [[ $got -eq 0 && $out == *"stale lock"* ]]; then echo "ok: a lock held by a dead pid is cleared, not obeyed"
  else echo "FAIL (want 0/'stale lock', got $got/'$out'): a stale lock was not cleared" >&2; fails=$((fails + 1)); fi
  measure_lock_release
  if [[ ! -d $MEASURE_LOCK ]]; then echo "ok: releasing the lock removes it"
  else echo "FAIL: the lock survived its release" >&2; fails=$((fails + 1)); fi

  got=0; out="$(QUIET_LOAD=0 QUIET_WAIT=0 require_quiet 2>&1)" || got=$?
  if [[ $got -eq 4 && $out == *"refusing"* ]]; then echo "ok: a machine over the load line makes the measurement refuse"
  else echo "FAIL (want 4/'refusing', got $got/'$out'): a busy machine was measured anyway" >&2; fails=$((fails + 1)); fi

  got=0; out="$(QUIET_LOAD=0 QUIET_WAIT=0 QUIET_FORCE=1 require_quiet 2>&1)" || got=$?
  if [[ $got -eq 0 && $out == *"measuring anyway"* ]]; then echo "ok: QUIET_FORCE=1 measures a busy machine, loudly"
  else echo "FAIL (want 0/'measuring anyway', got $got/'$out'): the force override does not work" >&2; fails=$((fails + 1)); fi

  got=0; out="$(QUIET_LOAD=999 QUIET_WAIT=0 require_quiet 2>&1)" || got=$?
  if [[ $got -eq 0 && -z $out ]]; then echo "ok: a quiet machine passes the preflight silently"
  else echo "FAIL (want 0/silence, got $got/'$out'): the preflight fired on a quiet machine" >&2; fails=$((fails + 1)); fi

  # Waiting out the storm rather than refusing on it is the whole point of the wait, so it is pinned through the sleep seam: 30 s of budget is two 15 s waits and then a refusal.
  got=0; out="$(QUIET_LOAD=0 QUIET_WAIT=30 QUIET_SLEEP='echo slept' require_quiet 2>&1)" || got=$?
  if [[ $got -eq 4 && $(grep -c slept <<<"$out") -eq 2 ]]; then echo "ok: a busy machine is waited out twice before the measurement refuses"
  else echo "FAIL (want 4 after 2 waits, got $got after $(grep -c slept <<<"$out")): the wait budget is not spent before refusing" >&2; fails=$((fails + 1)); fi

  # The two defaults are pinned for the reason the cooldown default is: the self-test overrides both, so a mutant that moves them disables the gate in production and nothing else would notice.
  if [[ $QUIET_LOAD == 6.0 && $QUIET_WAIT == 180 ]]; then echo "ok: the preflight defaults to refusing over load 6.0 after waiting 180 s"
  else echo "FAIL: the preflight defaults moved to load '$QUIET_LOAD' / wait '$QUIET_WAIT' s" >&2; fails=$((fails + 1)); fi

  # The stamp is what makes a forced number auditable later, so it is pinned on the header, not only on the refusal.
  if [[ $(QUIET_LOAD=0 provenance_header "$REPO") == *"NOT QUIET"* ]]; then echo "ok: a header taken over the load line stamps NOT QUIET"
  else echo "FAIL: provenance_header did not stamp a busy machine" >&2; fails=$((fails + 1)); fi
  if [[ $(QUIET_LOAD=999 provenance_header "$REPO") != *"NOT QUIET"* ]]; then echo "ok: a header taken on a quiet machine carries no stamp"
  else echo "FAIL: provenance_header stamped a quiet machine" >&2; fails=$((fails + 1)); fi

  if ((fails)); then echo "experiment --self-test: $fails FAILED" >&2; return 1; fi
  echo "experiment --self-test: all checks passed"
}

if [[ ${1:-} == --self-test ]]; then self_test; exit; fi
if [[ $# -eq 0 ]]; then usage; exit 2; fi
parse_args "$@"
validate
run
