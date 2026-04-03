package setup

import (
	"bufio"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"

	"github.com/alansikora/codecanary/internal/auth"
	"github.com/alansikora/codecanary/internal/review"
	"github.com/charmbracelet/huh"
	"gopkg.in/yaml.v3"
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
		// Check if we're currently on that branch. If we can't determine
		// the current branch, refuse to delete conservatively.
		currentBranch, err := exec.Command("git", "rev-parse", "--abbrev-ref", "HEAD").Output()
		if err != nil {
			return fmt.Errorf("could not determine current branch (git error: %w) — refusing to delete %s; switch branches and retry", err, branch)
		}
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

	// 7. Install provider app if needed.
	if appReq := review.GetAppRequirement(provider); appReq != nil {
		if err := PromptAppInstall(appReq.Name, appReq.InstallURL, repo, reader); err != nil {
			return fmt.Errorf("installing %s app: %w", appReq.Name, err)
		}
	}

	// 8. Provider-specific auth and secret setup.
	secretName := ProviderSecretName()
	secretExists := auth.GitHubSecretExists(repo, secretName)
	previousProvider := readPreviousProvider()

	needNewSecret := true
	if secretExists {
		providerChanged := previousProvider != "" && previousProvider != provider

		var updateSecret bool
		if providerChanged {
			updateSecret = true // default to updating when provider changed
			if err := huh.NewForm(
				huh.NewGroup(
					huh.NewConfirm().
						Title(fmt.Sprintf("You changed your provider from %s to %s", previousProvider, provider)).
						Description(fmt.Sprintf("You probably want to update the %s secret with a new API key.", secretName)).
						Affirmative("Yes, update secret").
						Negative("No, keep existing").
						Value(&updateSecret),
				),
			).Run(); err != nil {
				return err
			}
		} else {
			title := fmt.Sprintf("%s secret already exists on %s", secretName, repo)
			desc := "You might want to keep the existing secret."
			if previousProvider != "" {
				title = fmt.Sprintf("You kept the same provider (%s)", provider)
				desc = fmt.Sprintf("The %s secret already exists — you might want to keep it.", secretName)
			}
			if err := huh.NewForm(
				huh.NewGroup(
					huh.NewConfirm().
						Title(title).
						Description(desc).
						Affirmative("Update secret").
						Negative("Keep existing").
						Value(&updateSecret),
				),
			).Run(); err != nil {
				return err
			}
		}
		needNewSecret = updateSecret
	}

	if needNewSecret {
		apiKey, err := CollectCredential(provider)
		if err != nil {
			return err
		}

		// Set GitHub secret.
		fmt.Fprintf(os.Stderr, "Setting %s secret on %s...\n", secretName, repo)
		if err := auth.SetGitHubSecret(repo, secretName, apiKey); err != nil {
			return fmt.Errorf("setting secret: %w", err)
		}
		fmt.Fprintf(os.Stderr, "  Done!\n\n")
	} else {
		fmt.Fprintf(os.Stderr, "Keeping existing %s secret.\n\n", secretName)
	}

	// 9. Select models.
	reviewModel, err := SelectModel(provider)
	if err != nil {
		return err
	}

	triageModel, err := SelectTriageModel(provider)
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

	workflow, err := GenerateWorkflow(secretName, actionRef)
	if err != nil {
		return err
	}

	if err := os.MkdirAll(workflowDir, 0755); err != nil {
		return fmt.Errorf("creating workflow directory: %w", err)
	}

	wroteWorkflow, err := writeFileWithConfirm(workflowPath, []byte(workflow))
	if err != nil {
		return err
	}
	if !wroteWorkflow {
		return fmt.Errorf("setup cancelled — workflow file is required")
	}

	// 12. Generate config.
	configPath := filepath.Join(".codecanary", "config.yml")
	wroteConfig, err := writeConfig(provider, reviewModel, triageModel, configPath)
	if err != nil {
		return err
	}

	// 13. Generate placeholder review policy.
	wrotePolicy, err := writeReviewPolicy(configPath)
	if err != nil {
		return err
	}

	// 14. Create PR.
	var filesToAdd []string
	var bullets []string

	filesToAdd = append(filesToAdd, workflowPath)
	bullets = append(bullets, "- Add CodeCanary automated PR review workflow")

	if wroteConfig {
		filesToAdd = append(filesToAdd, configPath)
		bullets = append(bullets, "- Add `.codecanary/config.yml` review config")
	}

	if wrotePolicy {
		policyPath := filepath.Join(".codecanary", "review.yml")
		filesToAdd = append(filesToAdd, policyPath)
		bullets = append(bullets, "- Add `.codecanary/review.yml` review policy placeholder")
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
	fmt.Fprintf(os.Stderr, "Customize review rules and context in .codecanary/review.yml\n")
	fmt.Fprintf(os.Stderr, "To also review locally, run: codecanary setup local\n")

	return nil
}

// readPreviousProvider reads the provider from an existing .codecanary/config.yml.
// Returns empty string if the file doesn't exist or can't be parsed.
func readPreviousProvider() string {
	data, err := os.ReadFile(filepath.Join(".codecanary", "config.yml"))
	if err != nil {
		return ""
	}
	var cfg struct {
		Provider string `yaml:"provider"`
	}
	if err := yaml.Unmarshal(data, &cfg); err != nil {
		return ""
	}
	return cfg.Provider
}
