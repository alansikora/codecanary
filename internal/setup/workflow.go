package setup

import (
	"fmt"
	"regexp"
)

var validSecretName = regexp.MustCompile(`^[A-Z][A-Z0-9_]*$`)
var validActionRef = regexp.MustCompile(`^[a-zA-Z0-9._-]+$`)

// GenerateWorkflow produces the GitHub Actions workflow YAML for CodeCanary.
// reviewSecretName and triageSecretName are GitHub Actions secret names.
// When both providers use the same secret, pass the same name for both.
func GenerateWorkflow(reviewSecretName, triageSecretName, actionRef string) (string, error) {
	if !validSecretName.MatchString(reviewSecretName) {
		return "", fmt.Errorf("invalid secret name %q — must match [A-Z][A-Z0-9_]*", reviewSecretName)
	}
	if triageSecretName != "" && !validSecretName.MatchString(triageSecretName) {
		return "", fmt.Errorf("invalid secret name %q — must match [A-Z][A-Z0-9_]*", triageSecretName)
	}
	if !validActionRef.MatchString(actionRef) {
		return "", fmt.Errorf("invalid action ref %q — must match [a-zA-Z0-9._-]+", actionRef)
	}

	// Normalize: if triage secret is empty or same as review, treat as single-secret.
	if triageSecretName == "" {
		triageSecretName = reviewSecretName
	}
	sameSec := triageSecretName == reviewSecretName

	// Build the action step's with: inputs and optional step-level env: block.
	var withAuth, stepEnv string

	if sameSec {
		// Single provider — same behavior as before.
		switch reviewSecretName {
		case "ANTHROPIC_API_KEY":
			withAuth = "          anthropic_api_key: ${{ secrets.ANTHROPIC_API_KEY }}"
		case "CLAUDE_CODE_OAUTH_TOKEN":
			withAuth = "          claude_code_oauth_token: ${{ secrets.CLAUDE_CODE_OAUTH_TOKEN }}"
		default:
			stepEnv = fmt.Sprintf("\n        env:\n          %s: ${{ secrets.%s }}", reviewSecretName, reviewSecretName)
		}
	} else {
		// Two different providers — pass both secrets via env.
		stepEnv = "\n        env:"
		for _, name := range uniqueSecrets(reviewSecretName, triageSecretName) {
			// Use with: for well-known action inputs, env: for the rest.
			switch name {
			case "ANTHROPIC_API_KEY":
				withAuth += "          anthropic_api_key: ${{ secrets.ANTHROPIC_API_KEY }}\n"
			case "CLAUDE_CODE_OAUTH_TOKEN":
				withAuth += "          claude_code_oauth_token: ${{ secrets.CLAUDE_CODE_OAUTH_TOKEN }}\n"
			default:
				stepEnv += fmt.Sprintf("\n          %s: ${{ secrets.%s }}", name, name)
			}
		}
		// If all secrets were handled via with:, clear stepEnv.
		if stepEnv == "\n        env:" {
			stepEnv = ""
		}
		// Trim trailing newline from withAuth if present.
		if len(withAuth) > 0 && withAuth[len(withAuth)-1] == '\n' {
			withAuth = withAuth[:len(withAuth)-1]
		}
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
`, actionRef, withAuth, stepEnv), nil
}

// uniqueSecrets returns deduplicated secret names in order.
func uniqueSecrets(names ...string) []string {
	seen := make(map[string]bool)
	var result []string
	for _, n := range names {
		if n != "" && !seen[n] {
			seen[n] = true
			result = append(result, n)
		}
	}
	return result
}
