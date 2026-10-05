package review

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os/exec"
	"strings"
	"time"

	"github.com/alansikora/codecanary/internal/credentials"
)

// validCLIModels is the set of allowed model values for the Claude CLI provider.
// Accepts both aliases (sonnet) and full model IDs (claude-sonnet-4-6).
var validCLIModels = map[string]bool{
	"haiku": true, "sonnet": true, "opus": true,
	"claude-haiku-4-5-20251001": true,
	"claude-sonnet-4-6":         true,
	"claude-sonnet-4-5":         true,
	"claude-opus-4-6":           true,
	"claude-opus-4-5":           true,
}

func init() {
	providers["claude"] = ProviderFactory{
		New:      newClaudeCLIProvider,
		Validate: validateClaude,
		// No pricing entries — the Claude CLI reports cost directly.
		SuggestedReviewModel: "sonnet",
		SuggestedTriageModel: "haiku",
		AppRequirement: &AppRequirement{
			Name:       "Claude",
			AppSlug:    "claude",
			InstallURL: "https://github.com/apps/claude/installations/new",
		},
		OAuthConfig: &OAuthConfig{
			ClientID:     "9d1c250a-e61b-44d9-88ed-5944d1962f5e",
			AuthorizeURL: "https://claude.ai/oauth/authorize",
			TokenURL:     "https://platform.claude.com/v1/oauth/token",
			Scope:        "user:inference",
		},
	}
}

func validateClaude(mc *ModelConfig) error {
	if mc.Model != "" && !validCLIModels[mc.Model] {
		return fmt.Errorf("invalid model %q for claude provider (valid: haiku, sonnet, opus)", mc.Model)
	}
	return nil
}

// claudeCLIProvider implements ModelProvider using the Claude CLI binary.
// Requires the `claude` binary in PATH and an OAuth token.
type claudeCLIProvider struct {
	model        string
	advisorModel string // optional advisor model — enables the Claude CLI server-side advisor tool via --settings and the experimental env gate
	env          []string
	extraArgs    []string // from ClaudeArgs; appended after all managed flags
	binaryPath   string   // resolved Claude CLI binary path; never empty
	// reviewTools, when non-empty, replaces the default `--tools ""` arg with
	// `--tools <reviewTools>` so the LLM can use Read/Grep/Glob to verify
	// hypotheses during a single Run call, and switches on the confinement in
	// claudeToolUseArgs. Empty preserves the historical single-shot behaviour.
	// See ReviewConfig.ClaudeReviewTools.
	reviewTools string
	// workDir is the directory the CLI runs in when reviewTools is set: the
	// repository root, which is the only working directory the file tools may
	// read. Empty when tools are off (the CLI inherits codecanary's cwd).
	workDir string
}

// claudeAdvisorEnableEnvVar is the Claude CLI env var that opts into the
// experimental advisor tool. Surfaced as a constant so the implementation and
// tests stay aligned with the name baked into the claude binary.
const claudeAdvisorEnableEnvVar = "CLAUDE_CODE_ENABLE_EXPERIMENTAL_ADVISOR_TOOL"

// claudeOAuthEnvVar is the environment variable the Claude CLI reads for OAuth tokens.
const claudeOAuthEnvVar = "CLAUDE_CODE_OAUTH_TOKEN"

func newClaudeCLIProvider(mc *ModelConfig, env []string) ModelProvider {
	binaryPath := mc.ClaudePath
	if binaryPath == "" {
		binaryPath = "claude"
	}
	// Map CODECANARY_PROVIDER_SECRET → CLAUDE_CODE_OAUTH_TOKEN so the Claude CLI
	// can authenticate using the OAuth token obtained during `codecanary setup`.
	env = scrubClaudeEnv(injectClaudeOAuthToken(env))
	if mc.AdvisorModel != "" {
		env = injectClaudeAdvisorGate(env)
	}
	p := &claudeCLIProvider{
		model:        mc.Model,
		advisorModel: mc.AdvisorModel,
		env:          env,
		extraArgs:    mc.ClaudeArgs,
		binaryPath:   binaryPath,
	}
	if mc.ClaudeReviewTools != "" {
		p.enableReviewTools(mc.ClaudeReviewTools, gitRepoRoot())
	}
	return p
}

