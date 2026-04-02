package credentials

import (
	"slices"

	"github.com/zalando/go-keyring"
)

const serviceName = "codecanary"

// providerEnvVars maps API-key-based providers to their default env var names.
var providerEnvVars = map[string]string{
	"anthropic":  "ANTHROPIC_API_KEY",
	"openai":     "OPENAI_API_KEY",
	"openrouter": "OPENROUTER_API_KEY",
}

// KnownProviderEnvVars returns the default env var names for each API-key-based provider.
func KnownProviderEnvVars() []string {
	vars := make([]string, 0, len(providerEnvVars))
	for _, v := range providerEnvVars {
		vars = append(vars, v)
	}
	slices.Sort(vars)
	return vars
}

// DefaultEnvVar returns the default environment variable name for a provider.
// Returns "" for providers that don't use API keys (e.g. "claude").
func DefaultEnvVar(provider string) string {
	return providerEnvVars[provider]
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
