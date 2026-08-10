#!/usr/bin/env bash
set -euo pipefail

ROOT="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
OUTPUT_ROOT="${OPENSURGE_RELEASE_TOOLS_ROOT:-$ROOT/runtime/release-tools}"
CACHE_ROOT="$OUTPUT_ROOT/cache"
BIN_ROOT="$OUTPUT_ROOT/bin"
RELEASE_ARCH="${OPENSURGE_RELEASE_ARCH:-$(uname -m)}"
MINIMUM_MACOS="${OPENSURGE_MINIMUM_MACOS:-13.0}"
GO_BIN="${GO_BIN:-$(command -v go || true)}"

[[ -x "$GO_BIN" ]] || { echo "Go toolchain not found; set GO_BIN" >&2; exit 1; }
# The lock file is the only source for runtime component versions, sources,
# architectures, and checksums. opensurge-deps validates it before producing
# shell-quoted assignments for this release-only script.
eval "$(cd "$ROOT" && "$GO_BIN" run ./cmd/opensurge-deps shell --arch "$RELEASE_ARCH")"

if [[ "$(uname -s)" != "Darwin" ]]; then
  echo "GUI release dependencies must be prepared on macOS" >&2
  exit 1
fi

mkdir -p "$CACHE_ROOT" "$BIN_ROOT"
work_dir="$(mktemp -d "${TMPDIR:-/private/tmp}/opensurge-release-deps.XXXXXX")"
trap 'rm -rf "$work_dir"' EXIT

download_and_verify() {
  local url=$1 output=$2 checksum=$3
  if [[ ! -f "$output" ]] || ! printf '%s  %s\n' "$checksum" "$output" | shasum -a 256 --check --status; then
    curl --fail --location --silent --show-error --retry 3 \
      --connect-timeout 15 --max-time 1200 "$url" -o "$output"
  fi
  printf '%s  %s\n' "$checksum" "$output" | shasum -a 256 --check
}

download_and_verify "$DNSMASQ_URL" "$CACHE_ROOT/$DNSMASQ_ARCHIVE" "$DNSMASQ_SHA256"
tar -xzf "$CACHE_ROOT/$DNSMASQ_ARCHIVE" -C "$work_dir"
build_jobs="$(sysctl -n hw.logicalcpu 2>/dev/null || getconf _NPROCESSORS_ONLN 2>/dev/null || echo 1)"
MACOSX_DEPLOYMENT_TARGET="$MINIMUM_MACOS" \
  make -C "$work_dir/dnsmasq-$DNSMASQ_VERSION" -j"$build_jobs" \
    "CC=clang -arch $RELEASE_ARCH"
install -m 0755 "$work_dir/dnsmasq-$DNSMASQ_VERSION/src/dnsmasq" "$BIN_ROOT/dnsmasq"

download_and_verify "$MIHOMO_URL" "$CACHE_ROOT/$MIHOMO_ARCHIVE" "$MIHOMO_SHA256"
gzip -dc "$CACHE_ROOT/$MIHOMO_ARCHIVE" >"$BIN_ROOT/mihomo"
chmod 0755 "$BIN_ROOT/mihomo"

for executable in "$BIN_ROOT/dnsmasq" "$BIN_ROOT/mihomo"; do
  if [[ ! -x "$executable" ]]; then
    echo "release dependency was not prepared: $executable" >&2
    exit 1
  fi
  /usr/bin/lipo "$executable" -verify_arch "$RELEASE_ARCH"
done

if [[ "$(uname -m)" == "$RELEASE_ARCH" ]]; then
  echo "Prepared: $("$BIN_ROOT/dnsmasq" --version | head -1)"
  echo "Prepared: $("$BIN_ROOT/mihomo" -v | head -1)"
else
  echo "Prepared dnsmasq $DNSMASQ_VERSION for $RELEASE_ARCH (cross-compiled)"
  echo "Prepared mihomo $MIHOMO_VERSION for $RELEASE_ARCH from $MIHOMO_ARCHIVE"
fi
echo "Release dependency directory: $BIN_ROOT"
