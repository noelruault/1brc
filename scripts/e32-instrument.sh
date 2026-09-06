#!/usr/bin/env bash
set -uo pipefail
# Was an absolute path to one checkout, which only ran on the machine that wrote it. scripts/ and code/ are siblings in every layout, so anchor there.
REPO="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
ASSETS=/Users/noelruault/Downloads/1brc/1brc-assets
BIN=$REPO/code/go/bin/1brc
IN=$ASSETS/measurements-1b.txt
source "$REPO/scripts/lib-provenance.sh"

measure_lock_acquire "go-opt-round-3-gap instrument pass" || exit 3
trap measure_lock_release EXIT
require_quiet || exit 4
provenance_header "$REPO"
echo "note:     shipped default = -workers 20 -buf 1024 -parse word -fold ptr (no flags passed; defaults read from the binary)"
echo "note:     round-robin P(hases)/C(puprofile), 20 s settle before every timed run, round 1 discarded as the first-touch outlier"
echo

for round in 0 1 2 3; do
  for kind in P C; do
    sleep 20
    if [[ $kind == P ]]; then
      echo "=== round $round -phases ==="
      echo "\$ /usr/bin/time -l $BIN -in \$ASSETS/measurements-1b.txt -phases > /dev/null"
      /usr/bin/time -l "$BIN" -in "$IN" -phases > /dev/null
    else
      echo "=== round $round -cpuprofile ==="
      echo "\$ /usr/bin/time -l $BIN -in \$ASSETS/measurements-1b.txt -cpuprofile /tmp/gap-prof-$round.out > /dev/null"
      /usr/bin/time -l "$BIN" -in "$IN" -cpuprofile /tmp/gap-prof-$round.out > /dev/null
    fi
    echo
  done
done
echo "load at end: $(uptime)"
