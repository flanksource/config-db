#!/usr/bin/env bash
set -euo pipefail

usage() {
  cat >&2 <<'EOF'
Usage:
  generate-pprof-hotspots.sh <base-cpu.pprof> <head-cpu.pprof> [out-dir]
  generate-pprof-hotspots.sh <base-cpu.pprof> <head-cpu.pprof> <base-mem.pprof> <head-mem.pprof> [out-dir]

Env:
  TOP  Number of hotspot functions to include (default: 5)
EOF
}

if [[ $# -lt 2 ]]; then
  usage
  exit 1
fi

BASE_CPU="$1"
HEAD_CPU="$2"
TOP="${TOP:-5}"

BASE_MEM=""
HEAD_MEM=""
OUT_DIR="."

if [[ $# -eq 3 ]]; then
  OUT_DIR="$3"
elif [[ $# -ge 4 ]]; then
  BASE_MEM="$3"
  HEAD_MEM="$4"
  OUT_DIR="${5:-.}"
fi

mkdir -p "$OUT_DIR"

escape_regex() {
  printf '%s' "$1" | sed -e 's/[][(){}.^$*+?|\\]/\\&/g'
}

generate_cpu() {
  local diff="$OUT_DIR/pprof-cpu-diff.txt"
  local diff_cum="$OUT_DIR/pprof-cpu-diff-cum.txt"
  local funcs="$OUT_DIR/pprof-cpu-hot-functions.txt"
  local md="$OUT_DIR/pprof-cpu-hotspots.md"
  local lines="$OUT_DIR/pprof-cpu-hot-lines.txt"

  if ! go tool pprof -top -nodecount=100 -diff_base "$BASE_CPU" "$HEAD_CPU" > "$diff" 2>&1; then
    echo "pprof CPU diff command failed" >> "$diff"
  fi

  if ! go tool pprof -top -cum -nodecount=150 -diff_base "$BASE_CPU" "$HEAD_CPU" > "$diff_cum" 2>&1; then
    echo "pprof CPU cumulative diff command failed" >> "$diff_cum"
  fi

  python3 .github/scripts/pprof-hotspots.py \
    --input "$diff_cum" \
    --metric cpu \
    --top "$TOP" \
    --functions-out "$funcs" \
    --markdown-out "$md"

  : > "$lines"
  while IFS= read -r fn; do
    [[ -z "$fn" ]] && continue
    local base_fn="${fn% (inline)}"
    local regex
    regex=$(escape_regex "$base_fn")
    {
      echo "### $fn"
      echo '```text'
      if ! go tool pprof -list "^${regex}$" -diff_base "$BASE_CPU" "$HEAD_CPU" 2>&1; then
        echo "No line-level match for: $fn"
      fi
      echo '```'
      echo
    } >> "$lines"
  done < "$funcs"
}

generate_mem() {
  local diff="$OUT_DIR/pprof-alloc-space-diff.txt"
  local diff_cum="$OUT_DIR/pprof-alloc-space-diff-cum.txt"
  local diff_objs="$OUT_DIR/pprof-alloc-objects-diff.txt"
  local funcs="$OUT_DIR/pprof-mem-hot-functions.txt"
  local md="$OUT_DIR/pprof-mem-hotspots.md"
  local lines="$OUT_DIR/pprof-mem-hot-lines.txt"

  if ! go tool pprof -top -nodecount=120 -sample_index=alloc_space -diff_base "$BASE_MEM" "$HEAD_MEM" > "$diff" 2>&1; then
    echo "pprof alloc_space diff command failed" >> "$diff"
  fi

  if ! go tool pprof -top -cum -nodecount=150 -sample_index=alloc_space -diff_base "$BASE_MEM" "$HEAD_MEM" > "$diff_cum" 2>&1; then
    echo "pprof alloc_space cumulative diff command failed" >> "$diff_cum"
  fi

  if ! go tool pprof -top -nodecount=120 -sample_index=alloc_objects -diff_base "$BASE_MEM" "$HEAD_MEM" > "$diff_objs" 2>&1; then
    echo "pprof alloc_objects diff command failed" >> "$diff_objs"
  fi

  python3 .github/scripts/pprof-hotspots.py \
    --input "$diff_cum" \
    --metric mem \
    --top "$TOP" \
    --functions-out "$funcs" \
    --markdown-out "$md"

  : > "$lines"
  while IFS= read -r fn; do
    [[ -z "$fn" ]] && continue
    local base_fn="${fn% (inline)}"
    local regex
    regex=$(escape_regex "$base_fn")
    {
      echo "### $fn"
      echo '```text'
      if ! go tool pprof -list "^${regex}$" -sample_index=alloc_space -diff_base "$BASE_MEM" "$HEAD_MEM" 2>&1; then
        echo "No line-level match for: $fn"
      fi
      echo '```'
      echo
    } >> "$lines"
  done < "$funcs"
}

generate_cpu

if [[ -n "$BASE_MEM" && -n "$HEAD_MEM" ]]; then
  generate_mem
fi

echo "Generated pprof outputs in $OUT_DIR" >&2
