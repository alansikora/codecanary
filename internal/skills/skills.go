// Package skills exposes the Claude Code skills that ship inside the
// codecanary binary. Each skill is authored as a normal SKILL.md file
// (so editors / linters / Claude Code itself can read it directly) and
// embedded at build time via //go:embed so `codecanary install-skill`
// can materialize it onto a user's machine without a network round-trip.
//
// Source-of-truth layout:
//
//	internal/skills/codecanary-review/SKILL.md   ← canonical (embedded)
//	.claude/skills/codecanary-review/SKILL.md    ← identical copy, for
//	                                               project-local Claude
//	                                               Code discovery when
//	                                               the repo itself is the
//	                                               working directory.
//
// Go's //go:embed can't use ".." in patterns, so it cannot reach up to
// the repo-level .claude/skills/ directory. The duplicate is kept in
// sync by a parity test (see skills_test.go), the same convention this
// project already uses for internal/setup/codecanary.yml vs.
// .github/workflows/codecanary.yml.
package skills

import _ "embed"

//go:embed codecanary-review/SKILL.md
var codecanaryReviewSkill string

// CodecanaryReview returns the body of the codecanary:review SKILL.md file
// embedded at build time.
func CodecanaryReview() string { return codecanaryReviewSkill }
