#!/usr/bin/env bash
# LAN exposure gate for the mobile (H5) control-plane surface.
#
# Required by docs/agent-wiki/sources/decisions/control-plane-lan-exposure.md.
# The unit tests in internal/controlapi/lan_exposure_test.go and
# mobile_surface_test.go cover the logic; this script covers the parts that
# need a real listener on a real interface, and the one check that no unit test
# can make: that a rebinding-style Host is refused by a socket a phone can
# actually open.
#
# Usage:
#   scripts/check-lan-exposure.sh <interface>      # e.g. en0
#
# It starts a throwaway Control Service bound to loopback plus that interface's
# IPv4 address, exercises the boundary, and stops it. It performs NO privileged
# action: no helper, no gateway lifecycle, no network reconfiguration.
set -euo pipefail

ROOT="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
IFACE="${1:-}"
PORT="${OPENSURGE_LAN_CHECK_PORT:-61799}"

if [[ -z "$IFACE" ]]; then
  echo "usage: scripts/check-lan-exposure.sh <interface>" >&2
  echo "example: scripts/check-lan-exposure.sh en0" >&2
  exit 2
fi

LAN_IP="$(ipconfig getifaddr "$IFACE" 2>/dev/null || true)"
if [[ -z "$LAN_IP" ]]; then
  echo "FAIL: interface $IFACE has no IPv4 address" >&2
  exit 1
fi

WORK="$(mktemp -d)"
CONTROL_BIN="$WORK/opensurge-control"
STORE="$WORK/store"
mkdir -p "$STORE"
SERVER_PID=""

cleanup() {
  if [[ -n "$SERVER_PID" ]] && kill -0 "$SERVER_PID" 2>/dev/null; then
    kill "$SERVER_PID" 2>/dev/null || true
    wait "$SERVER_PID" 2>/dev/null || true
  fi
  rm -rf "$WORK"
}
trap cleanup EXIT

pass() { printf '  ok   %s\n' "$1"; }
fail() { printf '  FAIL %s\n' "$1" >&2; FAILED=1; }
FAILED=0

echo "building control service"
(cd "$ROOT" && go build -o "$CONTROL_BIN" ./cmd/opensurge-control)

cp "$ROOT/examples/config.example.yaml" "$WORK/config.yaml"

echo "starting control service on 127.0.0.1:$PORT and $LAN_IP:$PORT"
"$CONTROL_BIN" \
  -config "$WORK/config.yaml" \
  -addr "127.0.0.1:$PORT" \
  -store "$STORE" \
  -mobile-interface "$IFACE" \
  >"$WORK/server.log" 2>&1 &
SERVER_PID=$!

for _ in $(seq 1 50); do
  if curl -fsS -o /dev/null "http://127.0.0.1:$PORT/" 2>/dev/null; then break; fi
  sleep 0.2
done
if ! kill -0 "$SERVER_PID" 2>/dev/null; then
  echo "FAIL: control service exited during startup" >&2
  cat "$WORK/server.log" >&2
  exit 1
fi

TOKEN="$(cat "$STORE/control-token")"

status() { curl -sS -o /dev/null -w '%{http_code}' "$@" 2>/dev/null || echo 000; }

echo
echo "1. reachable on the LAN address"
code="$(status "http://$LAN_IP:$PORT/")"
[[ "$code" == "200" ]] && pass "SPA served on $LAN_IP ($code)" || fail "SPA not served on $LAN_IP (got $code)"

echo
echo "2. unauthenticated LAN peer is refused the API"
code="$(status "http://$LAN_IP:$PORT/api/v1/overview")"
[[ "$code" == "401" ]] && pass "unauthenticated /api/v1/overview -> 401" || fail "unauthenticated API returned $code, want 401"

# Documented, accepted outcome: the static bundle is not behind auth. Recorded
# here so it is a known property rather than a surprise bug report.
code="$(status "http://$LAN_IP:$PORT/")"
[[ "$code" == "200" ]] && pass "SPA is intentionally unauthenticated (accepted, see decision record)" || fail "SPA status $code"

echo
echo "3. DNS-rebinding Host is refused on the LAN socket"
for host in evil.example.com attacker.test "evil.example.com:$PORT"; do
  code="$(curl -sS -o /dev/null -w '%{http_code}' -H "Host: $host" "http://$LAN_IP:$PORT/api/v1/overview" 2>/dev/null || echo 000)"
  [[ "$code" == "403" ]] && pass "Host: $host -> 403" || fail "Host: $host returned $code, want 403"
done