// enableReviewTools turns on reviewer tool use, confined to repoRoot. It fails
// closed: when the confinement root is unknown, or a user --settings in
// claude_args would displace the generated deny rules (Validate rejects that
// combination, so this is a second line), the review stays tool-less.
func (p *claudeCLIProvider) enableReviewTools(tools, repoRoot string) {
	switch {
	case repoRoot == "":
		Stderrf(ansiYellow, "Warning: claude_review_tools ignored — not inside a git repository, so there is no root to confine file reads to.\n")
	case hasFlag(p.extraArgs, "--settings"):
		Stderrf(ansiYellow, "Warning: claude_review_tools ignored — --settings in claude_args would replace the deny rules that confine file reads.\n")
	default:
		p.reviewTools = tools
		p.workDir = repoRoot
	}
}

// claudeScrubbedEnvKeys are credentials the Claude CLI subprocess never needs.
// The CLI authenticates with CLAUDE_CODE_OAUTH_TOKEN (injected from
// CODECANARY_PROVIDER_SECRET above), so the original secret is a duplicate,
// and GitHub tokens are only used by codecanary itself. Keeping them out of
// the child's environment means nothing the CLI runs or reads can echo them.
var claudeScrubbedEnvKeys = map[string]bool{
	credentials.EnvVar:        true,
	"GITHUB_TOKEN":            true,
	"GH_TOKEN":                true,
	"CODECANARY_GITHUB_TOKEN": true,
}

// scrubClaudeEnv returns a copy of env without claudeScrubbedEnvKeys. It never
// mutates the caller's slice, which is shared with the triage provider.
func scrubClaudeEnv(env []string) []string {
	out := make([]string, 0, len(env))
	for _, e := range env {
		key, _, _ := strings.Cut(e, "=")
		if claudeScrubbedEnvKeys[key] {
			continue
		}
		out = append(out, e)
	}
	return out
}

// injectClaudeAdvisorGate sets CLAUDE_CODE_ENABLE_EXPERIMENTAL_ADVISOR_TOOL=1
// so the Claude CLI activates its server-side advisor tool. Preserves any
// caller-provided value for the same variable.
func injectClaudeAdvisorGate(env []string) []string {
	for _, e := range env {
		key, _, _ := strings.Cut(e, "=")
		if key == claudeAdvisorEnableEnvVar {
			return env
		}
	}
	return append(env, claudeAdvisorEnableEnvVar+"=1")
}

// hasFlag reports whether args contains the given flag in either `--name` or
// `--name=value` form. Used to avoid stomping on user-provided values in
// claude_args.
func hasFlag(args []string, flag string) bool {
	prefix := flag + "="
	for _, a := range args {
		if a == flag || strings.HasPrefix(a, prefix) {
			return true
		}
	}
	return false
}

// injectClaudeOAuthToken copies CODECANARY_PROVIDER_SECRET into
// CLAUDE_CODE_OAUTH_TOKEN when the latter is not already set.
func injectClaudeOAuthToken(env []string) []string {
	var secret string
	hasOAuth := false
	for _, e := range env {
		key, val, _ := strings.Cut(e, "=")
		if key == claudeOAuthEnvVar {
			hasOAuth = true
			break
		}
		if key == credentials.EnvVar {
			secret = val
		}
	}
	if !hasOAuth && secret != "" {
		env = append(env, claudeOAuthEnvVar+"="+secret)
	}
	return env
}

// claudeIsolationArgs keep the CLI from loading Claude Code configuration out
// of the directory it runs in. In CI that directory is the PR head checkout,
// so a PR could otherwise ship a .claude/settings.json (hooks that run shell
// commands, an env block, apiKeyHelper, permission rules) or a .mcp.json whose
// servers `claude -p` connects without asking — all with the provider secret
// in the environment, and regardless of --tools. Per the Claude Code docs:
//   - --setting-sources user: read neither the project's settings files nor
//     its .mcp.json (https://code.claude.com/docs/en/permissions#what-runs-before-you-trust-a-folder)
//   - --strict-mcp-config: only MCP servers from --mcp-config (none unless the
//     operator passes one via claude_args) (https://code.claude.com/docs/en/cli-reference)
//
// Hooks are also turned off via "disableAllHooks" in the generated --settings
// (see claudeSettings), which outranks every settings file but managed policy.
var claudeIsolationArgs = []string{"--setting-sources", "user", "--strict-mcp-config"}

