#!/usr/bin/env bash
# Re-fires EVERY arm this study built, one bracketed invocation per mechanism, so another machine can rank them all.
# Killed arms stay in the list on purpose: a verdict is a fact about the machine that took it, and a second machine is where a ranking can invert.
set -euo pipefail

HERE="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
FILE="${FILE:-1b}"
RUNS="${RUNS:-10}"
DRY="${DRY:-0}"
ONLY="${ONLY:-}"

# name | prediction | arm...; the incumbent bracket is appended at both ends automatically.
groups=(
  "fold|E-27 kept the pointer walk, E-37/E-38 added two cursors and E-39 killed four; the four-wide loss is a register-pressure result on arm64 and is exactly the kind of ranking a different register file can invert|slice=-fold slice|hash=-fold hash|both=-fold both|lanes=-fold lanes|lanes4=-fold lanes4"
  "kernel|E-10 killed both batch tokenizers here at +9.8%/+10.4% end to end against a -40.4% microbenchmark; a machine with different vector-to-general cost may rank them differently|batch-swar=-kernel batch-swar|batch-neon=-kernel batch-neon"
  "parse|E-25 kept the word parse and H3 kept branchless over scalar; the branchless win was 11.4% at 1b having LOST 15.2% in its microbenchmark|branchless=-parse branchless|scalar=-parse scalar"
  "table|E-33 measured the runtime map 12.81% FASTER in the 10k regime and E-34 slower on 413 keys; the crossover is the point of this group|split=-table split|quot=-table quot|map=-table map"
  "io|H7 killed mmap at 5.6x here because 16 KiB pages meant 842k serial faults; a machine with larger pages or a different fault path is exactly where this could invert|mmap=-io mmap|mmap-madvise=-io mmap -madvise"
  "nocache|02-baseline measured the page cache LOSING above ~half of RAM (754 ms uncached against 1.126 s cached); on a box whose RAM dwarfs the file this should invert|pagecache=-nocache=false"
  "split|H1 killed the cursor split at 21% slower than static|cursor=-split cursor"
  "fill|E-29/E-31 found fill-ahead and oversubscription are SUBSTITUTES here, worth 0% stacked; a machine with different read latency may make them complements|sync=-fill sync|ahead=-fill ahead"
  "workers|E-17 kept NumCPU()*4/3 (-7.49%) and E-31 measured one-per-core at +14.09%; P-06 wants above-20 swept and its ceiling is perfect packing|w15=-workers 15|w20=-workers 20|w24=-workers 24|w30=-workers 30"
  "buf|E-17 killed 8 MiB at +3.05%; the sweep has never gone DOWNWARD, and a smaller buffer may keep the kernel's copy in cache|b256=-buf 256|b512=-buf 512|b4096=-buf 4096"
  "bits|the table is sized for 413 keys at -bits 17; this is inert for -table map|b14=-bits 14|b16=-bits 16|b18=-bits 18"
  "stacked|the combination worth the most if lanes cuts CPU and oversubscription then collects the idle it exposes|lanes=-fold lanes|lanes-w24=-fold lanes -workers 24|lanes-w28=-fold lanes -workers 28"
)

for g in "${groups[@]}"; do
  IFS='|' read -r -a parts <<< "$g"
  name="${parts[0]}"; pred="${parts[1]}"
  [[ -n $ONLY && $ONLY != "$name" ]] && continue

  args=(-i "lab-suite/$name" -p "$pred" -f "$FILE" -r "$RUNS" -a 'incumbent.a=')
  for ((i = 2; i < ${#parts[@]}; i++)); do args+=(-a "${parts[i]}"); done
  args+=(-a 'incumbent.b=')
  [[ $FILE != 1b ]] && args+=(--mechanism-only)

  if [[ $DRY == 1 ]]; then
    printf 'experiment.sh'; printf ' %q' "${args[@]}"; printf '\n\n'
    continue
  fi
  echo "=== lab-suite: $name ===" >&2
  # One failing group must not abandon the rest: a refused or contaminated invocation is recorded and the suite moves on.
  bash "$HERE/experiment.sh" "${args[@]}" || echo "lab-suite: group '$name' did not complete (see the bench file); continuing" >&2
done
