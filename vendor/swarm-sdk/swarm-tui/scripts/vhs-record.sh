#!/usr/bin/env bash
set -euo pipefail

ROOT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
cd "$ROOT_DIR"

VHS_BIN="${VHS_BIN:-vhs}"
TAPE_FILE="${1:-manual_tests/vhs/lineage_fixture.tape}"
OUTPUT_DIR="manual_tests/vhs/output"

if ! command -v "$VHS_BIN" >/dev/null 2>&1; then
	echo "error: '$VHS_BIN' is not installed"
	echo "install: brew install vhs"
	exit 1
fi

if [[ ! -f "$TAPE_FILE" ]]; then
	echo "error: tape not found: $TAPE_FILE"
	exit 1
fi

mkdir -p "$OUTPUT_DIR"

if [[ ! -x "./swarm" ]]; then
	echo "swarm binary not found; building..."
	make build
fi

echo "running VHS tape: $TAPE_FILE"
"$VHS_BIN" "$TAPE_FILE"
echo "done: visual artifacts written to $OUTPUT_DIR"
