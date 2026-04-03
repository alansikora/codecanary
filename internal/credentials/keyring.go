package credentials

import (
	"encoding/json"
	"errors"
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
// the credentials file. Returns the value and the storage location ("keychain"
// or "credentials file").
func Retrieve(envVarName string) (value string, source string, err error) {
	if val, err := keyring.Get(serviceName, envVarName); err == nil {
		return val, "keychain", nil
	}
	val, err := fileRetrieve(envVarName)
	if err != nil {
		return "", "", err
	}
	return val, "credentials file", nil
}

// Delete removes an API key from both the OS keychain and the credentials file.
func Delete(envVarName string) error {
	if err := keyring.Delete(serviceName, envVarName); err != nil && !errors.Is(err, keyring.ErrNotFound) {
		return fmt.Errorf("removing from keychain: %w", err)
	}
	return fileDelete(envVarName)
}

// --- file-based fallback (for systems without a keychain) ---

func credentialsPath() (string, error) {
	home, err := os.UserHomeDir()
	if err != nil {
		return "", fmt.Errorf("could not determine home directory: %w", err)
	}
	return filepath.Join(home, ".codecanary", "credentials.json"), nil
}

func readCredentials() (map[string]string, error) {
	path, err := credentialsPath()
	if err != nil {
		return nil, err
	}
	data, err := os.ReadFile(path)
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
	path, err := credentialsPath()
	if err != nil {
		return err
	}
	dir := filepath.Dir(path)
	if err := os.MkdirAll(dir, 0700); err != nil {
		return err
	}
	data, err := json.MarshalIndent(creds, "", "  ")
	if err != nil {
		return err
	}
	// Atomic write: temp file + rename to avoid partial writes corrupting existing credentials.
	tmp, err := os.CreateTemp(dir, ".credentials-*.json")
	if err != nil {
		return err
	}
	tmpPath := tmp.Name()
	_, writeErr := tmp.Write(data)
	_ = tmp.Close()
	if writeErr != nil {
		_ = os.Remove(tmpPath) // best-effort cleanup
		return writeErr
	}
	if err := os.Chmod(tmpPath, 0600); err != nil {
		_ = os.Remove(tmpPath) // best-effort cleanup
		return err
	}
	return os.Rename(tmpPath, path)
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
		path, err := credentialsPath()
		if err != nil {
			return err
		}
		if err := os.Remove(path); err != nil && !os.IsNotExist(err) {
			return err
		}
		return nil
	}
	return writeCredentials(creds)
}
