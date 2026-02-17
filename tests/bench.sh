#!/usr/bin/env bash
set -euo pipefail

cd "$(dirname "$0")/.."

PROF_DIR=$(mktemp -d)
trap 'rm -rf "$PROF_DIR"' EXIT

CPU_PROF="$PROF_DIR/cpu.prof"
MEM_PROF="$PROF_DIR/mem.prof"

if [[ $# -gt 0 ]]; then
  FLAC_FILE="$1"
  TEST_NAME="TestFLACBenchmarkDecodeFile"
  echo "Running FLAC decode benchmark on: $FLAC_FILE (20 iterations)..."
  export BENCH_FLAC_FILE="$FLAC_FILE"
else
  TEST_NAME="TestFLACBenchmarkDecode"
  echo "Running FLAC decode benchmark (20 iterations per format, white noise)..."
fi

echo ""

go test ./tests/ \
  -run "$TEST_NAME" \
  -count=1 \
  -v \
  -cpuprofile "$CPU_PROF" \
  -memprofile "$MEM_PROF" \
  2>&1

echo ""
echo "================================================================================"
echo "CPU Profile (top 20)"
echo "================================================================================"
go tool pprof -top -nodecount=20 "$CPU_PROF"

echo ""
echo "================================================================================"
echo "Memory Profile — alloc_space (top 20)"
echo "================================================================================"
go tool pprof -top -nodecount=20 -alloc_space "$MEM_PROF"

echo ""
echo "================================================================================"
echo "Memory Profile — inuse_space (top 20)"
echo "================================================================================"
go tool pprof -top -nodecount=20 -inuse_space "$MEM_PROF"

DOCS_DIR="docs"

echo ""
echo "================================================================================"
echo "Generating call graph diagrams in $DOCS_DIR/"
echo "================================================================================"
go tool pprof -png -nodecount=20 "$CPU_PROF" > "$DOCS_DIR/decode_cpu.png"
go tool pprof -png -nodecount=20 -alloc_space "$MEM_PROF" > "$DOCS_DIR/decode_alloc.png"
echo "  $DOCS_DIR/decode_cpu.png"
echo "  $DOCS_DIR/decode_alloc.png"
