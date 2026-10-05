package review

import (
	"encoding/json"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"github.com/alansikora/codecanary/internal/credentials"
)

// argValue returns the value following the named flag, or "" if missing.
// Test-only helper for asserting on managed flags built by buildArgs.
func argValue(args []string, flag string) string {
	for i, a := range args {
		if a == flag && i+1 < len(args) {
			return args[i+1]
		}
		if strings.HasPrefix(a, flag+"=") {
			return strings.TrimPrefix(a, flag+"=")
		}
	}
	return ""
}

// countFlag returns how many times flag appears as its own argv element.
func countFlag(args []string, flag string) int {
	n := 0
	for _, a := range args {
		if a == flag {
			n++
		}
	}
	return n
}

// settingsArg decodes the --settings JSON built by buildArgs.
func settingsArg(t *testing.T, args []string) map[string]any {
	t.Helper()
	raw := argValue(args, "--settings")
	if raw == "" {
		t.Fatalf("--settings missing from args %q", args)
	}
	var s map[string]any
	if err := json.Unmarshal([]byte(raw), &s); err != nil {
		t.Fatalf("--settings is not JSON: %v (%s)", err, raw)
	}
	return s
}

// newToolProvider builds a review provider with tools on, bypassing the
// gitRepoRoot lookup so the test does not depend on the cwd.
func newToolProvider(t *testing.T, extraArgs []string) *claudeCLIProvider {
	t.Helper()
	mc := &ModelConfig{Provider: "claude", Model: "sonnet", ClaudeArgs: extraArgs}
	p := newClaudeCLIProvider(mc, []string{"PATH=/usr/bin"}).(*claudeCLIProvider)
	p.enableReviewTools("Read,Grep,Glob", "/repo/root")
	return p
}

// Default behaviour: `--tools ""` disables all built-in tools, preserving
// the historical single-shot CLI invocation. No regression when the caller
// doesn't set claude_review_tools.
func TestClaudeProvider_DefaultDisablesTools(t *testing.T) {
	mc := &ModelConfig{Provider: "claude", Model: "sonnet"}
	p := newClaudeCLIProvider(mc, []string{"PATH=/usr/bin"}).(*claudeCLIProvider)
	args := p.buildArgs(RunOpts{})
	// Assert the flag is actually present before checking its value:
	// argValue returns "" both for `--tools ""` and for a missing --tools,
	// so a value-only assertion would still pass if the flag were dropped —
	// and a dropped --tools means the CLI falls back to its own default,
	// which enables tools rather than disabling them.
	if !slices.Contains(args, "--tools") {
		t.Fatalf("--tools must be passed explicitly to disable tools; got args %q", args)
	}
	if got := argValue(args, "--tools"); got != "" {
		t.Errorf("--tools should be empty by default, got %q", got)
	}
	// Tool-use confinement is only for tool runs.
	for _, f := range []string{"--restricted", "--permission-mode"} {
		if slices.Contains(args, f) {
			t.Errorf("%s should not be passed without tools; got %q", f, args)
		}
	}
	if _, ok := settingsArg(t, args)["permissions"]; ok {
		t.Errorf("no permission rules expected without tools")
	}
	if p.workDir != "" {
		t.Errorf("workDir should be empty without tools, got %q", p.workDir)
	}
}

// Every invocation — review and triage, tools or not — must keep the PR
// checkout's own Claude Code config (hooks, env, .mcp.json) from loading.
func TestClaudeProvider_IsolatesProjectConfigAlways(t *testing.T) {
	cases := map[string]*claudeCLIProvider{
		"no tools":   newClaudeCLIProvider(&ModelConfig{Provider: "claude", Model: "haiku"}, nil).(*claudeCLIProvider),
		"with tools": newToolProvider(t, nil),
	}
	for name, p := range cases {
		t.Run(name, func(t *testing.T) {
			args := p.buildArgs(RunOpts{})
			if got := argValue(args, "--setting-sources"); got != "user" {
				t.Errorf("--setting-sources = %q, want user", got)
			}
			if !slices.Contains(args, "--strict-mcp-config") {
				t.Errorf("--strict-mcp-config missing: %q", args)
			}
			if v, _ := settingsArg(t, args)["disableAllHooks"].(bool); !v {
				t.Errorf("disableAllHooks must be true in --settings")
			}
		})
	}
}

