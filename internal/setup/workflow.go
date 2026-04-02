package setup

import "fmt"

// GenerateWorkflow produces the GitHub Actions workflow YAML for CodeCanary.
func GenerateWorkflow(secretName, actionRef string) string {
	// Build the action step's with: inputs and optional step-level env: block.
	var withAuth, stepEnv string
	switch secretName {
	case "ANTHROPIC_API_KEY":
		withAuth = "          anthropic_api_key: ${{ secrets.ANTHROPIC_API_KEY }}"
	case "CLAUDE_CODE_OAUTH_TOKEN":
		withAuth = "          claude_code_oauth_token: ${{ secrets.CLAUDE_CODE_OAUTH_TOKEN }}"
	default:
		// Custom provider — pass the key as a step-level env var.
		stepEnv = fmt.Sprintf("\n        env:\n          %s: ${{ secrets.%s }}", secretName, secretName)
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
%s
          config_path: .codecanary/config.yml
          reply_only: ${{ github.event_name == 'pull_request_review_comment' }}%s

      - name: Usage
        if: always() && env.skip != 'true' && env.CODECANARY_USAGE != ''
        env:
          USAGE_DATA: ${{ env.CODECANARY_USAGE }}
        run: codecanary review costs --data "$USAGE_DATA"
`, actionRef, withAuth, stepEnv)
}
