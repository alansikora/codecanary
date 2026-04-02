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

		// Store in OS keychain.
		envVar := ProviderEnvVar(provider)
		if err := credentials.Store(envVar, apiKey); err != nil {
			fmt.Fprintf(os.Stderr, "Warning: could not store key in system keychain: %v\n", err)
			fmt.Fprintf(os.Stderr, "You can set %s as an environment variable instead.\n\n", envVar)
		} else {
			fmt.Fprintf(os.Stderr, "API key stored in system keychain.\n\n")
		}
	}

	// 3. Select model.
	reviewModel, err := SelectModel(provider)
	if err != nil {
		return err
	}

	// 4. Generate or write config.
	configPath := filepath.Join(".codecanary", "config.yml")
	if err := writeConfig(provider, reviewModel, configPath); err != nil {
		return err
	}

	fmt.Fprintf(os.Stderr, "\nSetup complete! Run `codecanary review` to review your current changes.\n")
	return nil
}

func writeConfig(provider, reviewModel, configPath string) error {
	if err := os.MkdirAll(filepath.Dir(configPath), 0o755); err != nil {
		return fmt.Errorf("creating config directory: %w", err)
	}

	// Build a minimal working config.
	config := fmt.Sprintf("version: 1\nprovider: %s\n", provider)
	if reviewModel != "" {
		config += fmt.Sprintf("review_model: %s\n", reviewModel)
	}
	config += "\n" + review.StarterRulesSection

	return writeFileWithConfirm(configPath, []byte(config))
}
