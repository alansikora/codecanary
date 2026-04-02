package credentials

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
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

// Store saves an API key. Tries the OS keychain first, falls back to
// ~/.codecanary/credentials.json (mode 0600) if no keychain is available.
func Store(envVarName, value string) error {
	if err := keyring.Set(serviceName, envVarName, value); err == nil {
		return nil
	}
	return fileStore(envVarName, value)
}

// Retrieve fetches an API key. Tries the OS keychain first, falls back to
// the credentials file.
func Retrieve(envVarName string) (string, error) {
	if val, err := keyring.Get(serviceName, envVarName); err == nil {
		return val, nil
	}
	return fileRetrieve(envVarName)
}

// Delete removes an API key from both the OS keychain and the credentials file.
func Delete(envVarName string) error {
	_ = keyring.Delete(serviceName, envVarName)
	return fileDelete(envVarName)
}

// --- file-based fallback (for systems without a keychain) ---

func credentialsPath() string {
	home, _ := os.UserHomeDir()
	return filepath.Join(home, ".codecanary", "credentials.json")
}

func readCredentials() (map[string]string, error) {
	data, err := os.ReadFile(credentialsPath())
	if err != nil {
		if os.IsNotExist(err) {
			return map[string]string{}, nil
		}
		return nil, err
	}
	var creds map[string]string
	if err := json.Unmarshal(data, &creds); err != nil {
		return nil, fmt.Errorf("parsing credentials file: %w", err)
	}
	return creds, nil
}

func writeCredentials(creds map[string]string) error {
	path := credentialsPath()
	if err := os.MkdirAll(filepath.Dir(path), 0700); err != nil {
		return err
	}
	data, err := json.MarshalIndent(creds, "", "  ")
	if err != nil {
		return err
	}
	return os.WriteFile(path, data, 0600)
}

func fileStore(envVarName, value string) error {
	creds, err := readCredentials()
	if err != nil {
		return err
	}
	creds[envVarName] = value
	return writeCredentials(creds)
}

func fileRetrieve(envVarName string) (string, error) {
	creds, err := readCredentials()
	if err != nil {
		return "", err
	}
	val, ok := creds[envVarName]
	if !ok {
		return "", fmt.Errorf("key %s not found", envVarName)
	}
	return val, nil
}

func fileDelete(envVarName string) error {
	creds, err := readCredentials()
	if err != nil {
		return err
	}
	delete(creds, envVarName)
	if len(creds) == 0 {
		// Clean up the file entirely if empty.
		os.Remove(credentialsPath())
		return nil
	}
	return writeCredentials(creds)
}
