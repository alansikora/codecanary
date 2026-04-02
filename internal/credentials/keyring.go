package credentials

import "github.com/zalando/go-keyring"

const serviceName = "codecanary"

// KnownProviderEnvVars returns the default env var names for each API-key-based provider.
var KnownProviderEnvVars = []string{
	"ANTHROPIC_API_KEY",
	"OPENAI_API_KEY",
	"OPENROUTER_API_KEY",
}

// Store saves an API key in the OS keychain under the given env var name.
func Store(envVarName, value string) error {
	return keyring.Set(serviceName, envVarName, value)
}

// Retrieve fetches an API key from the OS keychain.
func Retrieve(envVarName string) (string, error) {
	return keyring.Get(serviceName, envVarName)
}

// Delete removes an API key from the OS keychain.
func Delete(envVarName string) error {
	return keyring.Delete(serviceName, envVarName)
}

// DefaultEnvVar returns the default environment variable name for a provider.
func DefaultEnvVar(provider string) string {
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
