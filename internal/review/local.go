package review

import (
	"errors"
	"fmt"
	"os"
	"os/exec"
	"strings"
)

// RunLocalOptions configures a local (pre-PR) review run.
type RunLocalOptions struct {
	BaseBranch string // branch to diff against (default: "origin/main" or "main")
	ConfigPath string
	Output     string // "markdown" or "json"
	DryRun     bool
	SaveUsage  bool // write codecanary-usage.json (default: false)
	ApplyFixes bool // run claude automatically to apply all findings after review
}

// FetchLocalDiff builds a PRData struct from the local git diff against baseBranch.
// It does not require a GitHub PR to exist.
func FetchLocalDiff(baseBranch string) (*PRData, error) {
	// Verify base branch exists locally.
	if _, err := exec.Command("git", "rev-parse", "--verify", baseBranch).Output(); err != nil {
		return nil, fmt.Errorf("base branch %q not found locally (try: git fetch origin %s)", baseBranch, baseBranch)
	}

	// Get unified diff.
	diffOut, err := exec.Command("git", "diff", baseBranch+"...HEAD").Output()
	if err != nil {
		return nil, fmt.Errorf("git diff: %w", err)
	}
	diff := string(diffOut)

	if strings.TrimSpace(diff) == "" {
		return nil, fmt.Errorf("no changes between %q and HEAD — nothing to review", baseBranch)
	}

	// Extract file list from diff (reuses existing helper).
	files := FilesFromDiff(diff)

	// Get HEAD SHA.
	shaOut, err := exec.Command("git", "rev-parse", "HEAD").Output()
	if err != nil {
		return nil, fmt.Errorf("git rev-parse HEAD: %w", err)
	}
	sha := strings.TrimSpace(string(shaOut))

	// Get current branch name (empty in detached HEAD mode).
	branchOut, _ := exec.Command("git", "branch", "--show-current").Output()
	headBranch := strings.TrimSpace(string(branchOut))
	if headBranch == "" && len(sha) >= 8 {
		headBranch = sha[:8] // detached HEAD fallback
	}

	// Use the first commit subject as title; fall back to branch name.
	logOut, _ := exec.Command("git", "log", baseBranch+"...HEAD", "--format=%s", "--reverse").Output()
	title := headBranch
	if logLines := strings.Split(strings.TrimSpace(string(logOut)), "\n"); len(logLines) > 0 && logLines[0] != "" {
		title = logLines[0]
	}

	// Concatenate all commit messages as the PR body.
	bodyOut, _ := exec.Command("git", "log", baseBranch+"...HEAD", "--format=%B", "--reverse").Output()
	body := strings.TrimSpace(string(bodyOut))

	// Author from git config.
	authorOut, _ := exec.Command("git", "config", "user.name").Output()
	author := strings.TrimSpace(string(authorOut))
	if author == "" {
		author = "local"
	}

	_ = sha // SHA used only locally; PRData has no SHA field

	return &PRData{
		Number:     0,
		Title:      title,
		Body:       body,
		Author:     author,
		BaseBranch: baseBranch,
		HeadBranch: headBranch,
		Diff:       diff,
		Files:      files,
	}, nil
}

