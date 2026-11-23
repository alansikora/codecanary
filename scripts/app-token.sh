#!/usr/bin/env bash
# Exchanges the job's GitHub Actions OIDC token for a CodeCanary app
# installation token and writes it to $GITHUB_OUTPUT as `token`.
#
# MODE is the action's `mode` input. A fix gets the app's full permissions,
# since it pushes. Anything else (a review, or a reply_only run) asks for a
# token that cannot write contents, and the step fails if the worker grants
# more: an older worker ignores the request body and would hand back a token
# that can push.
set -euo pipefail

OIDC_URL=https://oidc.codecanary.sh/token

if [ "${MODE:-review}" = "fix" ]; then
  TOKEN_MODE=fix
else
  TOKEN_MODE=review
fi

# Request OIDC token from GitHub Actions (requires id-token: write permission).
if [ -z "${ACTIONS_ID_TOKEN_REQUEST_URL:-}" ]; then
  echo "::error::OIDC not available. Add 'id-token: write' permission to your workflow and install the CodeCanary Review app."
  exit 1
fi
OIDC_TOKEN=$(curl -sS -H "Authorization: bearer $ACTIONS_ID_TOKEN_REQUEST_TOKEN" \
  "${ACTIONS_ID_TOKEN_REQUEST_URL}&audience=codecanary" | jq -r '.value')
if [ -z "$OIDC_TOKEN" ] || [ "$OIDC_TOKEN" = "null" ]; then
  echo "::error::Failed to obtain OIDC token."
  exit 1
fi

# Exchange OIDC token for a GitHub App installation token.
RESPONSE=$(curl -sS -X POST "$OIDC_URL" \
  -H "Authorization: Bearer $OIDC_TOKEN" \
  -H "Content-Type: application/json" \
  --data "{\"mode\":\"$TOKEN_MODE\"}")
TOKEN=$(echo "$RESPONSE" | jq -r '.token // empty' 2>/dev/null || true)
if [ -z "$TOKEN" ]; then
  ERROR=$(echo "$RESPONSE" | jq -r '.error // "unknown error"' 2>/dev/null || echo "unexpected response")
  echo "::error::Failed to get CodeCanary Review app token: $ERROR. Install the app at https://github.com/apps/codecanary-bot"
  exit 1
fi
echo "::add-mask::$TOKEN"

PERMISSIONS=$(echo "$RESPONSE" | jq -c '.permissions // {}')
echo "CodeCanary app token ($TOKEN_MODE): $PERMISSIONS"
if [ "$TOKEN_MODE" = "review" ]; then
  CONTENTS=$(echo "$RESPONSE" | jq -r '.permissions.contents // "missing"')
  if [ "$CONTENTS" != "read" ]; then
    echo "::error::The review token must have contents: read, but oidc.codecanary.sh granted contents: $CONTENTS. The token service is older than this action; refusing to review with a token that can push."
    exit 1
  fi
fi

echo "token=$TOKEN" >> "$GITHUB_OUTPUT"
