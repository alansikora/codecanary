package setup

import (
	"bufio"
	"bytes"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/alansikora/codecanary/internal/auth"
	"github.com/alansikora/codecanary/internal/review"
	"github.com/charmbracelet/huh"
	"gopkg.in/yaml.v3"
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
	if provider == "" {
		return "", fmt.Errorf("provider must not be empty")
	}

	guidance := ProviderGuidance(provider)

	var apiKey string
	err := huh.NewForm(
		huh.NewGroup(
			huh.NewNote().
				Title(fmt.Sprintf("%s API Key", strings.ToTitle(provider[:1])+provider[1:])).
				Description(fmt.Sprintf("%s\nEnvironment variable: %s", guidance, ProviderEnvVar())),
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

// CollectCredential handles provider-specific credential collection:
// OAuth flow for providers that support it, manual API key input otherwise.
// The credential is always validated before being returned.
func CollectCredential(provider string) (string, error) {
	if oauthCfg := review.GetOAuthConfig(provider); oauthCfg != nil {
		token, err := auth.OAuthToken(oauthCfg.ClientID, oauthCfg.AuthorizeURL, oauthCfg.TokenURL, oauthCfg.Scope)
		if err != nil {
			return "", fmt.Errorf("OAuth authentication failed: %w", err)
		}

		fmt.Fprintf(os.Stderr, "Validating OAuth token...")
		if err := ValidateAPIKey(provider, token); err != nil {
			fmt.Fprintf(os.Stderr, " failed\n")
			return "", fmt.Errorf("OAuth token validation failed: %w", err)
		}
		fmt.Fprintf(os.Stderr, " valid!\n")
		return token, nil
	}

	key, err := InputAPIKey(provider)
	if err != nil {
		return "", err
	}

	fmt.Fprintf(os.Stderr, "Validating API key...")
	if err := ValidateAPIKey(provider, key); err != nil {
		fmt.Fprintf(os.Stderr, " failed\n")
		return "", fmt.Errorf("API key validation failed: %w", err)
	}
	fmt.Fprintf(os.Stderr, " valid!\n")
	return key, nil
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
// Returns true if the file was written, false if the user declined to overwrite.
func writeFileWithConfirm(path string, data []byte) (bool, error) {
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

func writeReviewPolicy(configPath string) (bool, error) {
	dir := filepath.Dir(configPath)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return false, fmt.Errorf("creating config directory: %w", err)
	}
	policyPath := filepath.Join(dir, "review.yml")

	// Non-destructive: if the file already exists, leave it alone.
	if _, err := os.Stat(policyPath); err == nil {
		fmt.Fprintf(os.Stderr, "Keeping existing %s\n", policyPath)
		return false, nil
	} else if !errors.Is(err, os.ErrNotExist) {
		return false, fmt.Errorf("checking %s: %w", policyPath, err)
	}

	content := `# Review rules — custom checks CodeCanary enforces on every PR.
# Each rule needs an id, description, and severity.
# Severity: critical | bug | warning | suggestion | nitpick
#
# rules:
#   - id: no-todo
#     description: "Don't merge TODO comments — open an issue instead"
#     severity: warning
#   - id: api-auth
#     description: "All new API endpoints must use the auth middleware"
#     severity: critical
#     paths: ["src/api/**"]

# Context — free-form text that gives the reviewer background about
# your project, stack, conventions, or anything it should keep in mind.
#
# context: |
#   This is a Go backend with a React frontend.
#   We use PostgreSQL and follow the repository pattern.

# Ignore — glob patterns for files CodeCanary should skip entirely.
#
# ignore:
#   - "vendor/**"
#   - "**/*.generated.go"
#   - "*.min.js"
`

	if err := os.WriteFile(policyPath, []byte(content), 0644); err != nil {
		return false, fmt.Errorf("writing %s: %w", policyPath, err)
	}
	fmt.Fprintf(os.Stderr, "Created %s\n", policyPath)
	return true, nil
}

func writeConfig(provider, reviewModel, triageModel, configPath string) (bool, error) {
	if err := os.MkdirAll(filepath.Dir(configPath), 0o755); err != nil {
		return false, fmt.Errorf("creating config directory: %w", err)
	}

	if provider == "" {
		return false, fmt.Errorf("provider is required")
	}
	if reviewModel == "" {
		return false, fmt.Errorf("review_model is required")
	}
	if triageModel == "" {
		return false, fmt.Errorf("triage_model is required")
	}

	// Read existing file into a yaml.Node tree to preserve comments and
	// user-added fields. If the file doesn't exist, start from scratch.
	existing, err := os.ReadFile(configPath)
	if err != nil {
		if !errors.Is(err, os.ErrNotExist) {
			return false, fmt.Errorf("reading %s: %w", configPath, err)
		}
		// File doesn't exist — fall through to fresh write.
	}

	if len(bytes.TrimSpace(existing)) == 0 {
		// No existing file (or empty) — write fresh.
		config := fmt.Sprintf("version: 1\n\nprovider: %s\n", provider)
		config += fmt.Sprintf("review_model: %s\n", reviewModel)
		config += fmt.Sprintf("triage_model: %s\n", triageModel)

		if err := os.WriteFile(configPath, []byte(config), 0644); err != nil {
			return false, fmt.Errorf("writing %s: %w", configPath, err)
		}
		fmt.Fprintf(os.Stderr, "Created %s\n", configPath)
		return true, nil
	}

	// Parse into node tree so we can surgically update keys.
	var doc yaml.Node
	if err := yaml.Unmarshal(existing, &doc); err != nil {
		return false, fmt.Errorf("parsing existing %s: %w", configPath, err)
	}
	if doc.Kind != yaml.DocumentNode || len(doc.Content) == 0 || doc.Content[0].Kind != yaml.MappingNode {
		return false, fmt.Errorf("%s is not a valid YAML mapping", configPath)
	}

	mapping := doc.Content[0]
	updates := []struct{ key, value string }{
		{"version", "1"},
		{"provider", provider},
		{"review_model", reviewModel},
		{"triage_model", triageModel},
	}

	// Collect changes, log what's different, skip write if nothing changed.
	var diffs []string
	for _, u := range updates {
		old := getYAMLScalar(mapping, u.key)
		if old == u.value {
			continue
		}
		if old == "" {
			diffs = append(diffs, fmt.Sprintf("  %s: %s (new)", u.key, u.value))
		} else {
			diffs = append(diffs, fmt.Sprintf("  %s: %s → %s", u.key, old, u.value))
		}
	}

	if len(diffs) == 0 {
		fmt.Fprintf(os.Stderr, "%s is up to date\n", configPath)
		return false, nil
	}

	for _, u := range updates {
		tag := "!!str"
		if u.key == "version" {
			tag = "!!int"
		}
		setYAMLScalar(mapping, u.key, u.value, yaml.ScalarNode, tag)
	}

	var buf bytes.Buffer
	enc := yaml.NewEncoder(&buf)
	enc.SetIndent(2)
	if err := enc.Encode(&doc); err != nil {
		return false, fmt.Errorf("marshaling config: %w", err)
	}
	if err := enc.Close(); err != nil {
		return false, fmt.Errorf("closing encoder: %w", err)
	}

	if err := os.WriteFile(configPath, buf.Bytes(), 0644); err != nil {
		return false, fmt.Errorf("writing %s: %w", configPath, err)
	}
	fmt.Fprintf(os.Stderr, "Updated %s:\n", configPath)
	for _, d := range diffs {
		fmt.Fprintln(os.Stderr, d)
	}
	return true, nil
}

// getYAMLScalar returns the string value of a scalar key in a YAML mapping
// node, or "" if the key is not found.
func getYAMLScalar(mapping *yaml.Node, key string) string {
	for i := 0; i < len(mapping.Content)-1; i += 2 {
		if mapping.Content[i].Value == key {
			return mapping.Content[i+1].Value
		}
	}
	return ""
}

// setYAMLScalar sets a scalar key in a YAML mapping node, updating the value
// in place if the key exists, or appending a new key-value pair if it doesn't.
// When updating an existing node, only the value is changed — the original tag
// and style are preserved. The tag parameter is only used for new entries.
func setYAMLScalar(mapping *yaml.Node, key, value string, kind yaml.Kind, tag string) {
	for i := 0; i < len(mapping.Content)-1; i += 2 {
		if mapping.Content[i].Value == key {
			mapping.Content[i+1].Value = value
			return
		}
	}
	keyNode := &yaml.Node{Kind: yaml.ScalarNode, Value: key}
	valNode := &yaml.Node{Kind: kind, Value: value, Tag: tag}
	mapping.Content = append(mapping.Content, keyNode, valNode)
}

// PromptAppInstall asks the user whether to install a provider app and, if confirmed,
// opens the install URL in the browser and waits for the user to finish.
func PromptAppInstall(name, installURL, repo string, reader *bufio.Reader) error {
	var install bool
	err := huh.NewForm(
		huh.NewGroup(
			huh.NewConfirm().
				Title(fmt.Sprintf("Install the %s app on %s?", name, repo)).
				Value(&install),
		),
	).Run()
	if err != nil {
		return err
	}
	if !install {
		return fmt.Errorf("skipped — %s app installation is required", name)
	}

	fmt.Fprintf(os.Stderr, "Opening browser to install the %s app...\n", name)
	fmt.Fprintf(os.Stderr, "  → Select the repository: %s\n\n", repo)
	if err := auth.OpenBrowser(installURL); err != nil {
		fmt.Fprintf(os.Stderr, "Could not open browser: %v\nOpen this URL in your browser:\n%s\n\n", err, installURL)
	}
	fmt.Fprintf(os.Stderr, "Press Enter after installing the app...")
	_, _ = reader.ReadString('\n')
	fmt.Fprintln(os.Stderr)
	return nil
}

func triageModelOptions(provider string) []huh.Option[string] {
	switch provider {
	case "anthropic":
		return []huh.Option[string]{
			huh.NewOption("claude-haiku-4-5-20251001", "claude-haiku-4-5-20251001"),
			huh.NewOption("claude-sonnet-4-6", "claude-sonnet-4-6"),
			huh.NewOption("claude-opus-4-6", "claude-opus-4-6"),
		}
	case "openai":
		return []huh.Option[string]{
			huh.NewOption("gpt-5.4-mini", "gpt-5.4-mini"),
			huh.NewOption("gpt-5.4", "gpt-5.4"),
		}
	case "openrouter":
		return []huh.Option[string]{
			huh.NewOption("anthropic/claude-haiku-4-5-20251001", "anthropic/claude-haiku-4-5-20251001"),
			huh.NewOption("anthropic/claude-sonnet-4-6", "anthropic/claude-sonnet-4-6"),
			huh.NewOption("openai/gpt-5.4-mini", "openai/gpt-5.4-mini"),
			huh.NewOption("openai/gpt-5.4", "openai/gpt-5.4"),
		}
	case "claude":
		return []huh.Option[string]{
			huh.NewOption("haiku", "haiku"),
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
			huh.NewOption("claude-sonnet-4-6", "claude-sonnet-4-6"),
			huh.NewOption("claude-opus-4-6", "claude-opus-4-6"),
			huh.NewOption("claude-haiku-4-5-20251001", "claude-haiku-4-5-20251001"),
		}
	case "openai":
		return []huh.Option[string]{
			huh.NewOption("gpt-5.4", "gpt-5.4"),
			huh.NewOption("gpt-5.4-mini", "gpt-5.4-mini"),
		}
	case "openrouter":
		return []huh.Option[string]{
			huh.NewOption("anthropic/claude-sonnet-4-6", "anthropic/claude-sonnet-4-6"),
			huh.NewOption("anthropic/claude-opus-4-6", "anthropic/claude-opus-4-6"),
			huh.NewOption("openai/gpt-5.4", "openai/gpt-5.4"),
		}
	case "claude":
		return []huh.Option[string]{
			huh.NewOption("sonnet", "sonnet"),
			huh.NewOption("opus", "opus"),
			huh.NewOption("haiku", "haiku"),
		}
	default:
		return nil
	}
}