// claudeReviewDenyRules are Read deny rules applied when the reviewer has file
// tools. Read rules cover Read, Grep, Glob and LSP, and a deny matches when
// either the requested path or the file a symlink resolves to matches, so a
// symlink committed in the PR cannot point the reviewer at these. They back up
// the working-directory confinement (claudeToolUseArgs) for the locations an
// injected prompt would go after: runner/process state, CLI and tool
// credentials, and the checkout's own git metadata and dotenv files.
// Deliberately not "~/**": on GitHub-hosted runners the checkout itself lives
// under $HOME (/home/runner/work/...), and deny always wins over allow.
var claudeReviewDenyRules = []string{
	"Read(//proc/**)",
	"Read(//sys/**)",
	"Read(//dev/**)",
	"Read(//etc/**)",
	"Read(//private/etc/**)",
	"Read(~/.claude/**)",
	"Read(~/.claude.json)",
	"Read(~/.codecanary/**)",
	"Read(~/.config/**)",
	"Read(~/.ssh/**)",
	"Read(~/.aws/**)",
	"Read(~/.gnupg/**)",
	"Read(~/.docker/**)",
	"Read(~/.kube/**)",
	"Read(~/.netrc)",
	"Read(~/.npmrc)",
	"Read(~/.git-credentials)",
	"Read(~/.gitconfig)",
	"Read(~/Library/**)",
	"Read(.git/**)",
	"Read(.env)",
	"Read(.env.*)",
}

// claudeToolUseArgs confine the file tools when claude_review_tools is set:
//   - --restricted: confines the built-in file tools to the working
//     directories, loads only managed settings and --settings, refuses
//     bypassPermissions, and removes command and WebFetch tools unless named
//     in --tools (Claude Code v2.1.248+; older CLIs reject the flag, so the
//     review fails instead of running unconfined).
//   - --permission-mode dontAsk: anything that would prompt — such as a read
//     outside the working directories — is denied rather than auto-approved.
//     Pinned explicitly because a `-p` run's built-in default can be auto
//     mode, where out-of-directory reads run without a prompt.
//
// https://code.claude.com/docs/en/cli-reference,
// https://code.claude.com/docs/en/permission-modes#which-mode-a-session-starts-in
var claudeToolUseArgs = []string{"--restricted", "--permission-mode", "dontAsk"}

// claudeSettings builds the --settings JSON. Hooks are always disabled; with
// tools on, it also adds blockReadsOutsideWorkingDirectories (file tools refuse
// reads outside the working directories in every permission mode) and
// claudeReviewDenyRules. https://code.claude.com/docs/en/settings-reference
func (p *claudeCLIProvider) claudeSettings() map[string]any {
	settings := map[string]any{"disableAllHooks": true}
	if p.advisorModel != "" {
		settings["advisorModel"] = p.advisorModel
	}
	if p.reviewTools != "" {
		settings["permissions"] = map[string]any{
			"blockReadsOutsideWorkingDirectories": true,
			"deny":                                claudeReviewDenyRules,
		}
	}
	return settings
}

// buildArgs assembles the argv passed to the Claude CLI for a single Run call.
// Extracted from Run() so tests can verify managed-flag behaviour (the
// `--tools` allowlist and the isolation/confinement flags) without exec'ing
// the binary.
func (p *claudeCLIProvider) buildArgs(opts RunOpts) []string {
	// `--tools ""` disables all built-in tools (Bash, Read, Edit, etc.),
	// making the CLI a single-shot prompt-in/text-out call. When the caller
	// configured `claude_review_tools` (review-model only), pass that
	// allowlist through instead so the LLM can verify hypotheses (e.g.
	// `Read,Grep,Glob`) before emitting findings.
	args := []string{"--print", "--output-format", "json", "--no-session-persistence", "--tools", p.reviewTools}
	args = append(args, claudeIsolationArgs...)
	if p.reviewTools != "" {
		args = append(args, claudeToolUseArgs...)
	}
	if p.model != "" {
		args = append(args, "--model", p.model)
	}
	if opts.MaxBudgetUSD > 0 {
		args = append(args, "--max-budget-usd", fmt.Sprintf("%.2f", opts.MaxBudgetUSD))
	}
	if hasFlag(p.extraArgs, "--settings") {
		// Only reachable without tools (enableReviewTools refuses otherwise).
		// The operator's settings replace ours; project hooks still stay out
		// via --setting-sources.
		if p.advisorModel != "" {
			Stderrf(ansiYellow, "Warning: advisor_model is ignored because --settings is set via claude_args — merge advisorModel into your settings JSON to use both.\n")
		}
	} else if data, err := json.Marshal(p.claudeSettings()); err == nil {
		// Inline JSON so we do not need a temp file.
		args = append(args, "--settings", string(data))
	}
	args = append(args, p.extraArgs...)
	return args
}

