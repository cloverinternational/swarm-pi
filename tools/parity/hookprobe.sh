#!/bin/bash
# Ad-hoc `swarm -p` (or `pi -p` with PROBE_BIN=pi) run against the scripted
# model in scripted-model.mjs. Extra args are passed to the binary.
#   CMDS="printf x > f && rm f" tools/parity/hookprobe.sh --hooks-verbose
set -u
HERE=$(cd "$(dirname "$0")" && pwd)
WS=${PROBE_WS:-/tmp/pi-swarm-hookprobe}
mkdir -p "$WS" && cd "$WS" && { [ -d .git ] || git init -q; }
export PORT_FILE=${PORT_FILE:-/tmp/pi-swarm-scripted-model.port}
rm -f "$PORT_FILE"
node "$HERE/scripted-model.mjs" 2>"$WS/model.log" &
MP=$!
until [ -s "$PORT_FILE" ]; do sleep 0.1; done
PORT=$(cat "$PORT_FILE")
PARITY_API_KEY=k HOME="$WS/home" swarm --no-update --approval-mode auto --api-type openai \
  --base-url "http://127.0.0.1:$PORT/v1" --api-key-env PARITY_API_KEY -m parity-model \
  --max-tokens 4096 --max-turns 0 --workspace "$WS" --output-format stream-json "$@" -p "PROBE" \
  >"$WS/swarm.out" 2>&1
echo "swarm exit=$?"
kill $MP 2>/dev/null
echo "--- model.log"; cut -c1-700 "$WS/model.log"
