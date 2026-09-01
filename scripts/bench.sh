#!/usr/bin/env bash
# Times 1brc/code/go with hyperfine and writes the raw output to a dated file under 1brc/bench/.
# Correctness runs first: a timing for a binary that fails the byte-compare is not a result.
set -euo pipefail

REPO="$(cd "$(dirname "${BASH_SOURCE[0]}")/../.." && pwd)"
ASSETS="${ASSETS:-/Users/noelruault/Downloads/1brc/1brc-assets}"
BIN="$REPO/1brc/code/go/bin/1brc"
LABEL="${1:-$(cd "$REPO" && git rev-parse --short HEAD)}"
# shellcheck source=lib-provenance.sh
source "$REPO/1brc/scripts/lib-provenance.sh"

FILES="${FILES:-10m 100m}"
RUNS="${RUNS:-5}"
WARMUP="${WARMUP:-1}"

command -v hyperfine >/dev/null || { echo "bench: hyperfine not installed (brew install hyperfine)" >&2; exit 1; }

# Held across the correctness runs too: they are 15-core load, so they must not land inside another measurement's timing any more than this bench's own hyperfine runs may.
measure_lock_acquire "bench.sh $LABEL"
trap measure_lock_release EXIT

cd "$REPO/1brc/code/go"
go build -o bin/1brc .
ASSETS="$ASSETS" bash "$REPO/1brc/scripts/check-correctness.sh"

require_quiet

mkdir -p "$REPO/1brc/bench"
stamp="$(date -u +%Y-%m-%dT%H%M%SZ)"
out="$REPO/1brc/bench/$stamp-$LABEL.txt"

{
  echo "# bench $stamp"
  echo "label:    $LABEL"
  echo "files:    $FILES   runs: $RUNS   warmup: $WARMUP"
  provenance_header "$REPO"
  echo
} > "$out"

for f in $FILES; do
  data="$ASSETS/measurements-$f.txt"
  if [[ ! -f $data ]]; then
    echo "bench: MISSING $data, skipping" | tee -a "$out" >&2
    continue
  fi
  echo "\$ hyperfine --warmup $WARMUP --runs $RUNS '$BIN -in $data > /dev/null'" >> "$out"
  hyperfine --warmup "$WARMUP" --runs "$RUNS" --style basic \
    -n "1brc $f" "$BIN -in $data > /dev/null" 2>&1 | tee -a "$out"
  echo >> "$out"
done

echo "bench: wrote $out"
