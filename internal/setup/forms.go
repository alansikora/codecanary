package setup

import (
	"fmt"
	"os"
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

// SelectProvider prompts the user to choose an LLM provider.
func SelectProvider() (string, error) {
	var provider string
	err := huh.NewForm(
		huh.NewGroup(
			huh.NewSelect[string]().
				Title("Which AI provider do you want to use?").
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

// SelectLocalProvider prompts for local use: Claude CLI (subscription) or an API-key provider.
func SelectLocalProvider() (string, error) {
	var authKind string
	if err := huh.NewForm(
		huh.NewGroup(
			huh.NewSelect[string]().
				Title("How should CodeCanary authenticate on this machine?").
				Description("Claude Pro or Max through the Claude CLI does not use ANTHROPIC_API_KEY.").
				Options(
					huh.NewOption("Claude CLI — subscription (no API key)", "claude"),
					huh.NewOption("API key — Anthropic, OpenAI, or OpenRouter", "api_key"),
				).
				Value(&authKind),
		),
	).Run(); err != nil {
		return "", err
	}
	if authKind == "claude" {
		return "claude", nil
	}
	return selectAPIKeyProvider()
}

func selectAPIKeyProvider() (string, error) {
	var provider string
	err := huh.NewForm(
		huh.NewGroup(
			huh.NewSelect[string]().
				Title("Which provider's API key?").
				Options(
					huh.NewOption("Anthropic", "anthropic"),
					huh.NewOption("OpenAI", "openai"),
					huh.NewOption("OpenRouter", "openrouter"),
				).
				Value(&provider),
		),
	).Run()
	return provider, err
}

// SelectExistingConfigStrategy asks how to persist setup when config.yml already exists.
func SelectExistingConfigStrategy() (string, error) {
	var choice string
	err := huh.NewForm(
		huh.NewGroup(
			huh.NewSelect[string]().
				Title(".codecanary/config.yml already exists. How should we save this setup?").
				Description("CI and teammates use config.yml. Use config.local.yml for machine-only overrides.").
				Options(
					huh.NewOption("Replace config.yml (shared file — commit with care)", "replace"),
					huh.NewOption("Write config.local.yml only (local reviews merge this; config.yml unchanged)", "local_overlay"),
				).
				Value(&choice),
		),
	).Run()
	return choice, err
}

// InputAPIKey prompts the user for their API key with provider-specific guidance.
func InputAPIKey(provider string) (string, error) {
	if provider == "" {
		return "", fmt.Errorf("provider must not be empty")
	}
	if provider == "claude" {
		return "", fmt.Errorf("claude provider uses the Claude CLI, not an API key — pick the Claude CLI option in local setup")
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

// SelectModel prompts the user to choose a review model or accept defaults.
func SelectModel(provider string) (string, error) {
	options := modelOptions(provider)
	if len(options) == 0 {
		return "", nil
	}

	var reviewModel string
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

// WriteFileConfirmOpts customizes the overwrite confirmation prompt.
type WriteFileConfirmOpts struct {
	OverwriteTitle       string
	OverwriteDescription string
}

// writeFileWithConfirm writes data to path, prompting to overwrite if it already exists.
// The bool is true if the file was written (created or updated).
func writeFileWithConfirm(path string, data []byte, opts WriteFileConfirmOpts) (bool, error) {
	action := "Created"
	title := opts.OverwriteTitle
	if title == "" {
		title = fmt.Sprintf("%s already exists. Overwrite?", path)
	}
	if _, err := os.Stat(path); err == nil {
		var overwrite bool
		confirm := huh.NewConfirm().
			Title(title).
			Value(&overwrite)
		if opts.OverwriteDescription != "" {
			confirm = confirm.Description(opts.OverwriteDescription)
		}
		if err := huh.NewForm(huh.NewGroup(confirm)).Run(); err != nil {
			return false, err
		}
		if !overwrite {
			fmt.Fprintf(os.Stderr, "Keeping existing %s\n", path)
			return false, nil
		}
		action = "Updated"
	}
	if err := os.WriteFile(path, data, 0644); err != nil {
		return false, fmt.Errorf("writing %s: %w", path, err)
	}
	fmt.Fprintf(os.Stderr, "%s %s\n", action, path)
	return true, nil
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
			huh.NewOption("claude-sonnet-4-6 (default)", ""),
			huh.NewOption("claude-opus-4-6", "claude-opus-4-6"),
			huh.NewOption("claude-haiku-4-5-20251001", "claude-haiku-4-5-20251001"),
		}
	case "openai":
		return []huh.Option[string]{
			huh.NewOption("gpt-5.4 (default)", ""),
			huh.NewOption("gpt-5.4-mini", "gpt-5.4-mini"),
		}
	case "openrouter":
		return []huh.Option[string]{
			huh.NewOption("anthropic/claude-sonnet-4-6 (default)", ""),
			huh.NewOption("anthropic/claude-opus-4-6", "anthropic/claude-opus-4-6"),
			huh.NewOption("openai/gpt-5.4", "openai/gpt-5.4"),
		}
	case "claude":
		return []huh.Option[string]{
			huh.NewOption("sonnet (default)", ""),
			huh.NewOption("opus", "opus"),
			huh.NewOption("haiku", "haiku"),
		}
	default:
		return nil
	}
}
