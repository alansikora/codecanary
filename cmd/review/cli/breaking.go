package cli

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"strings"

	"github.com/spf13/cobra"
)

var errBreakingChangesFound = errors.New("breaking changes detected")

type breakingSurface struct {
	Pattern  string
	Category string
	Impact   string
}

type breakingCategory struct {
	Name   string   `json:"name"`
	Impact string   `json:"impact"`
	Files  []string `json:"files"`
}

type breakingResult struct {
	Breaking   bool               `json:"breaking"`
	Categories []breakingCategory `json:"categories"`
}

var breakingManifest = []breakingSurface{
	{"internal/review/config.go", "Config Schema", "Users may need to update `.codecanary/config.yml` field names, types, or values"},
	{"cmd/review/cli/review.go", "CLI Flags", "CLI flag names or defaults may have changed"},
	{"cmd/review/cli/root.go", "CLI Flags", "CLI flag names or defaults may have changed"},
	{"cmd/review/cli/costs.go", "CLI Costs", "`codecanary review costs` behavior may have changed"},
	{"cmd/review/cli/setup.go", "Setup Command", "`codecanary setup` behavior may have changed"},
	{"cmd/review/cli/auth.go", "Auth Command", "`codecanary auth` behavior may have changed"},
	{"cmd/review/cli/upgrade.go", "Upgrade Command", "`codecanary upgrade` behavior may have changed"},
	{"internal/selfupdate", "Self-Update", "Version check or upgrade mechanism changed"},
	{"internal/setup/workflow.go", "Workflow Template", "Users may need to re-run setup or manually update their workflow file"},
	{"action.yml", "GitHub Action", "Action inputs, steps, or behavior changed — users may need to update their workflow"},
	{"install.sh", "Install Script", "Install behavior changed — users re-installing will get different behavior"},
	{"workers/oidc/src/index.ts", "Worker API", "Token exchange endpoint changed; action may behave differently"},
	{"internal/review/formatter.go", "Comment Format", "PR comment markers or format changed; existing threads may not be detected"},
	{"internal/auth/oauth.go", "OAuth / Auth", "OAuth endpoints, client ID, or auth flow changed"},
	{".goreleaser.yml", "Release Artifacts", "Binary naming, archive format, or supported platforms changed"},
}

var breakingChangesCmd = &cobra.Command{
	Use:           "breaking-changes",
	Short:         "Detect changes to user-facing surfaces",
	SilenceErrors: true,
	SilenceUsage:  true,
	RunE: func(cmd *cobra.Command, args []string) error {
		base, err := cmd.Flags().GetString("base")
		if err != nil {
			return fmt.Errorf("flag --base: %w", err)
		}
		output, err := cmd.Flags().GetString("output")
		if err != nil {
			return fmt.Errorf("flag --output: %w", err)
		}

		if base == "" {
			return fmt.Errorf("--base is required")
		}

		changed, err := gitDiffFiles(base)
		if err != nil {
			return fmt.Errorf("git diff: %w", err)
		}

		result := matchBreakingChanges(changed)

		if output == "json" {
			enc := json.NewEncoder(os.Stdout)
			enc.SetIndent("", "  ")
			if err := enc.Encode(result); err != nil {
				return fmt.Errorf("encoding json: %w", err)
			}
		} else {
			fmt.Print(formatBreakingComment(result))
		}

		if result.Breaking {
			return errBreakingChangesFound
		}
		return nil
	},
}

func init() {
	breakingChangesCmd.Flags().String("base", "", "Base git ref to diff against (required)")
	breakingChangesCmd.Flags().StringP("output", "o", "markdown", "Output format: markdown or json")
	reviewCmd.AddCommand(breakingChangesCmd)
}

func gitDiffFiles(base string) ([]string, error) {
	out, err := exec.Command("git", "diff", "--name-only", base+"...HEAD").Output()
	if err != nil {
		return nil, err
	}
	raw := strings.TrimSpace(string(out))
	if raw == "" {
		return nil, nil
	}
	return strings.Split(raw, "\n"), nil
}

func matchBreakingChanges(changedFiles []string) breakingResult {
	seen := map[string]*breakingCategory{}
	var categories []breakingCategory

	for _, entry := range breakingManifest {
		for _, f := range changedFiles {
			if f == entry.Pattern || strings.HasPrefix(f, entry.Pattern+"/") {
				if cat, ok := seen[entry.Category]; ok {
					cat.Files = append(cat.Files, entry.Pattern)
				} else {
					cat := breakingCategory{
						Name:   entry.Category,
						Impact: entry.Impact,
						Files:  []string{entry.Pattern},
					}
					seen[entry.Category] = &cat
					categories = append(categories, cat)
				}
				break
			}
		}
	}

	// Update slice entries from pointer map (dedup may have appended files).
	for i := range categories {
		if cat, ok := seen[categories[i].Name]; ok {
			categories[i] = *cat
		}
	}

	return breakingResult{
		Breaking:   len(categories) > 0,
		Categories: categories,
	}
}

func formatBreakingComment(result breakingResult) string {
	if !result.Breaking {
		return ""
	}

	var b strings.Builder
	b.WriteString("<!-- codecanary:breaking-change-check -->\n")
	b.WriteString("## Breaking Change Detection\n\n")
	b.WriteString("This PR modifies user-facing surfaces. Users may need to take action when this is released.\n\n")
	b.WriteString("### Affected Areas\n\n")

	for _, cat := range result.Categories {
		fmt.Fprintf(&b, "- **%s** (`%s`)\n", cat.Name, strings.Join(cat.Files, "`, `"))
		fmt.Fprintf(&b, "  %s\n\n", cat.Impact)
	}

	b.WriteString("### Checklist\n")
	b.WriteString("- [ ] Changes are backward-compatible (no user action needed)\n")
	b.WriteString("- [ ] Migration guide added to release notes\n")
	b.WriteString("- [ ] Version bump warranted (major/minor)\n")

	return b.String()
}
