package review

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"runtime"
	"strings"
	"time"

	"github.com/alansikora/codecanary/internal/credentials"
)

// The codex provider runs the CI fix with the Codex CLI (`codex exec`). It is
// fix-only: Codex has no tool-less mode a review could use, and its file
// tools are shell commands, so it never reviews.
//
// Codex reads files by running commands, so unlike the Claude fix it has a
// shell. Every command it runs, and every apply_patch edit, goes through
// Codex's OS sandbox (Seatbelt on macOS, bubblewrap on Linux) under a
// permission profile codecanary generates (codexFilesystemRules): reads
// limited to the runtime paths tools need plus the repository, writes to the
// repository only, protectedWritePaths and git metadata read-only or denied,
// no network, and an environment without secrets. The CLI's own surfaces that
// run outside that sandbox (web search, MCP, apps, browser and computer use,
// hooks, plugins, sub-agents) are switched off, and the checkout's .codex/
// layers and AGENTS.md are not loaded. See docs/review-flow.md ("Fix
// providers").
func init() {
	providers["codex"] = ProviderFactory{
		New:      newCodexProvider,
		Validate: validateCodex,
		FixOnly:  true,
		Fix: &FixCapability{
			// Empty: the Codex CLI's own default model.
			Preflight: codexFixPreflight,
		},
	}
}

