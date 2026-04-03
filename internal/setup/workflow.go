package setup

import (
	_ "embed"
	"fmt"
	"regexp"
	"strings"
)

//go:embed codecanary.yml
var canonicalWorkflow string

// Sentinel values in the canonical workflow that get replaced per-user.
const (
	sentinelActionRef   = "alansikora/codecanary@main"
	sentinelSecretRef   = "secrets.CODECANARY_PROVIDER_SECRET"
	sentinelVersionLine = "\n          codecanary_version: canary"
)

var validSecretName = regexp.MustCompile(`^[A-Z][A-Z0-9_]*$`)
var validActionRef = regexp.MustCompile(`^[a-zA-Z0-9._-]+$`)

// GenerateWorkflow produces the GitHub Actions workflow YAML for CodeCanary.
// secretName must be a valid GitHub Actions secret name (uppercase, digits, underscores).
// actionRef must be a valid action version tag (e.g. "v1", "canary").
func GenerateWorkflow(secretName, actionRef string) (string, error) {
	if !validSecretName.MatchString(secretName) {
		return "", fmt.Errorf("invalid secret name %q — must match [A-Z][A-Z0-9_]*", secretName)
	}
	if !validActionRef.MatchString(actionRef) {
		return "", fmt.Errorf("invalid action ref %q — must match [a-zA-Z0-9._-]+", actionRef)
	}

	result := canonicalWorkflow

	// Validate that all sentinel values exist in the embedded template.
	for _, sentinel := range []string{sentinelActionRef, sentinelSecretRef, sentinelVersionLine} {
		if !strings.Contains(result, sentinel) {
			return "", fmt.Errorf("workflow template is missing expected sentinel %q — the embedded codecanary.yml may have been modified incorrectly", sentinel)
		}
	}

	// 1. Replace action ref.
	// For canary, actionRef="canary" maps to @main (already the canonical value).
	targetRef := actionRef
	if actionRef == "canary" {
		targetRef = "main"
	}
	result = strings.Replace(result, sentinelActionRef, "alansikora/codecanary@"+targetRef, 1)

	// 2. Replace secret name.
	result = strings.Replace(result, sentinelSecretRef, "secrets."+secretName, 1)

	// 3. For non-canary (stable), remove the version line entirely.
	if actionRef != "canary" {
		result = strings.Replace(result, sentinelVersionLine, "", 1)
	}

	return result, nil
}
