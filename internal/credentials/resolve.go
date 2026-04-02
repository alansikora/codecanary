package credentials

import (
	"fmt"
	"strings"
)

// ResolveAPIKey looks up an API key by checking the environment first, then the OS keychain.
// Returns a descriptive error if neither source has the key.
func ResolveAPIKey(envVarName string, env []string) (string, error) {
	// 1. Check environment.
	for _, e := range env {
		k, v, ok := strings.Cut(e, "=")
		if ok && k == envVarName {
			return v, nil
		}
	}

	// 2. Check OS keychain.
	if val, err := Retrieve(envVarName); err == nil && val != "" {
		return val, nil
	}

	return "", fmt.Errorf("API key not found: set %s or run `codecanary setup local`", envVarName)
}
