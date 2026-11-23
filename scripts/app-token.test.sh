#!/usr/bin/env bash
# Tests scripts/app-token.sh against a fake curl: the body it sends for each
# action mode, and that a review refuses a token that can write contents.
# Run: scripts/app-token.test.sh (CI runs it in the test job).
set -euo pipefail

SCRIPT="$(cd "$(dirname "$0")" && pwd)/app-token.sh"
WORK=$(mktemp -d)
trap 'rm -rf "$WORK"' EXIT

mkdir "$WORK/bin"
# Fake curl: answers the OIDC request with a token, and the exchange with
# $FAKE_EXCHANGE, logging the exchange's --data argument.
cat > "$WORK/bin/curl" <<'FAKE'
#!/usr/bin/env bash
data="<none>"
url=""
while [ $# -gt 0 ]; do
  case "$1" in
    --data) data="$2"; shift ;;
    -H|-X) shift ;;
    -*) ;;
    *) url="$1" ;;
  esac
  shift
done
case "$url" in
  *audience=codecanary) echo '{"value":"oidc-jwt"}' ;;
  https://oidc.codecanary.sh/token) echo "$data" > "$CURL_LOG"; echo "$FAKE_EXCHANGE" ;;
  *) echo "unexpected curl url: $url" >&2; exit 1 ;;
esac
FAKE
chmod +x "$WORK/bin/curl"

failures=0
# run MODE EXCHANGE_RESPONSE -> sets status, body, out, token
run() {
  : > "$WORK/out"; : > "$WORK/curl.log"
  set +e
  out=$(PATH="$WORK/bin:$PATH" MODE="$1" FAKE_EXCHANGE="$2" CURL_LOG="$WORK/curl.log" \
    GITHUB_OUTPUT="$WORK/out" ACTIONS_ID_TOKEN_REQUEST_URL="https://actions.example/id?x=1" \
    ACTIONS_ID_TOKEN_REQUEST_TOKEN=req "$SCRIPT" 2>&1)
  status=$?
  set -e
  body=$(cat "$WORK/curl.log")
  token=$(sed -n 's/^token=//p' "$WORK/out")
}
check() { # name condition...
  local name=$1; shift
  if "$@"; then echo "ok   $name"; else echo "FAIL $name"; echo "     status=$status body=$body token=$token"; echo "$out" | sed 's/^/     | /'; failures=$((failures+1)); fi
}

REDUCED='{"token":"ghs_r","permissions":{"contents":"read","pull_requests":"write","statuses":"write","metadata":"read"}}'
FULL='{"token":"ghs_f","permissions":{"contents":"write","pull_requests":"write","statuses":"write","metadata":"read"}}'
OLD_WORKER='{"token":"ghs_old"}'

run review "$REDUCED"
check "review sends mode review and outputs the token" \
  test "$status" = 0 -a "$body" = '{"mode":"review"}' -a "$token" = ghs_r

run "" "$REDUCED"
check "an empty mode is a review" test "$status" = 0 -a "$body" = '{"mode":"review"}'

run fix "$FULL"
check "fix sends mode fix and accepts contents: write" \
  test "$status" = 0 -a "$body" = '{"mode":"fix"}' -a "$token" = ghs_f

run review "$FULL"
check "review fails on a token that can write contents" \
  test "$status" != 0 -a -z "$token"
check "  and says why" grep -q "contents: write" <<<"$out"

run review "$OLD_WORKER"
check "review fails against a worker that reports no permissions" \
  test "$status" != 0 -a -z "$token"

run review '{"error":"Request body is not valid JSON"}'
check "a worker error fails with its message" \
  bash -c '[ "$0" != 0 ] && grep -q "not valid JSON" <<<"$1"' "$status" "$out"

if [ "$failures" -gt 0 ]; then
  echo "$failures failure(s)"
  exit 1
fi