func (p *claudeCLIProvider) Run(ctx context.Context, prompt string, opts RunOpts) (*providerResult, error) {
	timeout := opts.Timeout
	if timeout <= 0 {
		timeout = 10 * time.Minute
	}
	ctx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()

	args := p.buildArgs(opts)
	cmd := exec.CommandContext(ctx, p.binaryPath, args...)
	cmd.Env = p.env
	cmd.Dir = p.workDir // "" inherits codecanary's cwd
	cmd.Stdin = strings.NewReader(prompt)
	output, err := cmd.Output()
	if err != nil {
		if ctx.Err() == context.DeadlineExceeded {
			return nil, fmt.Errorf("claude timed out after %s", timeout)
		}
		var exitErr *exec.ExitError
		if errors.As(err, &exitErr) {
			// The CLI may still emit the JSON envelope on stdout when it exits
			// non-zero (e.g. 429). Try to parse and classify it first so the
			// user sees a friendly message; fall back to a raw dump only when
			// we cannot extract structured info.
			if resp, ok := tryParseClaudeEnvelope(output); ok && resp.IsError {
				return nil, classifyProviderError("claude", resp.APIErrorStatus, resp.Result, string(output))
			}
			return nil, fmt.Errorf("claude failed: %s\n%s", string(exitErr.Stderr), string(output))
		}
		return nil, fmt.Errorf("running claude: %w", err)
	}

	resp, ok := tryParseClaudeEnvelope(output)
	if !ok {
		// Fallback: treat entire original output as plain text (e.g. older CLI version).
		return &providerResult{Text: string(output)}, nil
	}

	if resp.IsError {
		return nil, classifyProviderError("claude", resp.APIErrorStatus, resp.Result, string(output))
	}

	// Note: the Claude CLI JSON output does not expose stop_reason, so we
	// cannot detect truncation here. The CLI manages its own output limits.
	result := &providerResult{
		Text: resp.Result,
		Usage: CallUsage{
			Model:             resp.firstModel(),
			InputTokens:       resp.Usage.InputTokens,
			OutputTokens:      resp.Usage.OutputTokens,
			CacheReadTokens:   resp.Usage.CacheReadInputTokens,
			CacheCreateTokens: resp.Usage.CacheCreationInputTokens,
			CostUSD:           resp.CostUSD,
			DurationMS:        resp.DurationMS,
		},
		DurationMS: resp.DurationMS,
	}
	for model, mu := range resp.ModelUsage {
		result.ModelUsages = append(result.ModelUsages, CallUsage{
			Model:             model,
			InputTokens:       mu.InputTokens,
			OutputTokens:      mu.OutputTokens,
			CacheReadTokens:   mu.CacheReadInputTokens,
			CacheCreateTokens: mu.CacheCreationInputTokens,
			CostUSD:           mu.CostUSD,
		})
	}
	return result, nil
}

// tryParseClaudeEnvelope pulls a claudeJSONResponse out of the CLI's stdout.
// The Claude CLI may print non-JSON status lines (status-line UI) before or
// after the JSON envelope, so we seek to the first '{' and let json.Decoder
// stop after the first complete value — tolerating trailing noise.
func tryParseClaudeEnvelope(output []byte) (claudeJSONResponse, bool) {
	var resp claudeJSONResponse
	jsonOutput := output
	if i := bytes.IndexByte(output, '{'); i > 0 {
		jsonOutput = output[i:]
	}
	if err := json.NewDecoder(bytes.NewReader(jsonOutput)).Decode(&resp); err != nil {
		return resp, false
	}
	return resp, true
}
