package review

import (
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
)

// writeFile creates dir and writes a file under root with the given relative
// path and content. Fails the test on any error.
func writeFile(t *testing.T, root, relPath, content string) {
	t.Helper()
	abs := filepath.Join(root, relPath)
	if err := os.MkdirAll(filepath.Dir(abs), 0o755); err != nil {
		t.Fatalf("mkdir %q: %v", abs, err)
	}
	if err := os.WriteFile(abs, []byte(content), 0o644); err != nil {
		t.Fatalf("write %q: %v", abs, err)
	}
}

func TestReadProjectDocs_RootOnlyWhenNoPRFiles(t *testing.T) {
	root := t.TempDir()
	writeFile(t, root, "CLAUDE.md", "root guidance")
	writeFile(t, root, "apps/exchange-api/CLAUDE.md", "exchange-api guidance")

	docs := readProjectDocsFrom(root, nil)

	if _, ok := docs["CLAUDE.md"]; !ok {
		t.Errorf("expected root CLAUDE.md, got: %v", keys(docs))
	}
	if _, ok := docs[filepath.Join("apps", "exchange-api", "CLAUDE.md")]; ok {
		t.Errorf("did not expect nested CLAUDE.md when prFiles empty: %v", keys(docs))
	}
}

func TestReadProjectDocs_LoadsAncestorsOfChangedFiles(t *testing.T) {
	root := t.TempDir()
	writeFile(t, root, "CLAUDE.md", "root guidance")
	writeFile(t, root, "apps/exchange-api/CLAUDE.md", "exchange-api vanilla rails")
	writeFile(t, root, "apps/backoffice-frontend/CLAUDE.md", "react conventions")
	writeFile(t, root, "engines/exchange/CLAUDE.md", "exchange engine conventions")

	// A PR that only touches exchange-api.
	prFiles := []string{
		"apps/exchange-api/app/services/document_update_request_service.rb",
		"apps/exchange-api/test/services/document_update_request_service_test.rb",
	}

	docs := readProjectDocsFrom(root, prFiles)

	want := map[string]bool{
		"CLAUDE.md": true,
		filepath.Join("apps", "exchange-api", "CLAUDE.md"): true,
	}
	for path := range want {
		if _, ok := docs[path]; !ok {
			t.Errorf("expected %q, got: %v", path, keys(docs))
		}
	}

	// Sibling app docs must not leak in.
	for _, unwanted := range []string{
		filepath.Join("apps", "backoffice-frontend", "CLAUDE.md"),
		filepath.Join("engines", "exchange", "CLAUDE.md"),
	} {
		if _, ok := docs[unwanted]; ok {
			t.Errorf("did not expect %q (sibling/unrelated) in: %v", unwanted, keys(docs))
		}
	}
}

func TestReadProjectDocs_FallsBackToShallowerCLAUDEmd(t *testing.T) {
	root := t.TempDir()
	writeFile(t, root, "CLAUDE.md", "root")
	writeFile(t, root, "apps/CLAUDE.md", "all apps share this")
	// No apps/exchange-api/CLAUDE.md — discovery should still pick up apps/.

	docs := readProjectDocsFrom(root, []string{"apps/exchange-api/foo.rb"})

	if _, ok := docs[filepath.Join("apps", "CLAUDE.md")]; !ok {
		t.Errorf("expected apps/CLAUDE.md as ancestor fallback, got: %v", keys(docs))
	}
}

func TestReadProjectDocs_SkipsVendoredOrHiddenAncestors(t *testing.T) {
	root := t.TempDir()
	writeFile(t, root, "CLAUDE.md", "root")
	writeFile(t, root, "vendor/lib/CLAUDE.md", "vendored, should be ignored")
	writeFile(t, root, ".github/workflows/CLAUDE.md", "dotfile, should be ignored")
	writeFile(t, root, "node_modules/foo/CLAUDE.md", "deps, should be ignored")

	prFiles := []string{
		"vendor/lib/bar.go",
		".github/workflows/ci.yml",
		"node_modules/foo/index.js",
	}

	docs := readProjectDocsFrom(root, prFiles)

	if len(docs) != 1 {
		t.Errorf("expected only root CLAUDE.md, got: %v", keys(docs))
	}
	if _, ok := docs["CLAUDE.md"]; !ok {
		t.Errorf("root CLAUDE.md missing: %v", keys(docs))
	}
}

func TestReadProjectDocs_RespectsPerFileCap(t *testing.T) {
	root := t.TempDir()
	large := strings.Repeat("x", maxDocBytes*2) // 32 KB, double the cap.
	writeFile(t, root, "CLAUDE.md", large)

	docs := readProjectDocsFrom(root, nil)

	got := docs["CLAUDE.md"]
	if len(got) > maxDocBytes+len("\n... (truncated)") {
		t.Errorf("expected truncation to %d bytes + marker, got %d", maxDocBytes, len(got))
	}
	if !strings.HasSuffix(got, "(truncated)") {
		t.Errorf("expected truncation marker, got tail: %q", tail(got, 40))
	}
}

func TestReadProjectDocs_RespectsTotalCap(t *testing.T) {
	root := t.TempDir()
	// Four docs at 16 KB each = 64 KB total; total cap is 48 KB, so the
	// fourth should be dropped once the budget is exhausted.
	content := strings.Repeat("x", maxDocBytes)
	writeFile(t, root, "CLAUDE.md", content)
	writeFile(t, root, "apps/CLAUDE.md", content)
	writeFile(t, root, "apps/a/CLAUDE.md", content)
	writeFile(t, root, "apps/a/b/CLAUDE.md", content)

	docs := readProjectDocsFrom(root, []string{"apps/a/b/c.rb"})

	total := 0
	for _, v := range docs {
		total += len(v)
	}
	if total > maxTotalDocBytes {
		t.Errorf("total bytes %d exceeds cap %d: loaded %v", total, maxTotalDocBytes, keys(docs))
	}
}

func TestAncestorDirs_ShallowestFirstWithStableOrder(t *testing.T) {
	got := ancestorDirs([]string{
		"apps/exchange-api/app/services/foo.rb",
		"engines/exchange/lib/bar.rb",
		"apps/exchange-api/app/services/baz.rb", // same chain as first
	})
	want := []string{
		"apps",
		"engines",
		"apps/exchange-api",
		"engines/exchange",
		"apps/exchange-api/app",
		"engines/exchange/lib",
		"apps/exchange-api/app/services",
	}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("ancestor order mismatch\n got: %v\nwant: %v", got, want)
	}
}

func keys(m map[string]string) []string {
	out := make([]string, 0, len(m))
	for k := range m {
		out = append(out, k)
	}
	return out
}

func tail(s string, n int) string {
	if len(s) <= n {
		return s
	}
	return s[len(s)-n:]
}
