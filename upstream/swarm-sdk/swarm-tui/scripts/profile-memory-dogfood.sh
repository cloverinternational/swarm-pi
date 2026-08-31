#!/bin/bash
# Profile a real TUI/tool/render workload under a hard 100MB Docker limit.
# The TUI is driven exclusively through `swarm attach` and a deterministic,
# local OpenAI-compatible provider; no external model or host config is used.
set -euo pipefail

SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
SWARM_TUI_DIR="$(cd "${SCRIPT_DIR}/.." && pwd)"
MONO_DIR="$(cd "${SWARM_TUI_DIR}/../.." && pwd)"

IMAGE="${IMAGE:-swarm-tui-stress:dogfood}"
CONTAINER="${CONTAINER:-swarm-tui-profile-dogfood-$$}"
CONFIG_VOLUME="${CONFIG_VOLUME:-swarm-tui-profile-config-$$}"
HANDLE="${HANDLE:-profile-tui-$$}"
FAKE_PORT="${FAKE_PORT:-18080}"
PPROF_PORT="${PPROF_PORT:-6070}"
ROUNDS="${ROUNDS:-1}"
PROFILE_SECONDS="${PROFILE_SECONDS:-20}"
WAIT_MS="${WAIT_MS:-30000}"
OUTPUT_DIR="${OUTPUT_DIR:-/tmp/swarm-tui-dogfood-$(date +%Y%m%d-%H%M%S)}"
BUILD_IMAGE="${BUILD_IMAGE:-1}"
FAKE_SERVER_PID=""

cleanup() {
    local status=$?
    if [ "$status" -ne 0 ] && docker inspect "$CONTAINER" >/dev/null 2>&1; then
        docker logs "$CONTAINER" >"${OUTPUT_DIR}/container-failure.log" 2>&1 || true
    fi
    docker rm -f "$CONTAINER" >/dev/null 2>&1 || true
    docker volume rm "$CONFIG_VOLUME" >/dev/null 2>&1 || true
    if [ -n "$FAKE_SERVER_PID" ]; then
        kill "$FAKE_SERVER_PID" >/dev/null 2>&1 || true
        wait "$FAKE_SERVER_PID" 2>/dev/null || true
    fi
    return "$status"
}
trap cleanup EXIT INT TERM

for command_name in docker curl go python3; do
    if ! command -v "$command_name" >/dev/null 2>&1; then
        echo "missing required command: $command_name" >&2
        exit 1
    fi
done

mkdir -p "$OUTPUT_DIR"

if [ "$BUILD_IMAGE" = "1" ]; then
    docker build \
        -f "${SWARM_TUI_DIR}/Dockerfile.stress" \
        -t "$IMAGE" \
        "$MONO_DIR"
fi

python3 "${SCRIPT_DIR}/fake-openai-profile-server.py" \
    --port "$FAKE_PORT" >"${OUTPUT_DIR}/fake-provider.log" 2>&1 &
FAKE_SERVER_PID=$!
for _ in $(seq 1 50); do
    if curl -fsS "http://127.0.0.1:${FAKE_PORT}/v1/models" >/dev/null 2>&1; then
        break
    fi
    sleep 0.1
done
curl -fsS "http://127.0.0.1:${FAKE_PORT}/v1/models" >/dev/null

docker volume create "$CONFIG_VOLUME" >/dev/null
docker run --rm --network host \
    -v "${CONFIG_VOLUME}:/home/swarmuser" \
    "$IMAGE" provider add fake-profile \
    --api-type openai-compatible \
    --base-url "http://127.0.0.1:${FAKE_PORT}/v1" \
    --api-key profile-only \
    --no-fetch
docker run --rm --network host \
    -v "${CONFIG_VOLUME}:/home/swarmuser" \
    "$IMAGE" model add fake-profile fake-profile-model \
    --context-window 200000

docker run -d -t \
    --name "$CONTAINER" \
    --network host \
    --memory=100m \
    --memory-swap=100m \
    --pids-limit=256 \
    -e "SWARMOS_PPROF=${PPROF_PORT}" \
    -e "GOMEMLIMIT=75MiB" \
    -e "GODEBUG=memprofilerate=512" \
    -e "SWARMOS_SKIP_INTRO=1" \
    -e "TERM=xterm-256color" \
    -v "${CONFIG_VOLUME}:/home/swarmuser" \
    -v "${SCRIPT_DIR}/profile-memory-config.yaml:/home/swarmuser/.swarmos/config.yaml:ro" \
    -v "${SCRIPT_DIR}/profile-memory-agent-profiles.json:/home/swarmuser/.swarmos/agent_profiles.json:ro" \
    -v "${MONO_DIR}:/workspace:ro" \
    "$IMAGE" \
    -P fake-profile \
    -m fake-profile-model \
    --a2a-handle "$HANDLE" \
    --workspace /workspace \
    --no-update >/dev/null