echo
echo "4. scanning alone grants nothing; pairing needs the code typed on the Mac"
PAIR_JSON="$(curl -sS -X POST -H "Authorization: Bearer $TOKEN" "http://127.0.0.1:$PORT/api/v1/pairings" 2>/dev/null || true)"
PAIR_URL="$(printf '%s' "$PAIR_JSON" | sed -n 's/.*"url"[[:space:]]*:[[:space:]]*"\([^"]*\)".*/\1/p')"
PAIR_ID="$(printf '%s' "$PAIR_JSON" | sed -n 's/.*"id"[[:space:]]*:[[:space:]]*"\([^"]*\)".*/\1/p')"

if [[ -z "$PAIR_URL" || -z "$PAIR_ID" ]]; then
  fail "no pairing was created (response: $PAIR_JSON)"
else
  case "$PAIR_URL" in
    "http://$LAN_IP:$PORT/pair?p="*) pass "pairing URL is phone-reachable" ;;
    *) fail "pairing URL is not phone-reachable: $PAIR_URL" ;;
  esac

  PHONE="$WORK/phone.txt"
  SCAN_BODY="$(curl -sS -c "$PHONE" "$PAIR_URL" 2>/dev/null || true)"
  CODE="$(printf '%s' "$SCAN_BODY" | sed -n 's/.*class="code">\([0-9]\{6\}\)<.*/\1/p')"
  [[ -n "$CODE" ]] && pass "phone shows a 6-digit pair code" || fail "phone did not receive a pair code"

  # The whole point: the scan by itself must not authenticate.
  code="$(curl -sS -o /dev/null -w '%{http_code}' -b "$PHONE" "http://$LAN_IP:$PORT/api/v1/overview" 2>/dev/null || echo 000)"
  [[ "$code" == "401" ]] && pass "scan alone does NOT authenticate ($code)" || fail "scan alone returned $code, want 401"

  code="$(curl -sS -o /dev/null -w '%{http_code}' -X POST -H "Authorization: Bearer $TOKEN" \
    -H 'Content-Type: application/json' -d '{"code":"000000","name":"wrong"}' \
    "http://127.0.0.1:$PORT/api/v1/pairings/$PAIR_ID/confirm" 2>/dev/null || echo 000)"
  [[ "$code" == "403" ]] && pass "wrong pair code rejected ($code)" || fail "wrong pair code returned $code, want 403"

  curl -sS -o /dev/null -X POST -H "Authorization: Bearer $TOKEN" -H 'Content-Type: application/json' \
    -d "{\"code\":\"$CODE\",\"name\":\"gate-check phone\"}" \
    "http://127.0.0.1:$PORT/api/v1/pairings/$PAIR_ID/confirm" 2>/dev/null || true

  curl -sS -o /dev/null -b "$PHONE" -c "$PHONE" "http://$LAN_IP:$PORT/pair/status?p=$PAIR_ID" 2>/dev/null || true
  grep -q opensurge_device "$PHONE" && pass "phone received a device credential after confirmation" || fail "phone never received a device credential"

  code="$(curl -sS -o /dev/null -w '%{http_code}' -b "$PHONE" "http://$LAN_IP:$PORT/api/v1/overview" 2>/dev/null || echo 000)"
  [[ "$code" == "200" ]] && pass "paired device can read" || fail "paired device read returned $code, want 200"

  for route in /api/v1/gateway/start /api/v1/sources; do
    code="$(curl -sS -o /dev/null -w '%{http_code}' -X POST -b "$PHONE" \
      -H "Origin: http://$LAN_IP:$PORT" -H 'Content-Type: application/json' -d '{}' \
      "http://$LAN_IP:$PORT$route" 2>/dev/null || echo 000)"
    [[ "$code" == "403" ]] && pass "paired device POST $route -> 403" || fail "paired device POST $route returned $code, want 403"
  done

  code="$(curl -sS -o /dev/null -w '%{http_code}' -X POST -b "$PHONE" \
    -H "Origin: http://evil.example.com" -H 'Content-Type: application/json' -d '{}' \
    "http://$LAN_IP:$PORT/api/v1/connectivity/tests" 2>/dev/null || echo 000)"
  [[ "$code" == "403" ]] && pass "foreign Origin refused on an allowed mobile mutation" || fail "foreign Origin returned $code, want 403"

  echo
  echo "5. revocation takes effect immediately"
  DEV_ID="$(curl -sS -H "Authorization: Bearer $TOKEN" "http://127.0.0.1:$PORT/api/v1/paired-devices" 2>/dev/null \
    | sed -n 's/.*"id"[[:space:]]*:[[:space:]]*"\([^"]*\)".*/\1/p' | head -1)"
  if [[ -z "$DEV_ID" ]]; then
    fail "paired device is not listed in the whitelist"
  else
    pass "device appears in the whitelist"
    curl -sS -o /dev/null -X DELETE -H "Authorization: Bearer $TOKEN" \
      "http://127.0.0.1:$PORT/api/v1/paired-devices/$DEV_ID" 2>/dev/null || true
    code="$(curl -sS -o /dev/null -w '%{http_code}' -b "$PHONE" "http://$LAN_IP:$PORT/api/v1/overview" 2>/dev/null || echo 000)"
    [[ "$code" == "401" ]] && pass "revoked device is refused on the next request ($code)" || fail "revoked device returned $code, want 401"
  fi
fi

echo
if [[ "$FAILED" -ne 0 ]]; then
  echo "LAN exposure gate FAILED" >&2
  exit 1
fi

cat <<'NOTE'
LAN exposure gate passed.

Scope of this evidence: it proves the listener, the Host allowlist, the
read-only session boundary and the Origin check, all from this Mac. It does
NOT prove reachability from a second physical device, and it does NOT prove
anything about the gateway data plane. To complete the decision's requirement,
also open the QR link on a real phone on the same Wi-Fi and confirm the
control plane loads and is read-only.
NOTE
