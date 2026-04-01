package main

import (
	"bufio"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"syscall"

	"github.com/alansikora/codecanary/internal/auth"
	"github.com/alansikora/codecanary/internal/review"
	"golang.org/x/term"
)

var version = "dev"

func main() {
	if err := run(); err != nil {
		fmt.Fprintf(os.Stderr, "Error: %v\n", err)
		os.Exit(1)
	}
}

func hasFlag(name string) bool {
	for _, arg := range os.Args[1:] {
		if arg == name {
			return true
		}
	}
	return false
}

func run() error {
	canary := hasFlag("--canary")
	if canary {
		fmt.Fprintf(os.Stderr, "CodeCanary Setup (canary)\n\n")
	} else {
		fmt.Fprintf(os.Stderr, "CodeCanary Setup %s\n\n", version)
	}

	// Ensure stdin is a terminal so interactive prompts work.
	if !term.IsTerminal(int(os.Stdin.Fd())) {
		return fmt.Errorf("stdin is not a terminal — run with: curl -fsSL https://codecanary.sh/setup | sh")
	}

	reader := bufio.NewReader(os.Stdin)

	// 1. Check for gh CLI.
	if _, err := exec.LookPath("gh"); err != nil {
		return fmt.Errorf("gh CLI not found. Install it: https://cli.github.com")
	}

	// 2. Detect repo.
	repoOut, err := exec.Command("gh", "repo", "view", "--json", "nameWithOwner", "--jq", ".nameWithOwner").Output()
	if err != nil {
		return fmt.Errorf("could not detect repo (are you in a git repo with a GitHub remote?): %w", err)
	}
	repo := strings.TrimSpace(string(repoOut))
	fmt.Fprintf(os.Stderr, "Repository: %s\n\n", repo)

	// 3. Detect default branch and preflight.
	branch := "codecanary/review-setup"
	defaultBranchOut, err := exec.Command("gh", "repo", "view", "--json", "defaultBranchRef", "--jq", ".defaultBranchRef.name").Output()
	if err != nil {
		return fmt.Errorf("could not detect default branch: %w", err)
	}
	defaultBranch := strings.TrimSpace(string(defaultBranchOut))
	if defaultBranch == "" {
		return fmt.Errorf("could not detect default branch — is the repository empty?")
	}
	if err := exec.Command("git", "show-ref", "--verify", "refs/heads/"+branch).Run(); err == nil {
		return fmt.Errorf("branch %s already exists — delete it with `git branch -D %s` to retry", branch, branch)
	}

	// 4. Install the CodeCanary Review App.
	if err := auth.InstallCodeCanaryApp(repo, reader); err != nil {
		return fmt.Errorf("installing CodeCanary app: %w", err)
	}

	// 5. Auth: prompt for method.
	secretName, token, err := authenticateClaude(repo, reader)
	if err != nil {
		return err
	}

	// 6. Confirm and set secret.
	if token != "" {
		fmt.Fprintf(os.Stderr, "Set %s as a secret on %s? [Y/n] ", secretName, repo)
		ok, err := confirm(reader)
		if err != nil {
			return fmt.Errorf("reading input: %w", err)
		}
		if ok {
			fmt.Fprintf(os.Stderr, "Setting %s secret on %s...\n", secretName, repo)
			if err := auth.SetGitHubSecret(repo, secretName, token); err != nil {
				return fmt.Errorf("setting secret: %w", err)
			}
			fmt.Fprintf(os.Stderr, "  Done!\n\n")
		} else {
			fmt.Fprintf(os.Stderr, "Skipped. You'll need to set the secret manually:\n")
			fmt.Fprintf(os.Stderr, "  gh secret set %s --repo %s\n\n", secretName, repo)
		}
	}

	// 7. Create workflow file.
	workflowDir := filepath.Join(".github", "workflows")
	workflowPath := filepath.Join(workflowDir, "codecanary.yml")

	actionRef := "v1"
	if canary {
		actionRef = "canary"
	}

	var authEnv string
	if secretName == "ANTHROPIC_API_KEY" {
		authEnv = "          anthropic_api_key: ${{ secrets.ANTHROPIC_API_KEY }}"
	} else {
		authEnv = "          claude_code_oauth_token: ${{ secrets.CLAUDE_CODE_OAUTH_TOKEN }}"
	}

	workflow := fmt.Sprintf(`name: CodeCanary
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
          reply_only: ${{ github.event_name == 'pull_request_review_comment' }}

      - name: Usage
        if: always() && env.skip != 'true' && env.CODECANARY_USAGE != ''
        env:
          USAGE_DATA: ${{ env.CODECANARY_USAGE }}
        run: codecanary review costs --data "$USAGE_DATA"
`, actionRef, authEnv)

	if err := os.MkdirAll(workflowDir, 0755); err != nil {
		return fmt.Errorf("creating workflow directory: %w", err)
	}

	_, existsErr := os.Stat(workflowPath)
	workflowExisted := existsErr == nil
	if err := os.WriteFile(workflowPath, []byte(workflow), 0644); err != nil {
		return fmt.Errorf("writing workflow file: %w", err)
	}
	workflowCreated := true
	if workflowExisted {
		fmt.Fprintf(os.Stderr, "  Updated %s\n", workflowPath)
	} else {
		fmt.Fprintf(os.Stderr, "  Created %s\n", workflowPath)
	}

	// 8. Generate review config.
	configPath := filepath.Join(".codecanary", "config.yml")
	configCreated := false
	generateConfig := true
	if _, err := os.Stat(configPath); err == nil {
		fmt.Fprintf(os.Stderr, "  %s already exists. Re-generate? [y/N] ", configPath)
		answer, err := reader.ReadString('\n')
		if err != nil {
			return fmt.Errorf("reading input: %w", err)
		}
		if a := strings.TrimSpace(strings.ToLower(answer)); a != "y" && a != "yes" {
			fmt.Fprintf(os.Stderr, "  Keeping current config.\n")
			generateConfig = false
		}
	}
	if generateConfig {
		configContent := review.StarterConfig
		fmt.Fprintf(os.Stderr, "Generating review config...\n")
		if generated, err := review.Generate(); err != nil {
			fmt.Fprintf(os.Stderr, "  Warning: could not generate config with Claude: %v\n", err)
			fmt.Fprintf(os.Stderr, "  Using starter template instead\n")
		} else {
			configContent = generated + "\n"
		}
		if err := os.MkdirAll(filepath.Dir(configPath), 0o755); err != nil {
			return fmt.Errorf("creating .codecanary directory: %w", err)
		}
		if err := os.WriteFile(configPath, []byte(configContent), 0644); err != nil {
			return fmt.Errorf("writing review config: %w", err)
		}
		fmt.Fprintf(os.Stderr, "  Created %s\n", configPath)
		configCreated = true
	}

	// 9. Create PR.
	if !workflowCreated && !configCreated {
		fmt.Fprintf(os.Stderr, "\nSetup is already complete — nothing to do.\n")
		return nil
	}

	var filesToAdd []string
	var bullets []string
	if workflowCreated {
		filesToAdd = append(filesToAdd, ".github/workflows/codecanary.yml")
		bullets = append(bullets, "- Add CodeCanary automated PR review workflow")
	}
	if configCreated {
		filesToAdd = append(filesToAdd, ".codecanary/config.yml")
		bullets = append(bullets, "- Add starter `.codecanary/config.yml` config")
	}

	if len(filesToAdd) == 0 {
		return fmt.Errorf("internal error: no files to stage")
	}

	fmt.Fprintf(os.Stderr, "\nCreating PR...\n")

	if out, err := exec.Command("git", "checkout", "-b", branch).CombinedOutput(); err != nil {
		return fmt.Errorf("creating branch: %s\n%s", err, string(out))
	}
	if out, err := exec.Command("git", append([]string{"add"}, filesToAdd...)...).CombinedOutput(); err != nil {
		return fmt.Errorf("staging files: %s\n%s", err, string(out))
	}
	if out, err := exec.Command("git", "commit", "-m", "Add CodeCanary automated PR review").CombinedOutput(); err != nil {
		return fmt.Errorf("committing: %s\n%s", err, string(out))
	}
	if out, err := exec.Command("git", "push", "-u", "origin", branch).CombinedOutput(); err != nil {
		return fmt.Errorf("pushing: %s\n%s", err, string(out))
	}

	prBody := "## Summary\n" + strings.Join(bullets, "\n") + "\n\nCodeCanary will automatically review PRs on open and update."
	prOut, err := exec.Command("gh", "pr", "create",
		"--title", "Add CodeCanary PR review",
		"--body", prBody,
		"--repo", repo,
		"--base", defaultBranch,
	).CombinedOutput()
	if err != nil {
		return fmt.Errorf("creating PR: %s\n%s", err, string(prOut))
	}

	fmt.Fprintf(os.Stderr, "  %s\n", strings.TrimSpace(string(prOut)))
	fmt.Fprintf(os.Stderr, "\nDone! Merge the PR to enable automated reviews.\n")
	return nil
}

