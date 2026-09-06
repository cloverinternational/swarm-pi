#!/usr/bin/env bash
# A/B: same developer-role chat request through the control image and the patched image.
set -uo pipefail
IMG="$1"; LABEL="$2"; PORT=4001; ADMIN=ab-admin-key; DATA=/tmp/plexus-ab/data-$LABEL
docker rm -f plexus-ab >/dev/null 2>&1; rm -rf "$DATA"; mkdir -p "$DATA"
docker run -d --name plexus-ab --network host -e ADMIN_KEY=$ADMIN -e ENCRYPTION_KEY=0123456789abcdef0123456789abcdef0123456789abcdef0123456789abcdef \
  -e DATABASE_URL=sqlite:///app/data/plexus.db -e LOG_LEVEL=info -e PORT=$PORT -e DATA_DIR=/app/data -v "$DATA:/app/data" "$IMG" >/dev/null
until curl -s -m 2 -o /dev/null http://127.0.0.1:$PORT/health; do sleep 1; done
H=(-H "X-Admin-Key: $ADMIN" -H "Content-Type: application/json")
curl -s "${H[@]}" -X PUT http://127.0.0.1:$PORT/v0/management/providers/mock -d '{"api_base_url":{"responses":"http://127.0.0.1:4999/v1"},"api_key":"mock","models":["mock-model"]}' | head -c 200; echo
curl -s "${H[@]}" -X PUT http://127.0.0.1:$PORT/v0/management/aliases/luna -d '{"targets":[{"provider":"mock","model":"mock-model"}],"type":"text"}' | head -c 200; echo
curl -s "${H[@]}" -X PUT http://127.0.0.1:$PORT/v0/management/keys/pi-test -d '{"secret":"sk-pi-test","allowedModels":["luna"]}' | head -c 200; echo
docker restart plexus-ab >/dev/null; until curl -s -m 2 -o /dev/null http://127.0.0.1:$PORT/health; do sleep 1; done
curl -s "${H[@]}" http://127.0.0.1:$PORT/v0/management/keys | head -c 200; echo
: > /tmp/plexus-ab/mock-upstream.jsonl
for ROLE in developer system; do
  curl -s -m 30 -H "Authorization: Bearer sk-pi-test" -H "Content-Type: application/json" http://127.0.0.1:$PORT/v1/chat/completions \
    -d "{\"model\":\"luna\",\"stream\":false,\"messages\":[{\"role\":\"$ROLE\",\"content\":\"PROMPT-VIA-$ROLE: Always announce your mode.\"},{\"role\":\"user\",\"content\":\"hi\"}],\"tools\":[{\"type\":\"function\",\"function\":{\"name\":\"Bash\",\"parameters\":{\"type\":\"object\"}}}]}" | head -c 160; echo
done
echo "=== [$LABEL] what reached the mock upstream ==="
python3 - <<'PY'
import json
for line in open("/tmp/plexus-ab/mock-upstream.jsonl"):
    r=json.loads(line); b=r["body"]
    print(r["path"], "| instructions=", repr(b.get("instructions")), "| input roles=", [i.get("role") or i.get("type") for i in b.get("input",[])], "| tools=", len(b.get("tools",[])))
PY
docker logs plexus-ab 2>&1 | grep -E "Dispatching|Selected API|error|Error" | tail -4
docker rm -f plexus-ab >/dev/null 2>&1