for _ in $(seq 1 100); do
    if curl -fsS "http://127.0.0.1:${PPROF_PORT}/debug/pprof/" >/dev/null 2>&1; then
        break
    fi
    sleep 0.1
done
curl -fsS "http://127.0.0.1:${PPROF_PORT}/debug/pprof/" >/dev/null

ATTACH=(docker exec "$CONTAINER" /usr/local/bin/swarm-tui swarm attach "$HANDLE")
for _ in $(seq 1 100); do
    if "${ATTACH[@]}" state >"${OUTPUT_DIR}/state-before.json" 2>/dev/null; then
        break
    fi
    sleep 0.1
done
"${ATTACH[@]}" state >"${OUTPUT_DIR}/state-before.json"
"${ATTACH[@]}" frame >"${OUTPUT_DIR}/frame-before.txt"

docker inspect \
    -f 'memory={{.HostConfig.Memory}} swap={{.HostConfig.MemorySwap}} pids={{.HostConfig.PidsLimit}}' \
    "$CONTAINER" >"${OUTPUT_DIR}/limits.txt"
docker stats --no-stream \
    --format '{{.Name}} cpu={{.CPUPerc}} mem={{.MemUsage}} pct={{.MemPerc}} pids={{.PIDs}}' \
    "$CONTAINER" >"${OUTPUT_DIR}/stats-before.txt"
curl -fsS "http://127.0.0.1:${PPROF_PORT}/debug/pprof/heap?gc=1" \
    -o "${OUTPUT_DIR}/heap-before.pb.gz"
curl -fsS "http://127.0.0.1:${PPROF_PORT}/debug/pprof/allocs" \
    -o "${OUTPUT_DIR}/allocs-before.pb.gz"

curl -fsS "http://127.0.0.1:${PPROF_PORT}/debug/pprof/profile?seconds=${PROFILE_SECONDS}" \
    -o "${OUTPUT_DIR}/cpu-workload.pb.gz" &
CPU_PROFILE_PID=$!

for round in $(seq 1 "$ROUNDS"); do
    "${ATTACH[@]}" text "deterministic profiling workload ${round}" >/dev/null
    "${ATTACH[@]}" key enter >/dev/null
    "${ATTACH[@]}" state >"${OUTPUT_DIR}/state-round-${round}.json"
    "${ATTACH[@]}" wait "FAKE COMPLETE" "$WAIT_MS" >/dev/null
done
wait "$CPU_PROFILE_PID"

"${ATTACH[@]}" frame >"${OUTPUT_DIR}/frame-after.txt"
"${ATTACH[@]}" state >"${OUTPUT_DIR}/state-after.json"
curl -fsS "http://127.0.0.1:${PPROF_PORT}/debug/pprof/heap?gc=1" \
    -o "${OUTPUT_DIR}/heap-after.pb.gz"
curl -fsS "http://127.0.0.1:${PPROF_PORT}/debug/pprof/allocs" \
    -o "${OUTPUT_DIR}/allocs-after.pb.gz"
curl -fsS "http://127.0.0.1:${PPROF_PORT}/debug/pprof/goroutine" \
    -o "${OUTPUT_DIR}/goroutine-after.pb.gz"
docker stats --no-stream \
    --format '{{.Name}} cpu={{.CPUPerc}} mem={{.MemUsage}} pct={{.MemPerc}} pids={{.PIDs}}' \
    "$CONTAINER" >"${OUTPUT_DIR}/stats-after.txt"
docker inspect \
    -f 'running={{.State.Running}} oom={{.State.OOMKilled}} exit={{.State.ExitCode}}' \
    "$CONTAINER" >"${OUTPUT_DIR}/container-after.txt"

go tool pprof -sample_index=inuse_space -top -unit=MB \
    "${OUTPUT_DIR}/heap-after.pb.gz" >"${OUTPUT_DIR}/heap-after-top.txt"
go tool pprof -sample_index=alloc_space -top -unit=MB \
    "${OUTPUT_DIR}/allocs-after.pb.gz" >"${OUTPUT_DIR}/allocs-after-top.txt"
go tool pprof -sample_index=cpu -top \
    "${OUTPUT_DIR}/cpu-workload.pb.gz" >"${OUTPUT_DIR}/cpu-workload-top.txt"

cat "${OUTPUT_DIR}/limits.txt"
cat "${OUTPUT_DIR}/stats-before.txt"
cat "${OUTPUT_DIR}/stats-after.txt"
cat "${OUTPUT_DIR}/container-after.txt"
echo "profiles: ${OUTPUT_DIR}"
