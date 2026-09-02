#!/usr/bin/env bash
# Byte-compares 1brc/code/go's output against the committed reference output.
# A spec.md gate command: a variant that fails this is a bug, not a result.
set -euo pipefail

REPO="$(cd "$(dirname "${BASH_SOURCE[0]}")/../.." && pwd)"
ASSETS="${ASSETS:-/Users/noelruault/Downloads/1brc/1brc-assets}"
BIN="$REPO/1brc/code/go/bin/1brc"
# ARM carries an experiment arm's flags so a variant is byte-compared before it is ever timed.
read -r -a ARM_ARGV <<< "${ARM:-}"

# The 10k-station case is not optional: a 413-key table can pass the first case and still be wrong on the second.
CASES=(
  "measurements-10m.txt|expected-10m.out"
  "measurements-10k-stations-10m.txt|expected-10k-stations-10m.out"
)
# CASES_EXTRA appends "<data>|<expected>" pairs. experiment.sh sets it from -f so an arm timed on a file outside the two gate cases is byte-compared on THAT file: E-09 ranks arms per file, and the two 10m cases cannot establish correctness at a scale where the ranking happens.
# It stays out of the default list because the files it names are tens of gigabytes, and every 413-regime arm would pay that read immediately before being timed.
read -r -a EXTRA_CASES <<< "${CASES_EXTRA:-}"
CASES+=(${EXTRA_CASES[@]+"${EXTRA_CASES[@]}"})

cd "$REPO/1brc/code/go"
go build -o bin/1brc .

status=0
for case in "${CASES[@]}"; do
  data="$ASSETS/${case%%|*}"
  want="$REPO/1brc/testdata/${case##*|}"

  if [[ ! -f $data ]]; then
    echo "check-correctness: MISSING $data" >&2
    echo "  regenerate it with the command recorded in 1brc/02-baseline-data.txt" >&2
    status=1
    continue
  fi
  if [[ ! -f $want ]]; then
    echo "check-correctness: MISSING expected output $want" >&2
    status=1
    continue
  fi

  got=$(mktemp)
  trap 'rm -f "$got"' EXIT
  start=$(date +%s)
  "$BIN" -in "$data" ${ARM_ARGV[@]+"${ARM_ARGV[@]}"} > "$got"
  elapsed=$(($(date +%s) - start))

  if cmp -s "$got" "$want"; then
    entries=$(tr -cd '=' < "$got" | wc -c | tr -d ' ')
    echo "check-correctness: OK   ${case%%|*} (${entries} stations, ${elapsed}s)${ARM:+ [$ARM]}"
  else
    echo "check-correctness: FAIL ${case%%|*}${ARM:+ [$ARM]}" >&2
    # Names contain ", " (Washington, D.C.), so never split the line to diff it. Show the first differing byte offset and a window around it instead.
    off=$(cmp "$got" "$want" 2>&1 | sed -n 's/.*differ: char \([0-9]*\).*/\1/p')
    if [[ -n ${off:-} ]]; then
      from=$((off > 60 ? off - 60 : 1))
      echo "  first difference at byte $off" >&2
      echo "  got : ...$(tail -c +$from "$got" | head -c 120)..." >&2
      echo "  want: ...$(tail -c +$from "$want" | head -c 120)..." >&2
    fi
    status=1
  fi
  rm -f "$got"
  trap - EXIT
done

exit $status
