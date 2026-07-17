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

func TestSplitFrontmatter(t *testing.T) {
	meta, body := splitFrontmatter("---\ndescription: API rules\npaths:\n  - \"apps/api/**\"\n---\nUse the auth middleware.\n")
	if meta.Description != "API rules" {
		t.Errorf("description = %q, want %q", meta.Description, "API rules")
	}
	if !reflect.DeepEqual(meta.Paths, []string{"apps/api/**"}) {
		t.Errorf("paths = %v, want [apps/api/**]", meta.Paths)
	}
	if strings.TrimSpace(body) != "Use the auth middleware." {
		t.Errorf("body = %q, want %q", body, "Use the auth middleware.")
	}
}

func TestSplitFrontmatter_NoFrontmatter(t *testing.T) {
	meta, body := splitFrontmatter("Just a plain rule body.\n")
	if meta.Description != "" || len(meta.Paths) != 0 {
		t.Errorf("expected empty meta, got %+v", meta)
	}
	if body != "Just a plain rule body.\n" {
		t.Errorf("body = %q, want whole content", body)
	}
}

func TestSplitFrontmatter_Unterminated(t *testing.T) {
	content := "---\ndescription: oops\nno closing fence"
	meta, body := splitFrontmatter(content)
	if meta.Description != "" {
		t.Errorf("expected no meta for unterminated frontmatter, got %+v", meta)
	}
	if body != content {
		t.Errorf("expected whole content as body, got %q", body)
	}
}

func TestSplitFrontmatter_DashesInBodyNotTreatedAsFence(t *testing.T) {
	// A markdown horizontal rule (----) and a --- line in the body must not be
	// mistaken for the closing fence.
	content := "---\ndescription: rule\n---\nIntro paragraph.\n\n----\n\nMore text after an hr.\n"
	meta, body := splitFrontmatter(content)
	if meta.Description != "rule" {
		t.Errorf("description = %q, want %q", meta.Description, "rule")
	}
	if !strings.Contains(body, "Intro paragraph.") || !strings.Contains(body, "More text after an hr.") {
		t.Errorf("body was split at the wrong fence: %q", body)
	}
}

func TestSplitFrontmatter_DashValueNotTreatedAsFence(t *testing.T) {
	// A YAML value of "---" on its own indented line is inside the frontmatter,
	// but the fence detector keys on the trimmed line == "---". Guard the common
	// case where a value line like "sep: ---" must not end the frontmatter.
	content := "---\ndescription: rule\nsep: \"---\"\npaths:\n  - \"**/*.go\"\n---\nBody.\n"
	meta, body := splitFrontmatter(content)
	if len(meta.Paths) != 1 || meta.Paths[0] != "**/*.go" {
		t.Errorf("frontmatter ended early; paths = %v", meta.Paths)
	}
	if strings.TrimSpace(body) != "Body." {
		t.Errorf("body = %q, want %q", body, "Body.")
	}
}

func TestReadClaudeRules_ScopeIn(t *testing.T) {
	root := t.TempDir()
	writeFile(t, root, filepath.Join(".claude", "rules", "api.md"),
		"---\ndescription: API rules\npaths:\n  - \"apps/api/**\"\n---\nUse the auth middleware.\n")

	rules := readClaudeRulesFrom(root, []string{"apps/api/handler.go"})

	key := filepath.Join(".claude", "rules", "api.md")
	content, ok := rules[key]
	if !ok {
		t.Fatalf("expected rule %q to be included, got %v", key, keys(rules))
	}
	if !strings.Contains(content, "API rules") || !strings.Contains(content, "auth middleware") {
		t.Errorf("content missing description or body: %q", content)
	}
}

func TestReadClaudeRules_ScopeOut(t *testing.T) {
	root := t.TempDir()
	writeFile(t, root, filepath.Join(".claude", "rules", "api.md"),
		"---\npaths:\n  - \"apps/api/**\"\n---\nUse the auth middleware.\n")

	rules := readClaudeRulesFrom(root, []string{"apps/web/page.tsx"})
	if len(rules) != 0 {
		t.Errorf("expected no rules for non-matching change, got %v", keys(rules))
	}
}

func TestReadClaudeRules_NoPathsAlwaysIncluded(t *testing.T) {
	root := t.TempDir()
	writeFile(t, root, filepath.Join(".claude", "rules", "global.md"),
		"---\ndescription: global rule\n---\nAlways applies.\n")

	rules := readClaudeRulesFrom(root, []string{"anything/at/all.go"})
	if len(rules) != 1 {
		t.Fatalf("expected 1 rule (no paths => always), got %v", keys(rules))
	}
}

func TestReadClaudeRules_NoFrontmatterAlwaysIncluded(t *testing.T) {
	root := t.TempDir()
	writeFile(t, root, filepath.Join(".claude", "rules", "plain.md"),
		"No frontmatter here, just conventions.\n")

	rules := readClaudeRulesFrom(root, []string{"src/x.go"})
	if len(rules) != 1 {
		t.Fatalf("expected plain rule to always be included, got %v", keys(rules))
	}
}

func TestReadClaudeRules_PerFileTruncation(t *testing.T) {
	root := t.TempDir()
	writeFile(t, root, filepath.Join(".claude", "rules", "big.md"), strings.Repeat("x", maxRuleBytes+500))

	rules := readClaudeRulesFrom(root, []string{"src/x.go"})
	content := rules[filepath.Join(".claude", "rules", "big.md")]
	if !strings.HasSuffix(content, "(truncated)") {
		t.Errorf("expected truncation marker in oversized rule")
	}
	if len(content) > maxRuleBytes+len("\n... (truncated)") {
		t.Errorf("content not truncated: %d bytes", len(content))
	}
}

func TestReadClaudeRules_TotalBudget(t *testing.T) {
	root := t.TempDir()
	chunk := strings.Repeat("y", maxRuleBytes-100)
	for _, n := range []string{"a.md", "b.md", "c.md", "d.md", "e.md", "f.md"} {
		writeFile(t, root, filepath.Join(".claude", "rules", n), chunk)
	}

	rules := readClaudeRulesFrom(root, []string{"src/x.go"})
	total := 0
	for _, c := range rules {
		total += len(c)
	}
	if total > maxTotalRuleBytes {
		t.Errorf("total rule bytes %d exceeds budget %d", total, maxTotalRuleBytes)
	}
	if len(rules) == 0 {
		t.Errorf("expected at least one rule within budget")
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
