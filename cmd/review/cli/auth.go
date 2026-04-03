package cli

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strings"

	"github.com/alansikora/codecanary/internal/auth"
	"github.com/alansikora/codecanary/internal/credentials"
	"github.com/alansikora/codecanary/internal/review"
	"github.com/alansikora/codecanary/internal/setup"
	"github.com/charmbracelet/huh"
	"github.com/spf13/cobra"
	"golang.org/x/term"
)

var authCmd = &cobra.Command{
	Use:   "auth",
	Short: "Manage stored credentials",
	Long:  "View and manage API keys stored locally.",
}

var authStatusCmd = &cobra.Command{
	Use:   "status",
	Short: "Show stored credential status",
	RunE: func(cmd *cobra.Command, args []string) error {
		if _, source, err := credentials.Retrieve(); err == nil {
			fmt.Printf("  %s: stored in %s\n", credentials.EnvVar, source)
		} else {
			fmt.Printf("  %s: not found\n", credentials.EnvVar)
		}
		return nil
	},
}

var authDeleteCmd = &cobra.Command{
	Use:   "delete",
	Short: "Delete stored credential",
	RunE: func(cmd *cobra.Command, args []string) error {
		if _, _, err := credentials.Retrieve(); err != nil {
			fmt.Println("No stored credential found.")
			return nil
		}

		var confirm bool
		err := huh.NewForm(
			huh.NewGroup(
				huh.NewConfirm().
					Title(fmt.Sprintf("Delete %s?", credentials.EnvVar)).
					Value(&confirm),
			),
		).Run()
		if err != nil {
			return err
		}

		if !confirm {
			fmt.Println("Cancelled.")
			return nil
		}

		if err := credentials.Delete(); err != nil {
			return fmt.Errorf("deleting credential: %w", err)
		}
		fmt.Printf("Deleted %s.\n", credentials.EnvVar)
		return nil
	},
}

// refreshTarget identifies which credential store to refresh.
type refreshTarget struct {
	label    string // "Local" or "GitHub Actions"
	isRemote bool
}

var authRefreshCmd = &cobra.Command{
	Use:   "refresh",
	Short: "Check and update stored credentials",
	Long:  "Validate the current API key and optionally replace it.",
	RunE: func(cmd *cobra.Command, args []string) error {
		if !term.IsTerminal(int(os.Stdin.Fd())) {
			return fmt.Errorf("auth refresh requires an interactive terminal")
		}

		// Detect installs.
		hasLocal := hasLocalInstall()
		hasRemote, repo := hasRemoteInstall()

		if !hasLocal && !hasRemote {
			return fmt.Errorf("no CodeCanary installation found — run `codecanary setup` first")
		}

		// Determine target.
		var target refreshTarget
		if hasLocal && hasRemote {
			var choice string
			if err := huh.NewForm(
				huh.NewGroup(
					huh.NewSelect[string]().
						Title("Both local and GitHub Actions installs detected. Which credential do you want to refresh?").
						Options(
							huh.NewOption("Local", "local"),
							huh.NewOption(fmt.Sprintf("GitHub Actions (%s)", repoLabel(repo)), "github"),
						).
						Value(&choice),
				),
			).Run(); err != nil {
				return err
			}
			if choice == "github" {
				target = refreshTarget{label: "GitHub Actions", isRemote: true}
			} else {
				target = refreshTarget{label: "Local", isRemote: false}
			}
		} else if hasRemote {
			target = refreshTarget{label: "GitHub Actions", isRemote: true}
		} else {
			target = refreshTarget{label: "Local", isRemote: false}
		}

		// If targeting GitHub but repo unknown, prompt for it.
		if target.isRemote && repo == "" {
			fmt.Fprintf(os.Stderr, "GitHub Actions workflow found, but could not detect the repository.\n")
			fmt.Fprintf(os.Stderr, "Make sure the gh CLI is installed and authenticated, or enter the repo manually.\n\n")
			var manualRepo string
			if err := huh.NewForm(
				huh.NewGroup(
					huh.NewInput().
						Title("Repository (owner/repo)").
						Validate(func(s string) error {
							if !repoPattern.MatchString(strings.TrimSpace(s)) {
								return fmt.Errorf("expected format: owner/repo")
							}
							return nil
						}).
						Value(&manualRepo),
				),
			).Run(); err != nil {
				return err
			}
			repo = strings.TrimSpace(manualRepo)
		}

		fmt.Fprintf(os.Stderr, "Refreshing %s credentials\n\n", target.label)

		// Load provider from config.
		configPath, err := review.FindConfig()
		if err != nil {
			return fmt.Errorf("could not find config: %w", err)
		}
		cfg, err := review.LoadConfig(configPath)
		if err != nil {
			return fmt.Errorf("loading config: %w", err)
		}

		provider := cfg.Provider
		if provider == "claude" {
			fmt.Fprintf(os.Stderr, "Provider is Claude CLI — credentials are managed by the Claude CLI itself.\n")
			fmt.Fprintf(os.Stderr, "Run `claude` to check your authentication status.\n")
			return nil
		}

		fmt.Fprintf(os.Stderr, "Provider: %s\n", provider)

		// Retrieve current credential.
		currentKey, source, err := credentials.Retrieve()
		if err != nil {
			fmt.Fprintf(os.Stderr, "No stored credential found.\n")
			return promptAndStoreNewKey(provider, target, repo)
		}
		fmt.Fprintf(os.Stderr, "Credential found in %s\n", source)

		// Validate current credential.
		fmt.Fprintf(os.Stderr, "Validating current API key...")
		if err := setup.ValidateAPIKey(provider, currentKey); err != nil {
			fmt.Fprintf(os.Stderr, " invalid (%v)\n\n", err)
			fmt.Fprintf(os.Stderr, "The current credential does not work. You should replace it.\n\n")

			var replace bool
			if err := huh.NewForm(
				huh.NewGroup(
					huh.NewConfirm().
						Title("Replace credential?").
						Affirmative("Yes, enter new key").
						Negative("No, keep invalid key").
						Value(&replace),
				),
			).Run(); err != nil {
				return err
			}
			if !replace {
				fmt.Fprintf(os.Stderr, "Keeping existing credential.\n")
				return nil
			}
			return promptAndStoreNewKey(provider, target, repo)
		}

		fmt.Fprintf(os.Stderr, " valid!\n\n")

		var replace bool
		if err := huh.NewForm(
			huh.NewGroup(
				huh.NewConfirm().
					Title("Credential is valid. Replace it anyway?").
					Affirmative("Yes, replace").
					Negative("No, keep current").
					Value(&replace),
			),
		).Run(); err != nil {
			return err
		}
		if !replace {
			fmt.Fprintf(os.Stderr, "Keeping existing credential.\n")
			return nil
		}
		return promptAndStoreNewKey(provider, target, repo)
	},
}

