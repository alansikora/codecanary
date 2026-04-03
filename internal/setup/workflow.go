package setup

import (
	"fmt"
	"regexp"
)

var validSecretName = regexp.MustCompile(`^[A-Z][A-Z0-9_]*$`)
var validActionRef = regexp.MustCompile(`^[a-zA-Z0-9._-]+$`)

// GenerateWorkflow produces the GitHub Actions workflow YAML for CodeCanary.
// secretName must be a valid GitHub Actions secret name (uppercase, digits, underscores).
// actionRef must be a valid action version tag (e.g. "v1", "canary").
func GenerateWorkflow(secretName, actionRef string) (string, error) {
	if !validSecretName.MatchString(secretName) {
		return "", fmt.Errorf("invalid secret name %q — must match [A-Z][A-Z0-9_]*", secretName)
	}
	if !validActionRef.MatchString(actionRef) {
		return "", fmt.Errorf("invalid action ref %q — must match [a-zA-Z0-9._-]+", actionRef)
	}

	return fmt.Sprintf(`name: CodeCanary
on:
  pull_request_target:
    types: [opened, reopened, synchronize, ready_for_review]
  pull_request_review_comment:
    types: [created]

permissions:
  contents: read
  id-token: write
  pull-requests: write

jobs:
  review:
    if: >-
      (
        github.event_name == 'pull_request_target' &&
        github.event.pull_request.draft == false
      ) || (
        github.event.comment.user.login != 'codecanary-bot[bot]' &&
        github.event.comment.in_reply_to_id
      )
    runs-on: ubuntu-latest
    steps:
      - name: Check if codecanary thread
        id: check
        if: github.event_name == 'pull_request_review_comment'
        env:
          GH_TOKEN: ${{ github.token }}
        run: |
          BODY=$(gh api repos/${{ github.repository }}/pulls/comments/${{ github.event.comment.in_reply_to_id }} --jq '.body')
          if echo "$BODY" | grep -qF "codecanary:finding" || echo "$BODY" | grep -qF "codecanary fix" || echo "$BODY" | grep -qF "clanopy fix"; then
            echo "is_codecanary_thread=true" >> "$GITHUB_OUTPUT"
          else
            echo "Skipping: not a codecanary thread"
            exit 0
          fi

      - name: Skip if not codecanary thread
        if: github.event_name == 'pull_request_review_comment' && steps.check.outputs.is_codecanary_thread != 'true'
        run: |
          echo "skip=true" >> "$GITHUB_ENV"

      - uses: actions/checkout@v6
        if: env.skip != 'true'
        with:
          ref: ${{ github.event.pull_request.head.sha || github.sha }}

      - uses: alansikora/codecanary-action@%s
        if: env.skip != 'true'
        with:
          provider_secret: ${{ secrets.%s }}
          config_path: .codecanary/config.yml
          reply_only: ${{ github.event_name == 'pull_request_review_comment' }}

      - name: Usage
        if: always() && env.skip != 'true' && env.CODECANARY_USAGE != ''
        env:
          USAGE_DATA: ${{ env.CODECANARY_USAGE }}
        run: codecanary review costs --data "$USAGE_DATA"
`, actionRef, secretName), nil
}
