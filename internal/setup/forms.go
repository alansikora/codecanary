package setup

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/alansikora/codecanary/internal/review"
	"github.com/charmbracelet/huh"
)

// SelectSetupMode prompts the user to choose between local and GitHub setup.
func SelectSetupMode() (string, error) {
	var mode string
	err := huh.NewForm(
		huh.NewGroup(
			huh.NewSelect[string]().
				Title("How do you want to set up CodeCanary?").
				Options(
					huh.NewOption("Local development (review changes on this machine)", "local"),
					huh.NewOption("GitHub Actions (automated PR reviews)", "github"),
				).
				Value(&mode),
		),
	).Run()
	return mode, err
}

// SelectProvider prompts the user to choose an LLM provider for a given role.
func SelectProvider(role string) (string, error) {
	title := fmt.Sprintf("Which AI provider for %s?", role)
	var provider string
	err := huh.NewForm(
		huh.NewGroup(
			huh.NewSelect[string]().
				Title(title).
				Options(
					huh.NewOption("Anthropic", "anthropic"),
					huh.NewOption("OpenAI", "openai"),
					huh.NewOption("OpenRouter", "openrouter"),
					huh.NewOption("Claude CLI", "claude"),
				).
				Value(&provider),
		),
	).Run()
	return provider, err
}

// SelectTriageProvider prompts the user to choose a triage provider,
// with a "Same as review" default option.
func SelectTriageProvider(reviewProvider string) (string, error) {
	sameLabel := fmt.Sprintf("Same as review (%s)", reviewProvider)
	options := []huh.Option[string]{
		huh.NewOption(sameLabel, reviewProvider),
	}
	// Add other providers, skipping the one that matches reviewProvider
	// to avoid duplicate values in the dropdown.
	all := []struct{ label, value string }{
		{"Anthropic", "anthropic"},
		{"OpenAI", "openai"},
		{"OpenRouter", "openrouter"},
		{"Claude CLI", "claude"},
	}
	for _, p := range all {
		if p.value != reviewProvider {
			options = append(options, huh.NewOption(p.label, p.value))
		}
	}

	var provider string
	err := huh.NewForm(
		huh.NewGroup(
			huh.NewSelect[string]().
				Title("Which AI provider for triage?").
				Options(options...).
				Value(&provider),
		),
	).Run()
	return provider, err
}

// InputAPIKey prompts the user for their API key with provider-specific guidance.
func InputAPIKey(provider string) (string, error) {
	if provider == "" {
		return "", fmt.Errorf("provider must not be empty")
	}

	guidance := ProviderGuidance(provider)
	envVar := ProviderEnvVar(provider)

	var apiKey string
	err := huh.NewForm(
		huh.NewGroup(
			huh.NewNote().
				Title(fmt.Sprintf("%s API Key", strings.ToTitle(provider[:1])+provider[1:])).
				Description(fmt.Sprintf("%s\nEnvironment variable: %s", guidance, envVar)),
			huh.NewInput().
				Title("API Key").
				EchoMode(huh.EchoModePassword).
				Validate(func(s string) error {
					if strings.TrimSpace(s) == "" {
						return fmt.Errorf("API key cannot be empty")
					}
					return nil
				}).
				Value(&apiKey),
		),
	).Run()
	return strings.TrimSpace(apiKey), err
}

// SelectModel prompts the user to choose a review model.
// The provider's suggested review model is pre-selected.
func SelectModel(provider string) (string, error) {
	options := modelOptions(provider)
	if len(options) == 0 {
		return "", nil
	}

	reviewModel := review.GetSuggestedReviewModel(provider)
	err := huh.NewForm(
		huh.NewGroup(
			huh.NewSelect[string]().
				Title("Review model").
				Description("Used for the main code review").
				Options(options...).
				Value(&reviewModel),
		),
	).Run()
	return reviewModel, err
}

// SelectTriageModel prompts the user to choose a triage model.
// The provider's suggested triage model is pre-selected.
func SelectTriageModel(provider string) (string, error) {
	options := triageModelOptions(provider)
	if len(options) == 0 {
		return "", nil
	}

	triageModel := review.GetSuggestedTriageModel(provider)
	err := huh.NewForm(
		huh.NewGroup(
			huh.NewSelect[string]().
				Title("Triage model").
				Description("Cheaper/faster model used to re-evaluate threads on incremental reviews").
				Options(options...).
				Value(&triageModel),
		),
	).Run()
	return triageModel, err
}

