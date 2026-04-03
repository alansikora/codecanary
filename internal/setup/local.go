package setup

import (
	"fmt"
	"os"
	"path/filepath"

	"github.com/alansikora/codecanary/internal/credentials"
)

// RunLocal executes the interactive local setup wizard.
func RunLocal() error {
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
		// Collect and validate API key.
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

		// Store credential (system keychain, or ~/.codecanary/credentials.json fallback).
		envVar := ProviderEnvVar(provider)
		if err := credentials.Store(envVar, apiKey); err != nil {
			fmt.Fprintf(os.Stderr, "Warning: could not store API key: %v\n", err)
			fmt.Fprintf(os.Stderr, "Set %s as an environment variable instead.\n\n", envVar)
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

	// 4. Generate or write config.
	configPath := filepath.Join(".codecanary", "config.yml")
	if err := writeConfig(provider, reviewModel, triageModel, configPath); err != nil {
		return err
	}

	fmt.Fprintf(os.Stderr, "\nSetup complete! Run `codecanary review` to review your current changes.\n")
	fmt.Fprintf(os.Stderr, "Add review rules and context in .codecanary/review.yml\n")
	return nil
}

