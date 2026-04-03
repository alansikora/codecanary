package setup

import (
	"github.com/alansikora/codecanary/internal/credentials"
)

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

// ProviderEnvVar returns the environment variable name used for provider credentials.
func ProviderEnvVar() string {
	return credentials.EnvVar
}

// GitHubPermissionsGuidance returns an explanation of the GitHub Actions permissions.
func GitHubPermissionsGuidance() string {
	return `The workflow requires these GitHub Actions permissions:
  contents: read         — read repository code
  pull-requests: write   — post review comments on PRs
  id-token: write        — OIDC token for secure authentication`
}

// ProviderSecretName returns the GitHub secret name for the provider API key.
func ProviderSecretName() string {
	return credentials.EnvVar
}
