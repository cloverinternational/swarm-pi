#!/usr/bin/env bash
set -euo pipefail

script_dir=$(cd -- "$(dirname -- "${BASH_SOURCE[0]}")" && pwd)
tui_root=$(cd -- "$script_dir/.." && pwd)
repo_root=$(git -C "$tui_root" rev-parse --show-toplevel)
release_helper="$repo_root/ci/release/tui.sh"
version_pkg=github.com/Swarm-Code/mono/swarm-sdk/swarm-tui/internal/version

usage() {
  cat <<'EOF'
Usage: ./scripts/release.sh <command>

Commands:
  version   Print the canonical TUI version
  check     Validate the local release contract and working tree
  build     Build and verify a local Linux release candidate
  tag       Create the annotated canonical source tag locally (never pushes)

Publishing is CI-only. Push the reviewed annotated tui/vX.Y.Z tag to trigger
the release workflow after the GitHub App publisher is configured.
EOF
}

version=$("$release_helper" version)
source_tag="tui/$version"

check_tree() {
  [ "$(git -C "$repo_root" branch --show-current)" = "main" ] ||
    { echo "error: production tags must be prepared from main" >&2; exit 1; }
  [ -z "$(git -C "$repo_root" status --porcelain=v1)" ] ||
    { echo "error: working tree must be clean" >&2; exit 1; }
  if git -C "$repo_root" rev-parse -q --verify "refs/tags/$source_tag" >/dev/null; then
    echo "error: immutable tag $source_tag already exists" >&2
    exit 1
  fi
}

build_local() {
  local output="$tui_root/dist/swarmos-linux-amd64"
  local commit build_time build_num build_id ldflags
  commit=$(git -C "$repo_root" rev-parse HEAD)
  build_time=$(git -C "$repo_root" show -s --format=%cI HEAD)
  build_num=$(git -C "$repo_root" rev-list --count HEAD)
  build_id="${commit:0:12}-${build_num}"
  ldflags="-s -w -X ${version_pkg}.Version=${version} -X ${version_pkg}.GitCommit=${commit} -X ${version_pkg}.BuildTime=${build_time} -X ${version_pkg}.BuildID=${build_id} -X ${version_pkg}.BuildNum=${build_num}"

  mkdir -p "$tui_root/dist"
  go -C "$repo_root/swarm-sdk" build -trimpath -tags fts5 -ldflags "$ldflags" \
    -o "$output" ./swarm-tui/cmd/swarmos
  "$output" --version | grep -F "SwarmOS $version"
  "$output" --version | grep -F "Commit: $commit"
  (cd "$(dirname "$output")" && sha256sum "$(basename "$output")" >"$(basename "$output").sha256")
  printf 'built %s\n' "$output"
}

case "${1:-}" in
  version)
    printf '%s\n' "$version"
    ;;
  check)
    "$repo_root/ci/release/tui_test.sh"
    check_tree
    printf 'release contract ready for %s\n' "$source_tag"
    ;;
  build)
    build_local
    ;;
  tag)
    "$repo_root/ci/release/tui_test.sh"
    check_tree
    git -C "$repo_root" tag -a "$source_tag" -m "SwarmOS TUI $version"
    "$release_helper" validate-tag "$source_tag"
    printf 'created local annotated tag %s\n' "$source_tag"
    printf 'review it, then push with: git push origin %s\n' "$source_tag"
    ;;
  help|-h|--help|"")
    usage
    ;;
  *)
    usage >&2
    exit 1
    ;;
esac
