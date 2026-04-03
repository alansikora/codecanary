package setup

import (
	"fmt"
	"os"
	"strings"

	"github.com/alansikora/codecanary/internal/credentials"
	"github.com/alansikora/codecanary/internal/review"
	"github.com/charmbracelet/huh"
)

// RunConfigure lets users reconfigure the provider, models, and credentials
// on an existing .codecanary/config.yml.
func RunConfigure() error {
	// 1. Find and load existing config.
	configPath, err := review.FindConfig()
	if err != nil {
		return fmt.Errorf("no existing config found — run `codecanary setup` first")
	}

	cfg, err := review.LoadConfig(configPath)
	if err != nil {
		return fmt.Errorf("loading config: %w", err)
	}

	fmt.Fprintf(os.Stderr, "CodeCanary — Configure\n\n")
	fmt.Fprintf(os.Stderr, "Current config: %s\n", configPath)
	fmt.Fprintf(os.Stderr, "  Provider:     %s\n", cfg.Provider)
	fmt.Fprintf(os.Stderr, "  Review model: %s\n", cfg.EffectiveReviewModel())
	fmt.Fprintf(os.Stderr, "  Triage model: %s\n\n", cfg.EffectiveTriageModel())

	// 2. Select provider (pre-select current).
	provider, err := selectProviderWithDefault(cfg.Provider)
	if err != nil {
		return err
	}

	// 3. Handle credentials.
	providerChanged := provider != cfg.Provider
	if provider == "claude" {
		if providerChanged {
			if err := CheckClaudeCLI(); err != nil {
				return err
			}
			fmt.Fprintf(os.Stderr, "\n%s\n\n", ProviderGuidance("claude"))
		}
	} else {
		envVar := ProviderEnvVar(provider)
		_, source, credErr := credentials.Retrieve(envVar)

		if providerChanged || credErr != nil {
			// New provider or missing credential — must collect.
			apiKey, err := InputAPIKey(provider)
			if err != nil {
				return err
			}
			if err := validateAndStore(provider, envVar, apiKey); err != nil {
				return err
			}
		} else {
			// Credential exists — offer to update.
			var update bool
			if err := huh.NewForm(
				huh.NewGroup(
					huh.NewConfirm().
						Title(fmt.Sprintf("Update API key? (currently stored in %s)", source)).
						Affirmative("Yes").
						Negative("No").
						Value(&update),
				),
			).Run(); err != nil {
				return err
			}
			if update {
				apiKey, err := InputAPIKey(provider)
				if err != nil {
					return err
				}
				if err := validateAndStore(provider, envVar, apiKey); err != nil {
					return err
				}
			}
		}
	}

	// 4. Select review model (pre-select current).
	currentReviewModel := cfg.ReviewModel // "" means provider default
	if providerChanged {
		currentReviewModel = "" // reset to default when provider changes
	}
	reviewModel, err := selectModelWithDefault(provider, currentReviewModel)
	if err != nil {
		return err
	}

	// 5. Select triage model (pre-select current).
	currentTriageModel := cfg.TriageModel
	if providerChanged {
		currentTriageModel = review.GetSuggestedTriageModel(provider)
	}
	triageModel, err := selectTriageModelWithDefault(provider, currentTriageModel)
	if err != nil {
		return err
	}

	// 6. Update config file (preserves comments and other fields).
	if err := updateConfigFile(configPath, provider, reviewModel, triageModel); err != nil {
		return err
	}

	fmt.Fprintf(os.Stderr, "\nUpdated %s\n", configPath)
	fmt.Fprintf(os.Stderr, "  Provider:     %s\n", provider)
	if reviewModel != "" {
		fmt.Fprintf(os.Stderr, "  Review model: %s\n", reviewModel)
	} else {
		fmt.Fprintf(os.Stderr, "  Review model: (provider default)\n")
	}
	fmt.Fprintf(os.Stderr, "  Triage model: %s\n", triageModel)
	return nil
}

func validateAndStore(provider, envVar, apiKey string) error {
	fmt.Fprintf(os.Stderr, "Validating API key...")
	if err := ValidateAPIKey(provider, apiKey); err != nil {
		fmt.Fprintf(os.Stderr, " failed\n")
		return fmt.Errorf("API key validation failed: %w", err)
	}
	fmt.Fprintf(os.Stderr, " valid!\n")

	if err := credentials.Store(envVar, apiKey); err != nil {
		fmt.Fprintf(os.Stderr, "Warning: could not store API key: %v\n", err)
		fmt.Fprintf(os.Stderr, "Set %s as an environment variable instead.\n\n", envVar)
	} else {
		fmt.Fprintf(os.Stderr, "API key stored securely.\n\n")
	}
	return nil
}

func selectProviderWithDefault(current string) (string, error) {
	provider := current
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

func selectModelWithDefault(provider, current string) (string, error) {
	options := modelOptions(provider)
	if len(options) == 0 {
		return "", nil
	}
	model := current
	err := huh.NewForm(
		huh.NewGroup(
			huh.NewSelect[string]().
				Title("Review model").
				Description("Used for the main code review").
				Options(options...).
				Value(&model),
		),
	).Run()
	return model, err
}

func selectTriageModelWithDefault(provider, current string) (string, error) {
	options := triageModelOptions(provider)
	if len(options) == 0 {
		return "", nil
	}
	model := current
	err := huh.NewForm(
		huh.NewGroup(
			huh.NewSelect[string]().
				Title("Triage model").
				Description("Cheaper/faster model used to re-evaluate threads on incremental reviews").
				Options(options...).
				Value(&model),
		),
	).Run()
	return model, err
}

// updateConfigFile updates provider, review_model, and triage_model in the
// config file while preserving all other content (comments, rules, etc.).
func updateConfigFile(path, provider, reviewModel, triageModel string) error {
	data, err := os.ReadFile(path)
	if err != nil {
		return fmt.Errorf("reading config: %w", err)
	}

	lines := strings.Split(string(data), "\n")
	var result []string

	fieldsToSet := map[string]string{
		"provider":     provider,
		"triage_model": triageModel,
	}
	if reviewModel != "" {
		fieldsToSet["review_model"] = reviewModel
	}

	written := map[string]bool{}

	for _, line := range lines {
		trimmed := strings.TrimSpace(line)

		// Replace matching field lines.
		replaced := false
		for field, value := range fieldsToSet {
			if strings.HasPrefix(trimmed, field+":") {
				result = append(result, field+": "+value)
				written[field] = true
				replaced = true
				break
			}
		}

		// Remove review_model line when switching to provider default.
		if !replaced && reviewModel == "" && strings.HasPrefix(trimmed, "review_model:") {
			continue
		}

		if !replaced {
			result = append(result, line)
		}
	}

	// Insert any fields that weren't already in the file, after "version:".
	var missing []string
	for _, field := range []string{"provider", "review_model", "triage_model"} {
		if v, ok := fieldsToSet[field]; ok && !written[field] {
			missing = append(missing, field+": "+v)
		}
	}
	if len(missing) > 0 {
		var final []string
		inserted := false
		for _, line := range result {
			final = append(final, line)
			if !inserted && strings.HasPrefix(strings.TrimSpace(line), "version:") {
				final = append(final, missing...)
				inserted = true
			}
		}
		if !inserted {
			// No version line — prepend.
			final = append(missing, result...)
		}
		result = final
	}

	return os.WriteFile(path, []byte(strings.Join(result, "\n")), 0644)
}
