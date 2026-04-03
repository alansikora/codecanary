package credentials

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"

	"github.com/zalando/go-keyring"
)

const serviceName = "codecanary"

// EnvVar is the single environment variable used for all provider credentials.
const EnvVar = "CODECANARY_PROVIDER_SECRET"

// Store saves a credential. Tries the OS keychain first, falls back to
// ~/.codecanary/credentials.json (mode 0600) if no keychain is available.
func Store(value string) error {
	if err := keyring.Set(serviceName, EnvVar, value); err == nil {
		return nil
	}
	return fileStore(EnvVar, value)
}

// Retrieve fetches the stored credential. Tries the OS keychain first, falls back to
// the credentials file. Returns the value and the storage location ("keychain"
// or "credentials file").
func Retrieve() (value string, source string, err error) {
	if val, err := keyring.Get(serviceName, EnvVar); err == nil {
		return val, "keychain", nil
	}
	val, err := fileRetrieve(EnvVar)
	if err != nil {
		return "", "", err
	}
	return val, "credentials file", nil
}

// Delete removes the stored credential from both the OS keychain and the credentials file.
func Delete() error {
	if err := keyring.Delete(serviceName, EnvVar); err != nil && !errors.Is(err, keyring.ErrNotFound) {
		return fmt.Errorf("removing from keychain: %w", err)
	}
	return fileDelete(EnvVar)
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
	tmp.Close()
	if writeErr != nil {
		os.Remove(tmpPath) // best-effort cleanup
		return writeErr
	}
	if err := os.Chmod(tmpPath, 0600); err != nil {
		os.Remove(tmpPath) // best-effort cleanup
		return err
	}
	return os.Rename(tmpPath, path)
}

func fileStore(key, value string) error {
	creds, err := readCredentials()
	if err != nil {
		return err
	}
	creds[key] = value
	return writeCredentials(creds)
}

func fileRetrieve(key string) (string, error) {
	creds, err := readCredentials()
	if err != nil {
		return "", err
	}
	val, ok := creds[key]
	if !ok {
		return "", fmt.Errorf("key %s not found", key)
	}
	return val, nil
}

func fileDelete(key string) error {
	creds, err := readCredentials()
	if err != nil {
		return err
	}
	delete(creds, key)
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
