package review

import (
	"testing"
)

func TestFindingFromThreadLosesLocalStateFields(t *testing.T) {
	// This test documents the known limitation: FindingFromThread cannot
	// reconstruct findings from the plain-text body that findingsToKnownIssues
	// produces. This is why LocalPlatform.SavedFinding exists — it
	// bypasses FindingFromThread entirely for local mode.
	//
	// In GitHub mode, FindingFromThread works correctly because PR comments
	// contain embedded JSON markers (<!-- codecanary:finding {...} -->).

	tmpDir := t.TempDir()
	t.Setenv("HOME", tmpDir)

	branch := "test-branch-lossy"
	original := []Finding{
		{
			ID:          "missing-validation",
			File:        "api/handler.go",
			Line:        42,
			Severity:    "warning",
			Title:       "Missing input validation",
			Description: "The handler does not validate the request body before processing.",
			Suggestion:  "Add validation before the DB call.",
			FixRef:      "local-1",
		},
	}

	err := SaveLocalState(branch, &LocalState{
		SHA:      "abc123",
		Branch:   branch,
		Findings: original,
	})
	if err != nil {
		t.Fatalf("SaveLocalState: %v", err)
	}

	lp := &LocalPlatform{Branch: branch}
	threads, _, _ := lp.LoadPreviousFindings()

	// FindingFromThread is lossy for local state bodies — fields are empty.
	reconstructed := FindingFromThread(threads[0])
	if reconstructed.ID != "" {
		t.Errorf("expected empty ID from lossy reconstruction, got %q", reconstructed.ID)
	}
	if reconstructed.Title == original[0].Title {
		t.Error("expected lossy reconstruction to NOT preserve title")
	}
}

func TestLocalStillOpenFindingsPreserved(t *testing.T) {
	tmpDir := t.TempDir()
	t.Setenv("HOME", tmpDir)

	branch := "test-stillopen"
	original := []Finding{
		{
			ID:          "missing-check",
			File:        "handler.go",
			Line:        25,
			Severity:    "warning",
			Title:       "Missing nil check",
			Description: "The pointer is dereferenced without a nil check.",
			Suggestion:  "Add a nil guard before the dereference.",
			FixRef:      "local-1",
		},
		{
			ID:          "error-ignored",
			File:        "db.go",
			Line:        50,
			Severity:    "bug",
			Title:       "Error return ignored",
			Description: "The error from Close() is silently discarded.",
			FixRef:      "local-2",
		},
	}

	err := SaveLocalState(branch, &LocalState{
		SHA:      "aaa111",
		Branch:   branch,
		Findings: original,
	})
	if err != nil {
		t.Fatalf("SaveLocalState: %v", err)
	}

	// Load findings through the platform.
	lp := &LocalPlatform{Branch: branch}
	threads, _, _ := lp.LoadPreviousFindings()

	// Simulate runTriage's stillOpenFindings loop with the fix:
	// use SavedFinding for LocalPlatform, fall back to FindingFromThread.
	var stillOpen []Finding
	fixedSet := map[int]bool{} // nothing fixed
	for i, th := range threads {
		if fixedSet[i] {
			continue
		}
		if f, ok := lp.SavedFinding(i); ok {
			f.Status = "still open"
			stillOpen = append(stillOpen, f)
		} else {
			stillOpen = append(stillOpen, FindingFromThread(th))
		}
	}

	if len(stillOpen) != 2 {
		t.Fatalf("expected 2 still-open findings, got %d", len(stillOpen))
	}

	for i, f := range stillOpen {
		orig := original[i]
		if f.ID != orig.ID {
			t.Errorf("finding %d: ID = %q, want %q", i, f.ID, orig.ID)
		}
		if f.Title != orig.Title {
			t.Errorf("finding %d: Title = %q, want %q", i, f.Title, orig.Title)
		}
		if f.Description != orig.Description {
			t.Errorf("finding %d: Description = %q, want %q", i, f.Description, orig.Description)
		}
		if f.Suggestion != orig.Suggestion {
			t.Errorf("finding %d: Suggestion = %q, want %q", i, f.Suggestion, orig.Suggestion)
		}
		if f.Status != "still open" {
			t.Errorf("finding %d: Status = %q, want %q", i, f.Status, "still open")
		}
	}

	// Verify that saving these back preserves all fields.
	err = SaveLocalState(branch, &LocalState{
		SHA:      "bbb222",
		Branch:   branch,
		Findings: combineFindings(stillOpen, nil),
	})
	if err != nil {
		t.Fatalf("SaveLocalState (round 2): %v", err)
	}

	reloaded, err := LoadLocalState(branch)
	if err != nil {
		t.Fatalf("LoadLocalState: %v", err)
	}
	if len(reloaded.Findings) != 2 {
		t.Fatalf("expected 2 reloaded findings, got %d", len(reloaded.Findings))
	}
	for i, f := range reloaded.Findings {
		orig := original[i]
		if f.ID != orig.ID {
			t.Errorf("reloaded %d: ID = %q, want %q", i, f.ID, orig.ID)
		}
		if f.Title != orig.Title {
			t.Errorf("reloaded %d: Title = %q, want %q", i, f.Title, orig.Title)
		}
		if f.Description != orig.Description {
			t.Errorf("reloaded %d: Description = %q, want %q", i, f.Description, orig.Description)
		}
	}

	// SavedFinding bounds checks.
	if _, ok := lp.SavedFinding(-1); ok {
		t.Error("SavedFinding(-1) should return false")
	}
	if _, ok := lp.SavedFinding(999); ok {
		t.Error("SavedFinding(999) should return false")
	}
}