// When claude_review_tools is set, the provider passes the allowlist through
// to `--tools` and confines file access: restricted mode, dontAsk, reads
// blocked outside the working directory, explicit deny rules, and the CLI run
// from the repository root.
func TestClaudeProvider_ReviewToolsConfined(t *testing.T) {
	p := newToolProvider(t, nil)
	if p.workDir != "/repo/root" {
		t.Errorf("workDir = %q, want the repo root", p.workDir)
	}
	args := p.buildArgs(RunOpts{})
	if got := argValue(args, "--tools"); got != "Read,Grep,Glob" {
		t.Errorf("--tools = %q, want %q", got, "Read,Grep,Glob")
	}
	// `--tools` is in claudeReservedArgs, so the user can't smuggle a
	// conflicting value via claude_args. Verify exactly one --tools occurs.
	if n := countFlag(args, "--tools"); n != 1 {
		t.Errorf("expected exactly one --tools flag, got %d (%v)", n, args)
	}
	if !slices.Contains(args, "--restricted") {
		t.Errorf("--restricted missing: %q", args)
	}
	if got := argValue(args, "--permission-mode"); got != "dontAsk" {
		t.Errorf("--permission-mode = %q, want dontAsk", got)
	}
	if n := countFlag(args, "--settings"); n != 1 {
		t.Errorf("expected exactly one --settings flag, got %d", n)
	}

	perms, ok := settingsArg(t, args)["permissions"].(map[string]any)
	if !ok {
		t.Fatalf("permissions missing from --settings")
	}
	if v, _ := perms["blockReadsOutsideWorkingDirectories"].(bool); !v {
		t.Errorf("blockReadsOutsideWorkingDirectories must be true")
	}
	rawDeny, _ := perms["deny"].([]any)
	var deny []string
	for _, d := range rawDeny {
		deny = append(deny, d.(string))
	}
	for _, want := range []string{
		"Read(//proc/**)", "Read(//etc/**)", "Read(~/.claude/**)", "Read(~/.claude.json)",
		"Read(~/.config/**)", "Read(~/.ssh/**)", "Read(.git/**)", "Read(.env)",
	} {
		if !slices.Contains(deny, want) {
			t.Errorf("deny rules missing %q (got %v)", want, deny)
		}
	}
	// A blanket home deny would also block the checkout on GitHub-hosted
	// runners (/home/runner/work/...): deny always wins over allow.
	for _, d := range deny {
		if d == "Read(~/**)" || d == "Read(~)" {
			t.Errorf("deny rule %q would hide the repository itself", d)
		}
	}
}

// Advisor settings and confinement share the single --settings argument.
func TestClaudeProvider_ReviewToolsWithAdvisorMergesSettings(t *testing.T) {
	mc := &ModelConfig{Provider: "claude", Model: "sonnet", AdvisorModel: "opus"}
	p := newClaudeCLIProvider(mc, []string{"PATH=/usr/bin"}).(*claudeCLIProvider)
	p.enableReviewTools("Read", "/repo/root")
	s := settingsArg(t, p.buildArgs(RunOpts{}))
	if s["advisorModel"] != "opus" {
		t.Errorf("advisorModel lost: %v", s)
	}
	if _, ok := s["permissions"]; !ok {
		t.Errorf("permissions lost when advisor is set: %v", s)
	}
}

