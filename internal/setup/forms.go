package setup

import (
	"fmt"
	"os"
	"strings"

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

// InputAPIKey prompts the user for their API key with provider-specific guidance.
func InputAPIKey(provider string) (string, error) {
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

// ConfirmConfigGeneration asks whether to generate config by analyzing the project.
func ConfirmConfigGeneration() (bool, error) {
	var generate bool
	err := huh.NewForm(
		huh.NewGroup(
			huh.NewConfirm().
				Title("Generate a review config by analyzing this project?").
				Description("Uses the AI provider to create rules tailored to your codebase").
				Value(&generate),
		),
	).Run()
	return generate, err
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