// codexModelRe keeps flag-shaped and otherwise odd values out of --model;
// the CLI rejects models that don't exist.
var codexModelRe = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9._:/-]*$`)

func validateCodex(mc *ModelConfig) error {
	if mc.Model != "" && !codexModelRe.MatchString(mc.Model) {
		return fmt.Errorf("invalid model %q for codex provider", mc.Model)
	}
	return nil
}

// codexBinary is the Codex CLI the fix runs, looked up on PATH.
const codexBinary = "codex"

// codexProvider implements ModelProvider with the Codex CLI.
type codexProvider struct {
	model     string
	effort    string
	env       []string
	editFiles bool
}

func newCodexProvider(mc *ModelConfig, env []string) ModelProvider {
	return &codexProvider{model: mc.Model, effort: mc.Effort, env: env, editFiles: mc.EditFiles}
}

// codexFixPreflight checks that the Codex CLI is on PATH and that its
// sandbox can start here. Codex warns rather than stops when the sandbox is
// unavailable, so the fix refuses to run instead.
func codexFixPreflight(*ModelConfig) error {
	if _, err := exec.LookPath(codexBinary); err != nil {
		return fmt.Errorf("codex binary not found: %w", err)
	}
	switch runtime.GOOS {
	case "darwin":
		if _, err := exec.LookPath("sandbox-exec"); err != nil {
			return fmt.Errorf("the codex fix needs macOS's sandbox-exec: %w", err)
		}
	case "linux":
		// Codex uses the first bwrap on PATH; the action installs it and lets
		// it create user namespaces (scripts/setup-bwrap.sh).
		if out, err := exec.Command("bwrap", "--ro-bind", "/", "/", "--unshare-user", "--unshare-pid", "true").CombinedOutput(); err != nil {
			return fmt.Errorf("the codex fix needs a working bubblewrap sandbox: %v %s", err, strings.TrimSpace(string(out)))
		}
	default:
		return fmt.Errorf("the codex fix runs only on Linux and macOS, where its sandbox is available")
	}
	return nil
}

// codexDirs are the per-run directories the fix gives Codex.
type codexDirs struct {
	// Home is CODEX_HOME: only the auth file `codex login` writes, so no
	// user config, history or credentials of the machine's own Codex load.
	// Denied to sandboxed commands.
	Home string
	// Tmp is the commands' TMPDIR, the one writable place outside the
	// repository; the shared temp directories are denied.
	Tmp string
}

// codexWorkBase is where a run's codexDirs are made: $RUNNER_TEMP in GitHub
// Actions, else the user cache directory. Never the system temp directory,
// which the profile denies, and inside which bwrap can't mount the run's
// own writable or denied paths.
func codexWorkBase() (string, error) {
	base := os.Getenv("RUNNER_TEMP")
	if base == "" {
		dir, err := os.UserCacheDir()
		if err != nil {
			return "", fmt.Errorf("codex: no directory for the fix's working files: %w", err)
		}
		base = filepath.Join(dir, "codecanary")
	}
	if err := os.MkdirAll(base, 0o700); err != nil {
		return "", err
	}
	for _, tmp := range []string{os.TempDir(), "/tmp"} {
		if isWithin(base, tmp) {
			return "", fmt.Errorf("codex: working directory %s is inside %s, which the fix's sandbox denies", base, tmp)
		}
	}
	return base, nil
}

// isWithin reports whether path is dir or lies under it, symlinks resolved
// (macOS's /tmp is /private/tmp).
func isWithin(path, dir string) bool {
	if p, err := filepath.EvalSymlinks(path); err == nil {
		path = p
	}
	if d, err := filepath.EvalSymlinks(dir); err == nil {
		dir = d
	}
	rel, err := filepath.Rel(dir, path)
	return err == nil && rel != ".." && !strings.HasPrefix(rel, ".."+string(filepath.Separator))
}

// codexProfile names the permission profile the fix generates.
const codexProfile = "codecanary_fix"

// codexDisabledFeatures are Codex features that act outside the command
// sandbox or bring in configuration from elsewhere. --strict-config makes
// the CLI fail, rather than run with defaults, if it stops recognising the
// generated configuration.
var codexDisabledFeatures = []string{
	"apps",
	"browser_use",
	"browser_use_external",
	"computer_use",
	"in_app_browser",
	"multi_agent",
	"plugins",
	"remote_plugin",
	"hooks",
	"image_generation",
	"memories",
	"goals",
	"tool_suggest",
	"skill_mcp_dependency_install",
	"workspace_dependencies",
	"shell_snapshot",
}

// codexHomeDenies are home-directory credential stores, denied explicitly.
// The profile grants no home access to begin with (only :minimal and the
// repository, which on GitHub-hosted runners lives under $HOME), so these
// back it up.
//
// Not ~/.codex: the standalone installer puts the CLI there, and on Linux a
// read rule can't reopen a path inside a denied one (bwrap mounts the deny
// over it). The machine's own Codex auth isn't readable anyway: home is not
// granted, and the fix runs on its own CODEX_HOME.
var codexHomeDenies = []string{
	"~/.claude",
	"~/.codecanary",
	"~/.config",
	"~/.ssh",
	"~/.aws",
	"~/.gnupg",
	"~/.docker",
	"~/.kube",
	"~/.netrc",
	"~/.npmrc",
	"~/.git-credentials",
	"~/.gitconfig",
}

// tomlEntry is one key = value pair of a TOML inline table.
type tomlEntry struct {
	Key   string
	Value string // already TOML-encoded
}

// tomlString encodes s as a TOML basic string. JSON string escaping is a
// subset TOML accepts.
func tomlString(s string) string {
	b, _ := json.Marshal(s)
	return string(b)
}

// tomlInline renders entries as a TOML inline table, keys quoted.
func tomlInline(entries []tomlEntry) string {
	parts := make([]string, len(entries))
	for i, e := range entries {
		parts[i] = tomlString(e.Key) + "=" + e.Value
	}
	return "{" + strings.Join(parts, ", ") + "}"
}

// codexFilesystemRules is the profile's filesystem table. More specific
// paths win, and deny wins over write and read on the same path:
//   - :minimal read: the platform paths common tools need, nothing of $HOME.
//   - /tmp and $TMPDIR denied, so files other steps left there stay unread;
//     the run's own Tmp is writable instead. Its dirs must lie outside both
//     (codexWorkBase): on Linux nothing can be reopened inside a deny.
//   - installRoot read: the Codex CLI re-executes itself as the sandbox
//     helper, wherever it is installed.
//   - the run's CODEX_HOME and codexHomeDenies denied.
//   - in the repository: writable, except .git and dotenv files (denied, as
//     for the Claude fix) and protectedWritePaths (read-only).
func codexFilesystemRules(installRoot string, d codexDirs) []tomlEntry {
	read, write, deny := tomlString("read"), tomlString("write"), tomlString("deny")
	rules := []tomlEntry{
		{":minimal", read},
		{":slash_tmp", deny},
		{":tmpdir", deny},
	}
	if installRoot != "" {
		rules = append(rules, tomlEntry{installRoot, read})
	}
	rules = append(rules, tomlEntry{d.Home, deny}, tomlEntry{d.Tmp, write})
	for _, p := range codexHomeDenies {
		rules = append(rules, tomlEntry{p, deny})
	}
	workspace := []tomlEntry{
		{".", write},
		{".git", deny},
		{".env", deny},
		{".env.*", deny},
	}
	for _, p := range protectedWritePaths {
		p = strings.TrimSuffix(p, "/**")
		if p == ".git" {
			continue // denied outright above
		}
		workspace = append(workspace, tomlEntry{p, read})
	}
	return append(rules, tomlEntry{":workspace_roots", tomlInline(workspace)})
}

// configOverrides are the -c overrides of a fix run. Codex loads no
// config.toml (--ignore-user-config, and CODEX_HOME holds none), so these
// are its whole configuration.
func (p *codexProvider) configOverrides(root, installRoot string, d codexDirs) []string {
	prefix := "permissions." + codexProfile
	overrides := []string{
		"default_permissions=" + tomlString(codexProfile),
		prefix + ".filesystem=" + tomlInline(codexFilesystemRules(installRoot, d)),
		prefix + ".network={enabled=false}",
		// Anything outside the profile is refused, never escalated.
		`approval_policy="never"`,
		`web_search="disabled"`,
		// The checkout's .codex/ layers (config, hooks, rules) stay unloaded,
		// and so does its AGENTS.md: the fix prompt carries the instructions.
		"projects=" + tomlInline([]tomlEntry{{root, `{trust_level="untrusted"}`}}),
		"project_doc_max_bytes=0",
		// Commands get the core variables (PATH, HOME, ...) without secrets;
		// Codex also drops names containing KEY, SECRET or TOKEN.
		`shell_environment_policy.inherit="core"`,
		"shell_environment_policy.set=" + tomlInline([]tomlEntry{
			{"TMPDIR", tomlString(d.Tmp)},
			{"TMPPREFIX", tomlString(filepath.Join(d.Tmp, "zsh"))},
		}),
		`cli_auth_credentials_store="file"`,
	}
	if p.effort != "" {
		overrides = append(overrides, "model_reasoning_effort="+tomlString(p.effort))
	}
	return overrides
}

// buildArgs assembles the `codex exec` argv for a fix run, the prompt read
// from stdin. Extracted from Run so tests can check the confinement without
// exec'ing the binary.
func (p *codexProvider) buildArgs(root, installRoot string, d codexDirs) []string {
	args := []string{
		"exec", "--json", "--ephemeral",
		"--ignore-user-config", "--ignore-rules", "--strict-config",
		"--color", "never", "-C", root,
	}
	for _, f := range codexDisabledFeatures {
		args = append(args, "--disable", f)
	}
	for _, o := range p.configOverrides(root, installRoot, d) {
		args = append(args, "-c", o)
	}
	if p.model != "" {
		args = append(args, "--model", p.model)
	}
	return append(args, "-")
}

// codexEnv is the CLI's environment: allowedEnvKeys only, so no GitHub token
// or provider secret (the API key reaches Codex through `codex login`, in
// CODEX_HOME), plus CODEX_HOME.
func codexEnv(env []string, home string) []string {
	var out []string
	for _, e := range env {
		key, _, _ := strings.Cut(e, "=")
		if allowedEnvKeys[key] {
			out = append(out, e)
		}
	}
	return append(out, "CODEX_HOME="+home)
}

// resolveCodexBinary returns the Codex CLI's real path and its install root,
// two levels up (<root>/bin/codex for the standalone installer). The CLI
// re-executes itself by the path it was started with as the sandbox helper
// (apply_patch goes through it), so it runs by its real path, and the
// profile lets the helper read the install root.
func resolveCodexBinary(bin string) (real, installRoot string, err error) {
	if real, err = filepath.EvalSymlinks(bin); err != nil {
		return "", "", err
	}
	if real, err = filepath.Abs(real); err != nil {
		return "", "", err
	}
	return real, filepath.Dir(filepath.Dir(real)), nil
}

func (p *codexProvider) Run(ctx context.Context, prompt string, opts RunOpts) (*providerResult, error) {
	if !p.editFiles {
		return nil, errors.New("the codex provider only runs the fix")
	}
	root := gitRepoRoot()
	if root == "" {
		return nil, errors.New("codex: not inside a git repository, so there is no root to confine the fix to")
	}
	apiKey := lookupEnvVar(p.env, credentials.EnvVar)
	if apiKey == "" {
		return nil, errMissingAPIKey(credentials.EnvVar)
	}
	bin, err := exec.LookPath(codexBinary)
	if err != nil {
		return nil, fmt.Errorf("codex binary not found: %w", err)
	}
	bin, installRoot, err := resolveCodexBinary(bin)
	if err != nil {
		return nil, fmt.Errorf("locating the codex install: %w", err)
	}
	if opts.MaxBudgetUSD > 0 {
		Stderrf(ansiYellow, "Warning: max_budget_usd is not enforced for the codex fix — the Codex CLI has no budget limit; timeout_minutes still applies.\n")
	}

	base, err := codexWorkBase()
	if err != nil {
		return nil, err
	}
	work, err := os.MkdirTemp(base, "codecanary-codex-")
	if err != nil {
		return nil, err
	}
	defer func() { _ = os.RemoveAll(work) }()
	d := codexDirs{Home: filepath.Join(work, "home"), Tmp: filepath.Join(work, "tmp")}
	for _, dir := range []string{d.Home, d.Tmp} {
		if err := os.Mkdir(dir, 0o700); err != nil {
			return nil, err
		}
	}

	timeout := opts.Timeout
	if timeout <= 0 {
		timeout = defaultFixTimeout
	}
	ctx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()
	env := codexEnv(p.env, d.Home)

	login := exec.CommandContext(ctx, bin, "login", "--with-api-key")
	login.Env = env
	login.Dir = work
	login.Stdin = strings.NewReader(apiKey)
	if out, err := login.CombinedOutput(); err != nil {
		return nil, fmt.Errorf("codex login: %v: %s", err, strings.TrimSpace(string(out)))
	}

	cmd := exec.CommandContext(ctx, bin, p.buildArgs(root, installRoot, d)...)
	cmd.Env = env
	cmd.Dir = root
	cmd.Stdin = strings.NewReader(prompt)
	var stdout, stderr bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr
	start := time.Now()
	runErr := cmd.Run()
	durationMS := int(time.Since(start).Milliseconds())
	if ctx.Err() == context.DeadlineExceeded {
		return nil, fmt.Errorf("codex timed out after %s", timeout)
	}

	result, evErr := parseCodexEvents(stdout.Bytes())
	switch {
	case evErr != nil:
		return nil, evErr
	case runErr != nil:
		return nil, fmt.Errorf("codex failed: %v\n%s", runErr, truncateUTF8(stderr.String(), 4000))
	case result.Text == "":
		return nil, fmt.Errorf("codex returned no final message\n%s", truncateUTF8(stderr.String(), 4000))
	}
	result.DurationMS = durationMS
	result.Usage.Model = p.model
	result.Usage.DurationMS = durationMS
	result.Usage.CostUSD = estimateCost(result.Usage)
	return result, nil
}

// codexEvent is one line of `codex exec --json`.
type codexEvent struct {
	Type string `json:"type"`
	Item *struct {
		Type string `json:"type"`
		Text string `json:"text"`
	} `json:"item"`
	Usage *struct {
		InputTokens       int `json:"input_tokens"`
		CachedInputTokens int `json:"cached_input_tokens"`
		OutputTokens      int `json:"output_tokens"`
	} `json:"usage"`
	Message string `json:"message"`
	Error   *struct {
		Message string `json:"message"`
	} `json:"error"`
}

// codexStatusRe pulls the HTTP status out of a Codex error message
// ("unexpected status 401 Unauthorized: ...").
var codexStatusRe = regexp.MustCompile(`unexpected status (\d{3})`)

// codexError classifies a Codex error message as a ProviderError.
func codexError(msg, raw string) *ProviderError {
	status := 0
	if m := codexStatusRe.FindStringSubmatch(msg); m != nil {
		_, _ = fmt.Sscan(m[1], &status)
	}
	return classifyProviderError("codex", status, msg, raw)
}

// parseCodexEvents reads the JSONL event stream: the last agent message is
// the answer, and turn usage is summed. A failed turn becomes a
// ProviderError. "error" events are not fatal on their own — Codex reports
// each retry as one ("Reconnecting... 3/5") — but the last one explains a
// run that ends without an answer. Lines that aren't JSON events are skipped.
func parseCodexEvents(out []byte) (*providerResult, error) {
	result := &providerResult{}
	var lastErr, lastErrRaw string
	sc := bufio.NewScanner(bytes.NewReader(out))
	sc.Buffer(make([]byte, 0, 64*1024), 16*1024*1024)
	for sc.Scan() {
		line := bytes.TrimSpace(sc.Bytes())
		if len(line) == 0 || line[0] != '{' {
			continue
		}
		var ev codexEvent
		if json.Unmarshal(line, &ev) != nil {
			continue
		}
		switch ev.Type {
		case "item.completed":
			if ev.Item != nil && ev.Item.Type == "agent_message" {
				result.Text = ev.Item.Text
			}
		case "turn.completed":
			if u := ev.Usage; u != nil {
				result.Usage.InputTokens += max(0, u.InputTokens-u.CachedInputTokens)
				result.Usage.CacheReadTokens += u.CachedInputTokens
				result.Usage.OutputTokens += u.OutputTokens
			}
		case "turn.failed":
			msg := "turn failed"
			if ev.Error != nil && ev.Error.Message != "" {
				msg = ev.Error.Message
			}
			return nil, codexError(msg, string(line))
		case "error":
			lastErr, lastErrRaw = ev.Message, string(line)
		}
	}
	if result.Text == "" && lastErr != "" {
		return nil, codexError(lastErr, lastErrRaw)
	}
	return result, nil
}