// promptAndStoreNewKey collects a new API key (or runs OAuth), validates it,
// and stores it in the appropriate location (local keychain or GitHub secret).
func promptAndStoreNewKey(provider string, target refreshTarget, repo string) error {
	apiKey, err := setup.CollectCredential(provider)
	if err != nil {
		return err
	}

	// For remote target, update the GitHub secret first so a failure
	// doesn't leave local and remote out of sync.
	// All providers share the same secret name (CODECANARY_PROVIDER_SECRET).
	if target.isRemote {
		secretName := setup.ProviderSecretName()
		fmt.Fprintf(os.Stderr, "Setting %s secret on %s...\n", secretName, repo)
		if err := auth.SetGitHubSecret(repo, secretName, apiKey); err != nil {
			return fmt.Errorf("setting GitHub secret: %w", err)
		}
		fmt.Fprintf(os.Stderr, "GitHub secret updated.\n")
	}

	// Always store locally (matches setup flows — keeps local keychain in sync).
	fmt.Fprintf(os.Stderr, "Storing credential locally...\n")
	if err := credentials.Store(apiKey); err != nil {
		fmt.Fprintf(os.Stderr, "Warning: could not store credential locally: %v\n", err)
	} else {
		fmt.Fprintf(os.Stderr, "Local credential updated.\n")
	}

	fmt.Fprintf(os.Stderr, "\nCredential refreshed successfully.\n")
	return nil
}

// hasLocalInstall checks if a .codecanary/config.yml exists (local or GitHub — it's always there).
func hasLocalInstall() bool {
	_, err := review.FindConfig()
	return err == nil
}

// hasRemoteInstall checks if a .github/workflows/codecanary.yml exists and
// detects the GitHub repo via gh CLI. Returns (true, "") if the workflow is
// found but the repo cannot be detected (e.g. gh CLI not installed).
func hasRemoteInstall() (bool, string) {
	dir, err := os.Getwd()
	if err != nil {
		return false, ""
	}
	for {
		workflowPath := filepath.Join(dir, ".github", "workflows", "codecanary.yml")
		if _, err := os.Stat(workflowPath); err == nil {
			return true, detectRepo()
		}
		parent := filepath.Dir(dir)
		if parent == dir {
			break
		}
		dir = parent
	}
	return false, ""
}

// repoLabel returns the repo name for display, or a placeholder if unknown.
func repoLabel(repo string) string {
	if repo == "" {
		return "repo unknown"
	}
	return repo
}

// repoPattern matches GitHub "owner/repo" names. Both segments must start and
// end with an alphanumeric character (no trailing dots or hyphens).
var repoPattern = regexp.MustCompile(`^[a-zA-Z0-9]([a-zA-Z0-9._-]*[a-zA-Z0-9])?/[a-zA-Z0-9]([a-zA-Z0-9._-]*[a-zA-Z0-9])?$`)

// detectRepo returns "owner/repo" via gh CLI, or empty string on failure.
// The result is validated against repoPattern to reject malformed output.
func detectRepo() string {
	out, err := exec.Command("gh", "repo", "view", "--json", "nameWithOwner", "--jq", ".nameWithOwner").Output()
	if err != nil {
		return ""
	}
	name := strings.TrimSpace(string(out))
	if !repoPattern.MatchString(name) {
		return ""
	}
	return name
}

func init() {
	authCmd.AddCommand(authStatusCmd)
	authCmd.AddCommand(authDeleteCmd)
	authCmd.AddCommand(authRefreshCmd)
	rootCmd.AddCommand(authCmd)
}