// writeFileWithConfirm writes data to path, prompting to overwrite if it already exists.
func writeFileWithConfirm(path string, data []byte) error {
	action := "Created"
	if _, err := os.Stat(path); err == nil {
		var overwrite bool
		if err := huh.NewForm(
			huh.NewGroup(
				huh.NewConfirm().
					Title(fmt.Sprintf("%s already exists. Overwrite?", path)).
					Value(&overwrite),
			),
		).Run(); err != nil {
			return err
		}
		if !overwrite {
			fmt.Fprintf(os.Stderr, "Keeping existing %s\n", path)
			return nil
		}
		action = "Updated"
	}
	if err := os.WriteFile(path, data, 0644); err != nil {
		return fmt.Errorf("writing %s: %w", path, err)
	}
	fmt.Fprintf(os.Stderr, "%s %s\n", action, path)
	return nil
}

// writeConfig generates and writes the .codecanary/config.yml file.
func writeConfig(reviewMC, triageMC review.ModelConfig, configPath string) error {
	if err := os.MkdirAll(filepath.Dir(configPath), 0o755); err != nil {
		return fmt.Errorf("creating config directory: %w", err)
	}

	if reviewMC.Provider == "" || reviewMC.Model == "" {
		return fmt.Errorf("review provider and model are required")
	}
	if triageMC.Provider == "" || triageMC.Model == "" {
		return fmt.Errorf("triage provider and model are required")
	}

	config := "version: 1\nreview:\n"
	config += fmt.Sprintf("  provider: %s\n", reviewMC.Provider)
	config += fmt.Sprintf("  model: %s\n", reviewMC.Model)
	config += "triage:\n"
	config += fmt.Sprintf("  provider: %s\n", triageMC.Provider)
	config += fmt.Sprintf("  model: %s\n", triageMC.Model)

	return writeFileWithConfirm(configPath, []byte(config))
}

func triageModelOptions(provider string) []huh.Option[string] {
	switch provider {
	case "anthropic":
		return []huh.Option[string]{
			huh.NewOption("claude-haiku-4-5-20251001 (recommended)", "claude-haiku-4-5-20251001"),
			huh.NewOption("claude-sonnet-4-6", "claude-sonnet-4-6"),
			huh.NewOption("claude-opus-4-6", "claude-opus-4-6"),
		}
	case "openai":
		return []huh.Option[string]{
			huh.NewOption("gpt-5.4-mini (recommended)", "gpt-5.4-mini"),
			huh.NewOption("gpt-5.4", "gpt-5.4"),
		}
	case "openrouter":
		return []huh.Option[string]{
			huh.NewOption("anthropic/claude-haiku-4-5-20251001 (recommended)", "anthropic/claude-haiku-4-5-20251001"),
			huh.NewOption("anthropic/claude-sonnet-4-6", "anthropic/claude-sonnet-4-6"),
			huh.NewOption("openai/gpt-5.4-mini", "openai/gpt-5.4-mini"),
			huh.NewOption("openai/gpt-5.4", "openai/gpt-5.4"),
		}
	case "claude":
		return []huh.Option[string]{
			huh.NewOption("haiku (recommended)", "haiku"),
			huh.NewOption("sonnet", "sonnet"),
			huh.NewOption("opus", "opus"),
		}
	default:
		return nil
	}
}

func modelOptions(provider string) []huh.Option[string] {
	switch provider {
	case "anthropic":
		return []huh.Option[string]{
			huh.NewOption("claude-sonnet-4-6 (recommended)", "claude-sonnet-4-6"),
			huh.NewOption("claude-opus-4-6", "claude-opus-4-6"),
			huh.NewOption("claude-haiku-4-5-20251001", "claude-haiku-4-5-20251001"),
		}
	case "openai":
		return []huh.Option[string]{
			huh.NewOption("gpt-5.4 (recommended)", "gpt-5.4"),
			huh.NewOption("gpt-5.4-mini", "gpt-5.4-mini"),
		}
	case "openrouter":
		return []huh.Option[string]{
			huh.NewOption("anthropic/claude-sonnet-4-6 (recommended)", "anthropic/claude-sonnet-4-6"),
			huh.NewOption("anthropic/claude-opus-4-6", "anthropic/claude-opus-4-6"),
			huh.NewOption("openai/gpt-5.4", "openai/gpt-5.4"),
		}
	case "claude":
		return []huh.Option[string]{
			huh.NewOption("sonnet (recommended)", "sonnet"),
			huh.NewOption("opus", "opus"),
			huh.NewOption("haiku", "haiku"),
		}
	default:
		return nil
	}
}
