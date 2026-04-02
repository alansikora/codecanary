package setup

import (
	"bufio"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"

	"github.com/alansikora/codecanary/internal/auth"
	"github.com/alansikora/codecanary/internal/credentials"
	"github.com/charmbracelet/huh"
)

// RunGitHub executes the interactive GitHub Actions setup wizard.
func RunGitHub(canary bool) error {
	fmt.Fprintf(os.Stderr, "CodeCanary — GitHub Actions Setup\n\n")

	// 1. Check for gh CLI.
	if _, err := exec.LookPath("gh"); err != nil {
		return fmt.Errorf("gh CLI not found — install it from https://cli.github.com")
	}

	// 2. Detect repo.
	repoOut, err := exec.Command("gh", "repo", "view", "--json", "nameWithOwner", "--jq", ".nameWithOwner").Output()
	if err != nil {
		return fmt.Errorf("could not detect repo (are you in a git repo with a GitHub remote?): %w", err)
	}
	repo := strings.TrimSpace(string(repoOut))
	fmt.Fprintf(os.Stderr, "Repository: %s\n\n", repo)

	// 3. Detect default branch.
	defaultBranchOut, err := exec.Command("gh", "repo", "view", "--json", "defaultBranchRef", "--jq", ".defaultBranchRef.name").Output()
	if err != nil {
		return fmt.Errorf("could not detect default branch: %w", err)
	}
	defaultBranch := strings.TrimSpace(string(defaultBranchOut))
	if defaultBranch == "" || defaultBranch == "null" {
		return fmt.Errorf("could not detect default branch — is the repository empty?")
	}

	// 4. Handle existing setup branch.
	branch := "codecanary/review-setup"
	if err := exec.Command("git", "show-ref", "--verify", "refs/heads/"+branch).Run(); err == nil {
		// Check if we're currently on that branch.
		currentBranch, _ := exec.Command("git", "rev-parse", "--abbrev-ref", "HEAD").Output()
		if strings.TrimSpace(string(currentBranch)) == branch {
			return fmt.Errorf("you are currently on branch %s — switch to another branch first, then retry", branch)
		}

		var deleteBranch bool
		if err := huh.NewForm(
			huh.NewGroup(
				huh.NewConfirm().
					Title(fmt.Sprintf("Branch %s already exists. Delete and re-create?", branch)).
					Value(&deleteBranch),
			),
		).Run(); err != nil {
			return err
		}
		if !deleteBranch {
			return fmt.Errorf("setup cancelled — branch %s already exists", branch)
		}
		if out, err := exec.Command("git", "branch", "-D", branch).CombinedOutput(); err != nil {
			return fmt.Errorf("deleting branch: %s\n%s", err, string(out))
		}
	}

	// 5. Install CodeCanary GitHub App.
	reader := bufio.NewReader(os.Stdin)
	if err := auth.InstallCodeCanaryApp(repo, reader); err != nil {
		return fmt.Errorf("installing CodeCanary app: %w", err)
	}

	// 6. Select provider.
	provider, err := SelectProvider()
	if err != nil {
		return err
	}

	// 7. Provider-specific auth and secret setup.
	var secretName string
	var apiKey string

	if provider == "claude" {
		// OAuth flow for Claude CLI.
		if err := auth.InstallGitHubApp(repo, reader); err != nil {
			return fmt.Errorf("installing Claude GitHub App: %w", err)
		}
		token, err := auth.OAuthToken()
		if err != nil {
			return fmt.Errorf("OAuth authentication failed: %w", err)
		}
		secretName = "CLAUDE_CODE_OAUTH_TOKEN"
		apiKey = token
	} else {
		// Collect API key.
		key, err := InputAPIKey(provider)
		if err != nil {
			return err
		}

		fmt.Fprintf(os.Stderr, "Validating API key...")
		if err := ValidateAPIKey(provider, key); err != nil {
			fmt.Fprintf(os.Stderr, " failed\n")
			return fmt.Errorf("API key validation failed: %w", err)
		}
		fmt.Fprintf(os.Stderr, " valid!\n")

		secretName = ProviderSecretName(provider)
		apiKey = key

		// Also store locally for `codecanary review` usage.
		envVar := ProviderEnvVar(provider)
		if err := credentials.Store(envVar, key); err != nil {
			// Non-fatal — local storage is a nice-to-have during GitHub setup.
			fmt.Fprintf(os.Stderr, "Note: could not store key in local keychain: %v\n", err)
		}
	}

	// 8. Set GitHub secret.
	var setSecret bool
	err = huh.NewForm(
		huh.NewGroup(
			huh.NewConfirm().
				Title(fmt.Sprintf("Set %s as a secret on %s?", secretName, repo)).
				Value(&setSecret),
		),
	).Run()
	if err != nil {
		return err
	}

	if setSecret {
		fmt.Fprintf(os.Stderr, "Setting %s secret on %s...\n", secretName, repo)
		if err := auth.SetGitHubSecret(repo, secretName, apiKey); err != nil {
			return fmt.Errorf("setting secret: %w", err)
		}
		fmt.Fprintf(os.Stderr, "  Done!\n\n")
	} else {
		fmt.Fprintf(os.Stderr, "Skipped. Set the secret manually:\n")
		fmt.Fprintf(os.Stderr, "  gh secret set %s --repo %s\n\n", secretName, repo)
	}

	// 9. Select model.
	reviewModel, err := SelectModel(provider)
	if err != nil {
		return err
	}

	// 10. Explain workflow permissions.
	fmt.Fprintf(os.Stderr, "\n%s\n\n", GitHubPermissionsGuidance())

	// 11. Generate workflow file.
	workflowDir := filepath.Join(".github", "workflows")
	workflowPath := filepath.Join(workflowDir, "codecanary.yml")

	actionRef := "v1"
	if canary {
		actionRef = "canary"
	}

	workflow := GenerateWorkflow(secretName, actionRef)

	if err := os.MkdirAll(workflowDir, 0755); err != nil {
		return fmt.Errorf("creating workflow directory: %w", err)
	}

	if err := writeFileWithConfirm(workflowPath, []byte(workflow)); err != nil {
		return err
	}

	// 12. Generate config.
	configPath := filepath.Join(".codecanary", "config.yml")
	if err := writeConfig(provider, reviewModel, configPath); err != nil {
		return err
	}

	// 13. Create PR.
	var filesToAdd []string
	var bullets []string

	filesToAdd = append(filesToAdd, workflowPath)
	bullets = append(bullets, "- Add CodeCanary automated PR review workflow")

	if _, err := os.Stat(configPath); err == nil {
		filesToAdd = append(filesToAdd, configPath)
		bullets = append(bullets, "- Add `.codecanary/config.yml` review config")
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
