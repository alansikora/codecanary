package setup

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/alansikora/codecanary/internal/credentials"
	"github.com/alansikora/codecanary/internal/review"
)

// RunLocal executes the interactive local setup wizard.
func RunLocal() error {
	fmt.Fprintf(os.Stderr, "CodeCanary — Local Setup\n\n")

	provider, err := SelectLocalProvider()
	if err != nil {
		return err
	}
	provider = strings.TrimSpace(provider)

	if provider == "claude" {
		if err := CheckClaudeCLI(); err != nil {
			return err
		}
		fmt.Fprintf(os.Stderr, "\n%s\n\n", ProviderGuidance("claude"))
	} else {
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
	}

	reviewModel, err := SelectModel(provider)
	if err != nil {
		return err
	}

	triageModel, err := SelectTriageModel(provider)
	if err != nil {
		return err
	}

	written, err := writeLocalSetupConfig(provider, reviewModel, triageModel)
	if err != nil {
		return err
	}
	if !written {
		fmt.Fprintf(os.Stderr, "\nNo config file was updated.\n")
		fmt.Fprintf(os.Stderr, "If you wanted Claude CLI locally but the repo still has provider: anthropic in config.yml, run setup again and choose config.local.yml or overwrite.\n")
		return nil
	}

	fmt.Fprintf(os.Stderr, "\nSetup complete! Run `codecanary review` to review your current changes.\n")
	return nil
}

func buildMainConfigYAML(provider, reviewModel, triageModel string) string {
	config := fmt.Sprintf("version: 1\nprovider: %s\n", provider)
	if reviewModel != "" {
		config += fmt.Sprintf("review_model: %s\n", reviewModel)
	}
	config += fmt.Sprintf("triage_model: %s\n", triageModel)
	config += "\n" + review.StarterRulesSection
	return config
}

func buildMinimalLocalOverlay(provider, reviewModel, triageModel string) string {
	var b strings.Builder
	b.WriteString("# Merged on top of config.yml when you run `codecanary review` without a PR.\n")
	b.WriteString("# GitHub Actions uses only config.yml. Add this file to .gitignore if it should stay private.\n")
	b.WriteString("# Numeric limits from the main config (max_budget_usd, max_file_size, etc.) can only be\n")
	b.WriteString("# tightened via this file, not cleared back to unlimited/default — omitted fields inherit from config.yml.\n")
	b.WriteString("version: 1\n")
	b.WriteString(fmt.Sprintf("provider: %s\n", provider))
	if reviewModel != "" {
		b.WriteString(fmt.Sprintf("review_model: %s\n", reviewModel))
	}
	b.WriteString(fmt.Sprintf("triage_model: %s\n", triageModel))
	return b.String()
}

func writeLocalSetupConfig(provider, reviewModel, triageModel string) (bool, error) {
	configPath := filepath.Join(".codecanary", "config.yml")
	localPath := filepath.Join(".codecanary", "config.local.yml")

	if err := os.MkdirAll(filepath.Dir(configPath), 0o755); err != nil {
		return false, fmt.Errorf("creating config directory: %w", err)
	}
	if triageModel == "" {
		return false, fmt.Errorf("triage_model is required")
	}

	mainYAML := buildMainConfigYAML(provider, reviewModel, triageModel)
	overlayYAML := buildMinimalLocalOverlay(provider, reviewModel, triageModel)

	if _, statErr := os.Stat(configPath); os.IsNotExist(statErr) {
		return writeFileWithConfirm(configPath, []byte(mainYAML), WriteFileConfirmOpts{})
	} else if statErr != nil {
		return false, fmt.Errorf("checking config path: %w", statErr)
	}

	strategy, err := SelectExistingConfigStrategy()
	if err != nil {
		return false, err
	}

	switch strategy {
	case "local_overlay":
		w, err := writeFileWithConfirm(localPath, []byte(overlayYAML), WriteFileConfirmOpts{
			OverwriteTitle:       fmt.Sprintf("%s already exists. Overwrite?", localPath),
			OverwriteDescription: "This file affects only local reviews on this machine. Safe to add to .gitignore.",
		})
		if err != nil {
			return false, err
		}
		if w {
			fmt.Fprintf(os.Stderr, "\nWrote %s — merged over config.yml when you run local `codecanary review` (no PR).\n", localPath)
			fmt.Fprintf(os.Stderr, "Add %s to .gitignore if you do not want these settings in the repo.\n", localPath)
		}
		return w, nil
	default:
		return writeFileWithConfirm(configPath, []byte(mainYAML), WriteFileConfirmOpts{
			OverwriteTitle:       fmt.Sprintf("Replace %s?", configPath),
			OverwriteDescription: "This is the shared config used by CI and teammates. If you only need different settings on your laptop, go back and choose config.local.yml instead.",
		})
	}
}

// writeMainConfigForPath writes the full config with starter rules (GitHub setup path).
func writeMainConfigForPath(provider, reviewModel, triageModel, configPath string) (bool, error) {
	if err := os.MkdirAll(filepath.Dir(configPath), 0o755); err != nil {
		return false, fmt.Errorf("creating config directory: %w", err)
	}
	if triageModel == "" {
		return false, fmt.Errorf("triage_model is required")
	}
	data := buildMainConfigYAML(provider, reviewModel, triageModel)
	return writeFileWithConfirm(configPath, []byte(data), WriteFileConfirmOpts{})
}
