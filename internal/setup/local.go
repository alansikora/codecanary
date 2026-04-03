package setup

import (
	"fmt"
	"os"
	"path/filepath"

	"github.com/alansikora/codecanary/internal/credentials"
	"github.com/alansikora/codecanary/internal/review"
)

// RunLocal executes the interactive local setup wizard.
func RunLocal() error {
	fmt.Fprintf(os.Stderr, "CodeCanary — Local Setup\n\n")

	// 1. Review provider.
	reviewProvider, err := SelectProvider("review")
	if err != nil {
		return err
	}

	// 2. Review provider credentials.
	if err := collectLocalCredentials(reviewProvider); err != nil {
		return err
	}

	// 3. Review model.
	reviewModel, err := SelectModel(reviewProvider)
	if err != nil {
		return err
	}

	// 4. Triage provider.
	triageProvider, err := SelectTriageProvider(reviewProvider)
	if err != nil {
		return err
	}

	// 5. Triage provider credentials (only if different from review).
	if triageProvider != reviewProvider {
		if err := collectLocalCredentials(triageProvider); err != nil {
			return err
		}
	}

	// 6. Triage model.
	triageModel, err := SelectTriageModel(triageProvider)
	if err != nil {
		return err
	}

	// 7. Write config.
	configPath := filepath.Join(".codecanary", "config.yml")
	reviewMC := review.ModelConfig{Provider: reviewProvider, Model: reviewModel}
	triageMC := review.ModelConfig{Provider: triageProvider, Model: triageModel}
	if err := writeConfig(reviewMC, triageMC, configPath); err != nil {
		return err
	}

	fmt.Fprintf(os.Stderr, "\nSetup complete! Run `codecanary review` to review your current changes.\n")
	return nil
}

// collectLocalCredentials handles credential collection for a provider in local mode.
func collectLocalCredentials(provider string) error {
	if provider == "claude" {
		if err := CheckClaudeCLI(); err != nil {
			return err
		}
		fmt.Fprintf(os.Stderr, "\n%s\n\n", ProviderGuidance("claude"))
		return nil
	}

	apiKey, err := InputAPIKey(provider)
	if err != nil {
		return err
	}

	fmt.Fprintf(os.Stderr, "Validating API key...")
	if err := ValidateAPIKey(provider, apiKey); err != nil {
		fmt.Fprintf(os.Stderr, " failed\n")
		return fmt.Errorf("API key validation failed: %w", err)
	}
	fmt.Fprintf(os.Stderr, " valid!\n")

	envVar := ProviderEnvVar(provider)
	if err := credentials.Store(envVar, apiKey); err != nil {
		fmt.Fprintf(os.Stderr, "Warning: could not store API key: %v\n", err)
		fmt.Fprintf(os.Stderr, "Set %s as an environment variable instead.\n\n", envVar)
	} else {
		fmt.Fprintf(os.Stderr, "API key stored securely.\n\n")
	}
	return nil
}