// RunLocal orchestrates a local review against a base branch without a GitHub PR.
func RunLocal(opts RunLocalOptions) error {
	baseBranch := opts.BaseBranch
	if baseBranch == "" {
		// Prefer origin/main over local main to ensure the merge base
		// matches what GitHub uses for the PR diff, regardless of whether
		// the local main branch is up-to-date.
		if _, err := exec.Command("git", "rev-parse", "--verify", "origin/main").Output(); err == nil {
			baseBranch = "origin/main"
		} else {
			baseBranch = "main"
		}
	}

	// 1. Fetch local diff.
	pr, err := FetchLocalDiff(baseBranch)
	if err != nil {
		return err
	}

	// 2. Load config.
	var cfg *ReviewConfig
	configPath := opts.ConfigPath
	if configPath == "" {
		configPath = ".codecanary.yml"
	}
	loaded, err := LoadConfig(configPath)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			cfg = &ReviewConfig{}
		} else {
			return fmt.Errorf("loading config: %w", err)
		}
	} else {
		cfg = loaded
	}

	// 3. Read project documentation (CLAUDE.md files).
	projectDocs := ReadProjectDocs()
	if len(projectDocs) > 0 {
		fmt.Fprintf(os.Stderr, "Loaded %d project doc(s) for review context\n", len(projectDocs))
	}

	// 4. Fetch full file contents for changed files.
	fileContents, skippedFiles := FetchFileContents(pr.Files, cfg.Ignore, cfg.EffectiveMaxFileSize(), cfg.EffectiveMaxTotalSize())
	pr.FileContents = fileContents
	if len(skippedFiles) > 0 {
		fmt.Fprintf(os.Stderr, "Skipped %d large/ignored files: %s\n", len(skippedFiles), strings.Join(skippedFiles, ", "))
	}

	// 5. Build prompt.
	fmt.Fprintf(os.Stderr, "Reviewing local changes against %q...\n", baseBranch)
	prompt := BuildPrompt(pr, cfg, 0, projectDocs)

	// 6. Dry run — print prompt and return.
	if opts.DryRun {
		fmt.Print(prompt)
		return nil
	}

	// 7. Run Claude.
	env := resolveEnv()
	tracker := &UsageTracker{}

	claudeOut, err := runClaude(prompt, env, cfg.EffectiveReviewModel(), cfg.MaxBudgetUSD, cfg.EffectiveTimeout())
	if err != nil {
		return err
	}
	usage := claudeOut.Usage
	usage.Phase = "review"
	tracker.Add(usage)

	// 8. Parse findings.
	findings, err := ParseFindings(claudeOut.Text)
	if err != nil {
		return fmt.Errorf("parsing findings: %w", err)
	}

	// Safety net: drop findings on files not in the diff.
	diffFileSet := make(map[string]bool, len(pr.Files))
	for _, f := range pr.Files {
		diffFileSet[f] = true
	}
	var filtered []Finding
	for _, f := range findings {
		if f.File == "" || diffFileSet[f.File] {
			filtered = append(filtered, f)
		} else {
			fmt.Fprintf(os.Stderr, "Dropped finding on file not in diff: %s\n", f.File)
		}
	}
	findings = FilterNonActionable(filtered)

	if len(findings) == 0 {
		fmt.Fprintf(os.Stderr, "No findings\n")
	} else {
		fmt.Fprintf(os.Stderr, "Found %d finding(s)\n", len(findings))
	}

	// 9. Get HEAD SHA for result tracking.
	headSHA, _ := exec.Command("git", "rev-parse", "HEAD").Output()

	reviewResult := &ReviewResult{
		PRNumber: 0,
		Repo:     "local",
		Findings: findings,
		SHA:      strings.TrimSpace(string(headSHA)),
	}

	// 10. Format and print output.
	outputFormat := opts.Output
	if outputFormat == "" {
		outputFormat = "markdown"
	}

	var formatted string
	switch outputFormat {
	case "json":
		jsonOut, err := FormatJSON(reviewResult)
		if err != nil {
			return fmt.Errorf("formatting JSON: %w", err)
		}
		formatted = jsonOut
	default:
		// Strip the hidden <!-- codecanary:review {...} --> block that FormatMarkdown
		// appends for incremental PR reviews. It serves no purpose in pre-review output
		// and pollutes the terminal.
		md := FormatMarkdown(reviewResult)
		if idx := strings.Index(md, "\n<!-- codecanary:review "); idx != -1 {
			md = md[:idx]
		}
		// Append the fix-all prompt when there are findings.
		if len(findings) > 0 {
			md += "\n---\n\n## Fix All With AI\n\n"
			md += "Paste the prompt below into your AI coding tool, or run: `codecanary pre-review --fix`\n\n"
			fixPrompt := buildFixAllPrompt(findings)
			fence := codeFence(fixPrompt)
			md += fence + "\n"
			md += fixPrompt
			md += fence + "\n"
		}
		formatted = md
	}

	fmt.Print(formatted)

	// 11. Apply fixes via Claude if requested.
	if opts.ApplyFixes && len(findings) > 0 {
		fmt.Fprintf(os.Stderr, "\nApplying fixes via Claude...\n\n")
		claudeCmd := exec.Command("claude", buildFixAllPrompt(findings))
		claudeCmd.Stdin = os.Stdin
		claudeCmd.Stdout = os.Stdout
		claudeCmd.Stderr = os.Stderr
		if err := claudeCmd.Run(); err != nil {
			return fmt.Errorf("applying fixes: %w", err)
		}
	}

	// 12. Write usage report only when explicitly requested.
	if opts.SaveUsage {
		if report := tracker.Report("local", 0); len(report.Calls) > 0 {
			if err := WriteUsageFile(report); err != nil {
				fmt.Fprintf(os.Stderr, "Warning: could not write usage report: %v\n", err)
			}
		}
	}

	return nil
}
