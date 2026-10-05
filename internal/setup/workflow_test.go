package setup

import (
	"crypto/sha256"
	"encoding/hex"
	"os"
	"path/filepath"
	"regexp"
	"runtime"
	"strings"
	"testing"
)

// repoRoot returns the absolute path to the repository root.
func repoRoot(t *testing.T) string {
	t.Helper()
	_, filename, _, ok := runtime.Caller(0)
	if !ok {
		t.Fatal("could not determine test file path")
	}
	dir := filepath.Dir(filename)
	for {
		if _, err := os.Stat(filepath.Join(dir, "go.mod")); err == nil {
			return dir
		}
		parent := filepath.Dir(dir)
		if parent == dir {
			t.Fatal("could not find repo root (no go.mod found)")
		}
		dir = parent
	}
}

func TestEmbeddedWorkflowMatchesRepoFile(t *testing.T) {
	root := repoRoot(t)
	onDisk, err := os.ReadFile(filepath.Join(root, ".github", "workflows", "codecanary.yml"))
	if err != nil {
		t.Fatalf("reading .github/workflows/codecanary.yml: %v", err)
	}
	if canonicalWorkflow != string(onDisk) {
		t.Errorf("embedded canonicalWorkflow differs from .github/workflows/codecanary.yml\n"+
			"Update internal/setup/codecanary.yml to match .github/workflows/codecanary.yml "+
			"(or vice versa). embedded=%d bytes, on-disk=%d bytes",
			len(canonicalWorkflow), len(onDisk))
	}
}

func TestGenerateWorkflow_CanaryRoundTrip(t *testing.T) {
	got, err := GenerateWorkflow("CODECANARY_PROVIDER_SECRET", "canary")
	if err != nil {
		t.Fatalf("GenerateWorkflow() error: %v", err)
	}
	// Canary with the default secret should reproduce the canonical file exactly.
	if got != canonicalWorkflow {
		t.Errorf("GenerateWorkflow(CODECANARY_PROVIDER_SECRET, canary) does not reproduce "+
			"the canonical workflow. got=%d bytes, want=%d bytes",
			len(got), len(canonicalWorkflow))
	}
}

func TestGenerateWorkflow_Stable(t *testing.T) {
	got, err := GenerateWorkflow("CODECANARY_PROVIDER_SECRET", "v1")
	if err != nil {
		t.Fatalf("GenerateWorkflow() error: %v", err)
	}
	if !strings.Contains(got, "alansikora/codecanary@v1") {
		t.Error("expected action ref @v1")
	}
	if strings.Contains(got, "alansikora/codecanary@canary") {
		t.Error("should not contain @canary for stable")
	}
}

func TestGenerateWorkflow_CustomSecret(t *testing.T) {
	got, err := GenerateWorkflow("MY_API_KEY", "v1")
	if err != nil {
		t.Fatalf("GenerateWorkflow() error: %v", err)
	}
	if !strings.Contains(got, "secrets.MY_API_KEY") {
		t.Error("expected custom secret name in output")
	}
	if strings.Contains(got, "CODECANARY_PROVIDER_SECRET") {
		t.Error("should not contain default secret name when custom is specified")
	}
}

func TestGenerateWorkflow_InvalidInputs(t *testing.T) {
	tests := []struct {
		name      string
		secret    string
		actionRef string
	}{
		{"empty secret", "", "v1"},
		{"lowercase secret", "my_secret", "v1"},
		{"empty ref", "MY_SECRET", ""},
		{"invalid ref chars", "MY_SECRET", "v1/foo"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			_, err := GenerateWorkflow(tt.secret, tt.actionRef)
			if err == nil {
				t.Error("expected error for invalid input")
			}
		})
	}
}

// templateFingerprint pins the embedded workflow template at the version
// in templateFingerprintVersion. It is the SHA-256 of the template with
// the `# codecanary-workflow: v<N>` marker line removed.
//
// When this test fails you changed the template: bump the marker in
// internal/setup/codecanary.yml (and .github/workflows/codecanary.yml) to
// the next version, then update both constants below. The bump is what
// makes the review bot tell consumer repos their copy is outdated.
const (
	templateFingerprintVersion = 1
	templateFingerprint        = "899fc0cc1a02121d1f210d6098eb226188a12191be05bb01a7cdea38bc060353"
)

var markerLine = regexp.MustCompile(`(?m)^[ \t]*#[ \t]*codecanary-workflow:[ \t]*v\d+[ \t]*\n`)

func TestTemplateVersionBumpedOnChange(t *testing.T) {
	v := TemplateVersion()
	if v < 1 {
		t.Fatalf("embedded template has no `# codecanary-workflow: v<N>` marker")
	}
	sum := sha256.Sum256([]byte(markerLine.ReplaceAllString(canonicalWorkflow, "")))
	got := hex.EncodeToString(sum[:])
	switch {
	case v == templateFingerprintVersion && got != templateFingerprint:
		t.Errorf("workflow template changed but its marker is still v%d: bump it to v%d "+
			"in both workflow files, then set templateFingerprintVersion = %d and "+
			"templateFingerprint = %q", v, v+1, v+1, got)
	case v != templateFingerprintVersion:
		t.Errorf("template marker is v%d: set templateFingerprintVersion = %d and "+
			"templateFingerprint = %q", v, v, got)
	}
}

func TestGenerateWorkflow_KeepsTemplateMarker(t *testing.T) {
	got, err := GenerateWorkflow("MY_API_KEY", "v1")
	if err != nil {
		t.Fatalf("GenerateWorkflow() error: %v", err)
	}
	if !strings.Contains(got, "# codecanary-workflow: v") {
		t.Error("generated workflow lost the template version marker")
	}
}