// Tool use fails closed: no confinement root, or a user --settings that would
// replace the deny rules, keeps the review tool-less.
func TestClaudeProvider_ReviewToolsFailClosed(t *testing.T) {
	t.Run("no repo root", func(t *testing.T) {
		mc := &ModelConfig{Provider: "claude", Model: "sonnet"}
		p := newClaudeCLIProvider(mc, nil).(*claudeCLIProvider)
		p.enableReviewTools("Read,Grep,Glob", "")
		if p.reviewTools != "" || p.workDir != "" {
			t.Errorf("tools enabled without a repo root: %q in %q", p.reviewTools, p.workDir)
		}
		if got := argValue(p.buildArgs(RunOpts{}), "--tools"); got != "" {
			t.Errorf("--tools = %q, want empty", got)
		}
	})
	t.Run("user settings", func(t *testing.T) {
		p := newToolProvider(t, []string{`--settings={"x":1}`})
		if p.reviewTools != "" {
			t.Errorf("tools enabled despite user --settings")
		}
		args := p.buildArgs(RunOpts{})
		if slices.Contains(args, "--restricted") || argValue(args, "--tools") != "" {
			t.Errorf("expected tool-less args, got %q", args)
		}
		// Project config is still isolated even though our settings JSON is
		// replaced by the operator's.
		if argValue(args, "--setting-sources") != "user" {
			t.Errorf("--setting-sources must still be passed: %q", args)
		}
	})
}

// The review provider gets tools; a triage provider built from the same
// config does not (runner only sets ClaudeReviewTools on reviewMC).
func TestClaudeProvider_TriageStaysToolless(t *testing.T) {
	triage := newClaudeCLIProvider(&ModelConfig{Provider: "claude", Model: "haiku"}, nil).(*claudeCLIProvider)
	if got := argValue(triage.buildArgs(RunOpts{}), "--tools"); got != "" {
		t.Errorf("triage --tools = %q, want empty", got)
	}
}

// Secrets the CLI never reads are stripped from its environment; the OAuth
// token it authenticates with is kept. The caller's slice is not mutated.
func TestClaudeProvider_ScrubsEnv(t *testing.T) {
	env := []string{
		"PATH=/usr/bin",
		credentials.EnvVar + "=secret-value",
		"GITHUB_TOKEN=ghs_x",
		"GH_TOKEN=ghs_y",
		"CODECANARY_GITHUB_TOKEN=ghs_z",
		"GITHUB_REPOSITORY=o/r",
	}
	orig := slices.Clone(env)
	p := newClaudeCLIProvider(&ModelConfig{Provider: "claude", Model: "sonnet"}, env).(*claudeCLIProvider)
	if !slices.Equal(env, orig) {
		t.Errorf("caller env mutated: %v", env)
	}
	if !slices.Contains(p.env, claudeOAuthEnvVar+"=secret-value") {
		t.Errorf("OAuth token not injected: %v", p.env)
	}
	if !slices.Contains(p.env, "GITHUB_REPOSITORY=o/r") || !slices.Contains(p.env, "PATH=/usr/bin") {
		t.Errorf("non-secret vars dropped: %v", p.env)
	}
	for _, e := range p.env {
		key, _, _ := strings.Cut(e, "=")
		if claudeScrubbedEnvKeys[key] {
			t.Errorf("%s should be scrubbed from the claude env", key)
		}
	}
}

// With tools on, the CLI runs from the repo root, so a relative claude_path
// such as ./bin/claude must be made absolute against codecanary's cwd first.
// Bare names stay on PATH lookup.
func TestAbsIfRelativePath(t *testing.T) {
	cwd, err := os.Getwd()
	if err != nil {
		t.Fatal(err)
	}
	if got, want := absIfRelativePath("./bin/claude"), filepath.Join(cwd, "bin", "claude"); got != want {
		t.Errorf("relative path = %q, want %q", got, want)
	}
	for _, p := range []string{"claude", "/usr/local/bin/claude"} {
		if got := absIfRelativePath(p); got != p {
			t.Errorf("absIfRelativePath(%q) = %q, want unchanged", p, got)
		}
	}
}
