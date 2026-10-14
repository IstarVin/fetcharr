#!/usr/bin/env bash
# Smoke test for a running Fetcharr server: create, list, get, pause, resume,
# cancel and delete a job and check the HTTP status of every step.
#
#   BASE_URL  server root (default http://127.0.0.1:8080)
#   TOKEN     bearer token; leave unset when the server has no auth_token
#   CATEGORY  category id to use (default: first category from /api/info)
#   SMOKE_JSON  force the JSON helper: jq or python3 (default: jq if installed)
#
# Needs bash and curl, plus jq or python3 for JSON parsing. Exits non-zero if
# any step does not return the expected status or value.
set -u

BASE_URL="${BASE_URL:-http://127.0.0.1:8080}"
BASE_URL="${BASE_URL%/}"
TOKEN="${TOKEN:-}"

command -v curl >/dev/null 2>&1 || { echo "curl is required" >&2; exit 2; }
if [ -n "${SMOKE_JSON:-}" ]; then
  JSON="$SMOKE_JSON" # force "jq" or "python3"
elif command -v jq >/dev/null 2>&1; then
  JSON=jq
elif command -v python3 >/dev/null 2>&1; then
  JSON=python3
else
  echo "jq or python3 is required" >&2
  exit 2
fi

tmp="$(mktemp)"
trap 'rm -f "$tmp"' EXIT

fails=0
STATUS=""
BODY=""

# jget <json> <key>: top-level scalar value of an object ("null" when null).
jget() {
  if [ "$JSON" = jq ]; then
    printf '%s' "$1" | jq -r --arg k "$2" '.[$k] | if . == null then "null" else tostring end' 2>/dev/null
  else
    printf '%s' "$1" | python3 -c '
import json, sys
try:
    v = json.load(sys.stdin).get(sys.argv[1])
except Exception:
    sys.exit(0)
print("null" if v is None else (str(v).lower() if isinstance(v, bool) else v))' "$2"
  fi
}

# list_has_id <json> <id>: succeeds when .jobs contains a job with that id.
list_has_id() {
  if [ "$JSON" = jq ]; then
    printf '%s' "$1" | jq -e --arg id "$2" '.jobs | map(.id) | index($id) != null' >/dev/null 2>&1
  else
    printf '%s' "$1" | python3 -c '
import json, sys
d = json.load(sys.stdin)
sys.exit(0 if any(j.get("id") == sys.argv[1] for j in d.get("jobs", [])) else 1)' "$2" 2>/dev/null
  fi
}

# first_category <info json>: id of the first category.
first_category() {
  if [ "$JSON" = jq ]; then
    printf '%s' "$1" | jq -r '.categories[0].id // empty' 2>/dev/null
  else
    printf '%s' "$1" | python3 -c '
import json, sys
c = json.load(sys.stdin).get("categories") or []
print(c[0]["id"] if c else "")' 2>/dev/null
  fi
}

# req <method> <path> [json-body] [token]: sets STATUS and BODY.
req() {
  local method="$1" path="$2" data="${3:-}" token="${4-$TOKEN}"
  local args=(-sS -o "$tmp" -w '%{http_code}' -X "$method" "$BASE_URL$path")
  [ -n "$token" ] && args+=(-H "Authorization: Bearer $token")
  [ -n "$data" ] && args+=(-H 'Content-Type: application/json' --data "$data")
  STATUS="$(curl "${args[@]}" 2>/dev/null)" || STATUS="000"
  BODY="$(cat "$tmp")"
}

pass() { printf 'PASS  %s\n' "$1"; }
fail() { printf 'FAIL  %s (%s)\n' "$1" "$2"; fails=$((fails + 1)); }

# expect <step> <status> [field expected-value]
expect() {
  local step="$1" want="$2" field="${3:-}" value="${4:-}"
  if [ "$STATUS" != "$want" ]; then
    fail "$step" "HTTP $STATUS, want $want: $BODY"
    return 1
  fi
  if [ -n "$field" ]; then
    local got
    got="$(jget "$BODY" "$field")"
    if [ "$got" != "$value" ]; then
      fail "$step" "$field = '$got', want '$value'"
      return 1
    fi
  fi
  pass "$step"
}

echo "smoke test against $BASE_URL ($JSON, auth: $([ -n "$TOKEN" ] && echo token || echo none))"

req GET /api/health "" ""
expect "GET /api/health (no token)" 200 status ok || { echo "server not reachable"; exit 1; }

if [ -n "$TOKEN" ]; then
  req GET /api/jobs "" ""
  expect "GET /api/jobs without token is rejected" 401
  req GET /api/jobs "" "wrong-token"
  expect "GET /api/jobs with wrong token is rejected" 401
fi

req GET /api/info
expect "GET /api/info" 200 || exit 1
category="${CATEGORY:-$(first_category "$BODY")}"
[ -n "$category" ] || { fail "pick category" "no category in /api/info"; exit 1; }

# One body for every layout: fields that do not apply to the category's layout
# are ignored by the server.
unique="$(date +%s%N)"
url="https://smoke.example/media/$unique.mp4"
body="{\"url\":\"$url\",\"category\":\"$category\",\"title\":\"Smoke Test $unique\",\"show_name\":\"Smoke Test\",\"season\":1,\"episode\":1}"

req POST /api/jobs "$body"
expect "POST /api/jobs creates a job" 201 status pending || exit 1
id="$(jget "$BODY" id)"
[ -n "$id" ] && [ "$id" != null ] || { fail "created job has an id" "$BODY"; exit 1; }

req POST /api/jobs "$body"
expect "POST /api/jobs again is a duplicate" 409

req GET "/api/jobs?status=pending,paused&limit=500"
if [ "$STATUS" = 200 ] && list_has_id "$BODY" "$id"; then pass "GET /api/jobs lists the job"; else fail "GET /api/jobs lists the job" "HTTP $STATUS: $BODY"; fi

req GET "/api/jobs/$id"
expect "GET /api/jobs/{id}" 200 id "$id"

req POST "/api/jobs/$id/pause"
expect "pause" 200 status paused

req POST "/api/jobs/$id/pause"
expect "pause again is invalid_state" 409 || true

req POST "/api/jobs/$id/resume"
expect "resume" 200 status pending

req POST "/api/jobs/$id/cancel"
expect "cancel" 200 status cancelled

req DELETE "/api/jobs/$id"
expect "DELETE /api/jobs/{id}" 204

req GET "/api/jobs/$id"
expect "GET deleted job is 404" 404

echo
if [ "$fails" -eq 0 ]; then
  echo "ALL PASS"
  exit 0
fi
echo "$fails step(s) FAILED"
exit 1
