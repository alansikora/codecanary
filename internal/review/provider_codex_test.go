package review

import (
	"errors"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
)

func TestCodexFixArgs(t *testing.T) {
	p := &codexProvider{model: "gpt-5.5", effort: fixEffort, editFiles: true}
	d := codexDirs{Home: "/run/home", Tmp: "/run/tmp"}
	args := p.buildArgs("/repo/root", "/opt/codex", d)
	if args[0] != "exec" || args[len(args)-1] != "-" {
		t.Fatalf("want `exec ... -` (prompt on stdin), got %v", args)
	}
	joined := strings.Join(args, " ")
	for _, want := range []string{
		"--json", "--ephemeral", "--ignore-user-config", "--ignore-rules", "--strict-config", "-C /repo/root", "--model gpt-5.5",
		`default_permissions="codecanary_fix"`,
		"permissions.codecanary_fix.network={enabled=false}",
		`approval_policy="never"`,
		`web_search="disabled"`,
		`projects={"/repo/root"={trust_level="untrusted"}}`,
		"project_doc_max_bytes=0",
		`shell_environment_policy.inherit="core"`,
		`"TMPDIR"="/run/tmp"`,
		`model_reasoning_effort="medium"`,
	} {
		if !strings.Contains(joined, want) {
			t.Errorf("args lack %q:\n%s", want, joined)
		}
	}
	for _, f := range codexDisabledFeatures {
		if !strings.Contains(joined, "--disable "+f) {
			t.Errorf("feature %s not disabled", f)
		}
	}
	for _, a := range args {
		if strings.Contains(a, "danger") || strings.Contains(a, "bypass") || strings.HasPrefix(a, "--sandbox") {
			t.Errorf("unconfined flag %q", a)
		}
	}
}

func TestCodexFilesystemRules(t *testing.T) {
	rules := codexFilesystemRules("/opt/codex", codexDirs{Home: "/run/home", Tmp: "/run/tmp"})
	got := map[string]string{}
	var workspace string
	for _, r := range rules {
		got[r.Key] = r.Value
		if r.Key == ":workspace_roots" {
			workspace = r.Value
		}
	}
	for path, want := range map[string]string{
		":minimal":   `"read"`,
		":slash_tmp": `"deny"`,
		":tmpdir":    `"deny"`,
		"/opt/codex": `"read"`,
		"/run/home":  `"deny"`,
		"/run/tmp":   `"write"`,
		"~/.ssh":     `"deny"`,
	} {
		if got[path] != want {
			t.Errorf("%s = %s, want %s", path, got[path], want)
		}
	}
	// On Linux a deny hides everything under it, including the CLI's own
	// standalone install in ~/.codex.
	if _, ok := got["~/.codex"]; ok {
		t.Error("~/.codex denied: the sandbox helper could not run")
	}
	if _, ok := got[":root"]; ok {
		t.Error("the profile grants the whole filesystem")
	}
	for _, want := range []string{`"."="write"`, `".git"="deny"`, `".env"="deny"`, `".env.*"="deny"`} {
		if !strings.Contains(workspace, want) {
			t.Errorf("workspace rules lack %s: %s", want, workspace)
		}
	}
	// Every protected path is read-only in the sandbox, not only reverted.
	for _, p := range protectedWritePaths {
		p = strings.TrimSuffix(p, "/**")
		if p == ".git" {
			continue
		}
		if !strings.Contains(workspace, `"`+p+`"="read"`) {
			t.Errorf("protected path %s is writable in the sandbox: %s", p, workspace)
		}
	}
}

func TestCodexEnv(t *testing.T) {
	env := codexEnv([]string{
		"PATH=/bin", "HOME=/home/runner",
		"CODECANARY_PROVIDER_SECRET=sk", "GH_TOKEN=ghs", "GITHUB_TOKEN=ghs", "CODECANARY_GITHUB_TOKEN=ghs",
		"OPENAI_API_KEY=sk", "CODEX_API_KEY=sk", "CODEX_HOME=/elsewhere", "CLAUDE_CODE_OAUTH_TOKEN=x",
	}, "/run/home")
	want := []string{"PATH=/bin", "HOME=/home/runner", "CODEX_HOME=/run/home"}
	if !slices.Equal(env, want) {
		t.Errorf("env = %v, want %v", env, want)
	}
}