// authenticateClaude prompts the user for auth method and returns (secretName, token, error).
// If the secret already exists and the user doesn't want to replace it, returns ("", "", nil).
func authenticateClaude(repo string, reader *bufio.Reader) (string, string, error) {
	// Check if either secret already exists.
	secretsOut, err := exec.Command("gh", "secret", "list", "--repo", repo).Output()
	if err == nil {
		// OAuth token takes priority — only one auth secret is expected at a time.
		existing := ""
		if strings.Contains(string(secretsOut), "CLAUDE_CODE_OAUTH_TOKEN") {
			existing = "CLAUDE_CODE_OAUTH_TOKEN"
		} else if strings.Contains(string(secretsOut), "ANTHROPIC_API_KEY") {
			existing = "ANTHROPIC_API_KEY"
		}
		if existing != "" {
			fmt.Fprintf(os.Stderr, "  %s secret found on %s\n", existing, repo)
			fmt.Fprintf(os.Stderr, "  Replace it? [y/N] ")
			ok, err := confirmNo(reader)
			if err != nil {
				return "", "", fmt.Errorf("reading input: %w", err)
			}
			if !ok {
				fmt.Fprintf(os.Stderr, "  Keeping existing secret.\n\n")
				return existing, "", nil
			}
			fmt.Fprintf(os.Stderr, "\n")
		}
	}

	fmt.Fprintf(os.Stderr, "How would you like to authenticate Claude?\n")
	fmt.Fprintf(os.Stderr, "  [1] OAuth (default)\n")
	fmt.Fprintf(os.Stderr, "  [2] API key\n")
	fmt.Fprintf(os.Stderr, "Choice [1]: ")

	choice, err := reader.ReadString('\n')
	if err != nil {
		return "", "", fmt.Errorf("reading input: %w", err)
	}
	choice = strings.TrimSpace(choice)

	if choice == "2" {
		// API key flow.
		fmt.Fprintf(os.Stderr, "\nPaste your Anthropic API key: ")
		keyBytes, err := term.ReadPassword(int(syscall.Stdin))
		fmt.Fprintf(os.Stderr, "\n")
		if err != nil {
			return "", "", fmt.Errorf("reading API key: %w", err)
		}
		key := strings.TrimSpace(string(keyBytes))
		if key == "" {
			return "", "", fmt.Errorf("API key cannot be empty")
		}
		fmt.Fprintf(os.Stderr, "\n")
		return "ANTHROPIC_API_KEY", key, nil
	}

	// OAuth flow (default).
	if err := auth.InstallGitHubApp(repo, reader); err != nil {
		return "", "", fmt.Errorf("installing Claude GitHub App: %w", err)
	}

	token, err := auth.OAuthToken()
	if err != nil {
		return "", "", fmt.Errorf("OAuth authentication failed: %w", err)
	}

	return "CLAUDE_CODE_OAUTH_TOKEN", token, nil
}

func confirm(reader *bufio.Reader) (bool, error) {
	answer, err := reader.ReadString('\n')
	if err != nil {
		return false, err
	}
	answer = strings.TrimSpace(strings.ToLower(answer))
	return answer == "" || answer == "y" || answer == "yes", nil
}

func confirmNo(reader *bufio.Reader) (bool, error) {
	answer, err := reader.ReadString('\n')
	if err != nil {
		return false, err
	}
	answer = strings.TrimSpace(strings.ToLower(answer))
	return answer == "y" || answer == "yes", nil
}

