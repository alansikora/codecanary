package setup

import "fmt"

// ProviderGuidance returns human-readable guidance on where to get credentials for a provider.
func ProviderGuidance(provider string) string {
	switch provider {
	case "anthropic":
		return "Get your API key at console.anthropic.com"
	case "openai":
		return "Get your API key at platform.openai.com"
	case "openrouter":
		return "Get your API key at openrouter.ai"
	case "claude":
		return "CodeCanary will use your Claude CLI's authentication.\nMake sure you're logged in by running: claude"
	default:
		return ""
	}
}

// ProviderEnvVar returns the default env var name for a provider's API key.
func ProviderEnvVar(provider string) string {
	switch provider {
	case "anthropic":
		return "ANTHROPIC_API_KEY"
	case "openai":
		return "OPENAI_API_KEY"
	case "openrouter":
		return "OPENROUTER_API_KEY"
	default:
		return ""
	}
}

// GitHubPermissionsGuidance returns an explanation of the GitHub Actions permissions.
func GitHubPermissionsGuidance() string {
	return `The workflow requires these GitHub Actions permissions:
  contents: read         — read repository code
  pull-requests: write   — post review comments on PRs
  id-token: write        — OIDC token for secure authentication`
}

// ProviderSecretName returns the GitHub secret name for a provider.
func ProviderSecretName(provider string) string {
	switch provider {
	case "anthropic":
		return "ANTHROPIC_API_KEY"
	case "openai":
		return "OPENAI_API_KEY"
	case "openrouter":
		return "OPENROUTER_API_KEY"
	case "claude":
		return "CLAUDE_CODE_OAUTH_TOKEN"
	default:
		return fmt.Sprintf("%s_API_KEY", provider)
	}
}
