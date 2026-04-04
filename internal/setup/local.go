package setup

import (
	"fmt"
	"os"
	"path/filepath"

	"github.com/alansikora/codecanary/internal/credentials"
	"github.com/alansikora/codecanary/internal/review"
	"github.com/alansikora/codecanary/internal/telemetry"
)

// RunLocal executes the interactive local setup wizard.
func RunLocal(version string) error {
	fmt.Fprintf(os.Stderr, "CodeCanary — Local Setup\n\n")

	// 1. Select provider.
	provider, err := SelectProvider()
	if err != nil {
		return err
	}

	// 2. Provider-specific credential setup.
	if provider == "claude" {
		if err := CheckClaudeCLI(); err != nil {
			return err
		}
		fmt.Fprintf(os.Stderr, "\n%s\n\n", ProviderGuidance("claude"))
	} else {
		apiKey, err := CollectCredential(provider)
		if err != nil {
			return err
		}

		// Store credential (system keychain, or ~/.codecanary/credentials.json fallback).
		if err := credentials.Store(apiKey); err != nil {
			fmt.Fprintf(os.Stderr, "Warning: could not store API key: %v\n", err)
			fmt.Fprintf(os.Stderr, "Set %s as an environment variable instead.\n\n", credentials.EnvVar)
		} else {
			fmt.Fprintf(os.Stderr, "API key stored securely.\n\n")
		}
	}

	// 3. Select models.
	reviewModel, err := SelectModel(provider)
	if err != nil {
		return err
	}

	triageModel, err := SelectTriageModel(provider)
	if err != nil {
		return err
	}

	// 4. Generate or write config to ~/.codecanary/repos/<owner>/<repo>/config.yml.
	configPath, err := review.LocalConfigPath()
	if err != nil {
		return err
	}
	if _, err := writeConfig(provider, reviewModel, triageModel, configPath); err != nil {
		return err
	}

	// 5. Generate placeholder review policy in the repo (.codecanary/review.yml).
	// writeReviewPolicy derives the directory from its argument.
	repoConfigPath := filepath.Join(".codecanary", "config.yml")
	if _, err := writeReviewPolicy(repoConfigPath); err != nil {
		return err
	}

	telemetry.SendSetup(version, provider, "local")

	if telemetry.IsFirstRun() {
		fmt.Fprint(os.Stderr, telemetryOptOutMessage)
	}
	fmt.Fprintf(os.Stderr, "\nSetup complete! Run `codecanary review` to review your current changes.\n")
	fmt.Fprintf(os.Stderr, "Customize review rules and context in .codecanary/review.yml\n")
	return nil
}
