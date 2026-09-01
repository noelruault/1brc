#!/usr/bin/env bash
# Times 1brc/code/go with hyperfine and writes the raw output to a dated file under 1brc/bench/.
# Correctness runs first: a timing for a binary that fails the byte-compare is not a result.
set -euo pipefail

REPO="$(cd "$(dirname "${BASH_SOURCE[0]}")/../.." && pwd)"
ASSETS="${ASSETS:-/Users/noelruault/Downloads/1brc/1brc-assets}"
BIN="$REPO/1brc/code/go/bin/1brc"
LABEL="${1:-$(cd "$REPO" && git rev-parse --short HEAD)}"

FILES="${FILES:-10m 100m}"
RUNS="${RUNS:-5}"
WARMUP="${WARMUP:-1}"

command -v hyperfine >/dev/null || { echo "bench: hyperfine not installed (brew install hyperfine)" >&2; exit 1; }

cd "$REPO/1brc/code/go"
go build -o bin/1brc .
ASSETS="$ASSETS" bash "$REPO/1brc/scripts/check-correctness.sh"

mkdir -p "$REPO/1brc/bench"
stamp="$(date -u +%Y-%m-%dT%H%M%SZ)"
out="$REPO/1brc/bench/$stamp-$LABEL.txt"

# The state the numbers belong to. A timing whose binary cannot be identified is not re-derivable.
{
  echo "# bench $stamp"
  echo "label:    $LABEL"
  echo "commit:   $(cd "$REPO" && git rev-parse HEAD)$(cd "$REPO" && git diff --quiet || echo ' (DIRTY WORKING TREE)')"
  echo "go:       $(go version)"
  echo "machine:  $(sysctl -n machdep.cpu.brand_string), hw.ncpu $(sysctl -n hw.ncpu), $(($(sysctl -n hw.memsize) / 1024 / 1024 / 1024)) GiB, $(sw_vers -productVersion) $(uname -m)"
  echo "hyperfine: $(hyperfine --version)"
  echo "files:    $FILES   runs: $RUNS   warmup: $WARMUP"
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
