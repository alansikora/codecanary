package skills_test

import (
	"os"
	"testing"

	"github.com/alansikora/codecanary/internal/skills"
)

// TestCodecanaryReviewSkillSynced asserts the embedded skill matches the
// repo-level project-local copy under .claude/skills/. Both paths must
// stay identical — the embed is for `codecanary install-skill` (ships
// in the binary), the .claude/skills/ copy is for Claude Code's
// project-mode discovery when a session starts in this repo.
//
// Same pattern as internal/setup/TestCodecanaryWorkflowSynced for the
// workflow template at .github/workflows/codecanary.yml.
func TestCodecanaryReviewSkillSynced(t *testing.T) {
	const repoCopy = "../../.claude/skills/codecanary-review/SKILL.md"

	b, err := os.ReadFile(repoCopy)
	if err != nil {
		t.Fatalf("reading %s: %v", repoCopy, err)
	}
	if string(b) != skills.CodecanaryReview() {
		t.Fatalf("%s differs from internal/skills/codecanary-review/SKILL.md\n"+
			"edit either file and copy the change to the other; "+
			"both must stay identical", repoCopy)
	}
}