func TestParseCodexEvents(t *testing.T) {
	ok := `{"type":"thread.started","thread_id":"t"}
not json
{"type":"error","message":"Reconnecting... 1/5 (stream disconnected)"}
{"type":"item.completed","item":{"id":"i0","type":"reasoning","text":"thinking"}}
{"type":"item.completed","item":{"id":"i1","type":"agent_message","text":"first"}}
{"type":"item.completed","item":{"id":"i2","type":"agent_message","text":"answer"}}
{"type":"turn.completed","usage":{"input_tokens":1000,"cached_input_tokens":800,"output_tokens":50}}`
	res, err := parseCodexEvents([]byte(ok))
	if err != nil {
		t.Fatalf("a retried run failed: %v", err)
	}
	if res.Text != "answer" || res.Usage.InputTokens != 200 || res.Usage.CacheReadTokens != 800 || res.Usage.OutputTokens != 50 {
		t.Errorf("result = %+v", res)
	}

	failed := `{"type":"error","message":"Reconnecting... 5/5 (unexpected status 401 Unauthorized: Incorrect API key)"}
{"type":"turn.failed","error":{"message":"unexpected status 401 Unauthorized: Incorrect API key"}}`
	_, err = parseCodexEvents([]byte(failed))
	var pe *ProviderError
	if !errors.As(err, &pe) || pe.Kind != ErrorKindAuth || pe.Provider != "codex" {
		t.Errorf("failed turn = %v, want a codex auth ProviderError", err)
	}

	_, err = parseCodexEvents([]byte(`{"type":"error","message":"unexpected status 429 Too Many Requests"}`))
	if !errors.As(err, &pe) || pe.Kind != ErrorKindRateLimit {
		t.Errorf("error without an answer = %v, want a rate-limit ProviderError", err)
	}
}

func TestResolveCodexBinary(t *testing.T) {
	dir := t.TempDir()
	bin := filepath.Join(dir, "releases", "1.0", "bin", "codex")
	if err := os.MkdirAll(filepath.Dir(bin), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(bin, nil, 0o755); err != nil {
		t.Fatal(err)
	}
	link := filepath.Join(dir, "codex")
	if err := os.Symlink(bin, link); err != nil {
		t.Fatal(err)
	}
	real, root, err := resolveCodexBinary(link)
	if err != nil {
		t.Fatal(err)
	}
	wantReal, _ := filepath.EvalSymlinks(bin)
	if real != wantReal || root != filepath.Dir(filepath.Dir(wantReal)) {
		t.Errorf("resolved %q, root %q", real, root)
	}
}

func TestCodexRunsOnlyTheFix(t *testing.T) {
	p := NewProviderForRole(&ModelConfig{Provider: "codex"}, nil)
	if _, err := p.Run(t.Context(), "review this", RunOpts{}); err == nil {
		t.Error("codex ran without EditFiles")
	}
}

func TestFixProviderSelection(t *testing.T) {
	if got := fixProviderNames(); !slices.Equal(got, []string{"claude", "codex"}) {
		t.Errorf("fix providers = %v", got)
	}
	if slices.Contains(reviewProviderNames(), "codex") {
		t.Error("codex is offered as a review provider")
	}
	for _, name := range []string{"anthropic", "openai", "openrouter", "grok"} {
		if fixCapability(name) != nil {
			t.Errorf("%s claims it can edit files", name)
		}
	}
	claude, ok := NewProviderForRole(&ModelConfig{Provider: "claude", EditFiles: true}, nil).(*claudeCLIProvider)
	if !ok {
		t.Fatal("claude fix provider is not the Claude CLI")
	}
	if gitRepoRoot() != "" && !claude.writable {
		t.Error("claude fix provider can't edit")
	}
	codex, ok := NewProviderForRole(&ModelConfig{Provider: "codex", Model: "gpt-5.5", EditFiles: true, Effort: fixEffort}, nil).(*codexProvider)
	if !ok || !codex.editFiles || codex.model != "gpt-5.5" || codex.effort != fixEffort {
		t.Errorf("codex fix provider = %+v", codex)
	}
}

func TestCodexWorkBase(t *testing.T) {
	runnerTemp := filepath.Join(t.TempDir(), "_temp")
	t.Setenv("RUNNER_TEMP", runnerTemp)
	t.Setenv("TMPDIR", filepath.Join(t.TempDir(), "elsewhere"))
	if base, err := codexWorkBase(); err != nil || base != runnerTemp {
		t.Errorf("base = %q, %v; want RUNNER_TEMP", base, err)
	}
	// Inside the denied temp directory the sandbox can't start.
	t.Setenv("TMPDIR", filepath.Dir(runnerTemp))
	if _, err := codexWorkBase(); err == nil {
		t.Error("a working directory inside TMPDIR was accepted")
	}
}

func TestIsWithin(t *testing.T) {
	for _, tc := range []struct {
		path, dir string
		want      bool
	}{
		{"/a/b", "/a", true},
		{"/a", "/a", true},
		{"/ab", "/a", false},
		{"/b", "/a", false},
	} {
		if got := isWithin(tc.path, tc.dir); got != tc.want {
			t.Errorf("isWithin(%q, %q) = %t", tc.path, tc.dir, got)
		}
	}
}
