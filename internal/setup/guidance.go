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

// ProviderEnvVar returns the canonical name used for provider credentials — both as
// a local environment variable and as a GitHub Actions secret name.
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

// telemetryOptOutMessage is the message shown on first run to inform about telemetry.
const telemetryOptOutMessage = "\nAnonymous telemetry is on. Opt out: CODECANARY_NO_TELEMETRY=1\n"
