package review

import (
	"context"
	"errors"
	"strings"
	"testing"
)

const testWorkflowV1 = `# codecanary-workflow: v1
name: CodeCanary
jobs:
  review:
    steps:
      - uses: alansikora/codecanary@canary
`

func TestWorkflowTemplateVersion(t *testing.T) {
	tests := []struct {
		name string
		yaml string
		want int
	}{
		{"marker", testWorkflowV1, 1},
		{"marker after header", "# Source of truth\n#\n# codecanary-workflow: v12\nname: x\n", 12},
		{"no space after hash", "#codecanary-workflow: v3\n", 3},
		{"no marker", "name: CodeCanary\n", 0},
		{"malformed version", "# codecanary-workflow: v1a\n", 0},
		{"not a comment", "codecanary-workflow: v2\n", 0},
		{"empty", "", 0},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := WorkflowTemplateVersion(tt.yaml); got != tt.want {
				t.Errorf("WorkflowTemplateVersion() = %d, want %d", got, tt.want)
			}
		})
	}
}

func TestWorkflowNoticeLine(t *testing.T) {
	wf := func(content string) codecanaryWorkflow {
		return codecanaryWorkflow{Path: ".github/workflows/codecanary.yml", Content: content}
	}
	customized := strings.Replace(testWorkflowV1, "      - uses: alansikora/codecanary@canary\n",
		"      - run: make setup\n      - uses: alansikora/codecanary@v0.6.24\n        with:\n          provider_secret: ${{ secrets.MY_KEY }}\n", 1)

	tests := []struct {
		name     string
		wf       codecanaryWorkflow
		found    bool
		template int
		want     []string // substrings; nil means no notice
	}{
		{"current", wf(testWorkflowV1), true, 1, nil},
		{"customized but current", wf(customized), true, 1, nil},
		{"newer than template", wf(strings.Replace(testWorkflowV1, "v1", "v3", 1)), true, 2, nil},
		{"outdated marked copy", wf(testWorkflowV1), true, 2,
			[]string{"is on template v1", "(v2)", "`.github/workflows/codecanary.yml`", "codecanary setup github", workflowTemplateURL}},
		{"copy without marker", wf("name: CodeCanary\n"), true, 1,
			[]string{"predates the current template", "(v1)"}},
		{"missing workflow file", codecanaryWorkflow{}, false, 2, nil},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := workflowNoticeLine(tt.wf, tt.found, tt.template)
			if tt.want == nil {
				if got != "" {
					t.Errorf("expected no notice, got %q", got)
				}
				return
			}
			for _, s := range tt.want {
				if !strings.Contains(got, s) {
					t.Errorf("notice %q missing %q", got, s)
				}
			}
		})
	}
}

func stubLatest(tag string, err error, called *bool) func(context.Context) (string, error) {
	return func(context.Context) (string, error) {
		if called != nil {
			*called = true
		}
		return tag, err
	}
}

func TestBinaryNoticeLine(t *testing.T) {
	tests := []struct {
		name       string
		version    string
		latest     string
		err        error
		wantNotice bool
		wantFetch  bool
	}{
		{"older stable", "v0.6.20", "v0.6.24", nil, true, true},
		{"current stable", "v0.6.24", "v0.6.24", nil, false, true},
		{"newer than latest", "v0.7.0", "v0.6.24", nil, false, true},
		{"canary behind a fresh release", "v0.6.24-SNAPSHOT-d0d9bc4", "v0.6.25", nil, false, false},
		{"dev build", "dev", "v0.6.24", nil, false, false},
		{"empty version", "", "v0.6.24", nil, false, false},
		{"release fetch fails", "v0.6.20", "", errors.New("rate limited"), false, true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			called := false
			u := UpdateCheck{BinaryVersion: tt.version, LatestRelease: stubLatest(tt.latest, tt.err, &called)}
			got := u.binaryNoticeLine()
			if (got != "") != tt.wantNotice {
				t.Errorf("notice = %q, wantNotice %v", got, tt.wantNotice)
			}
			if called != tt.wantFetch {
				t.Errorf("fetched = %v, want %v", called, tt.wantFetch)
			}
			if tt.wantNotice {
				for _, s := range []string{tt.version, tt.latest, "codecanary_version"} {
					if !strings.Contains(got, s) {
						t.Errorf("notice %q missing %q", got, s)
					}
				}
			}
		})
	}
}

func TestRenderUpdateNotice(t *testing.T) {
	if got := renderUpdateNotice(nil); got != "" {
		t.Errorf("no lines: got %q, want empty", got)
	}
	got := renderUpdateNotice([]string{"one", "two"})
	want := "\n<sub>ℹ️ one</sub>\n\n<sub>ℹ️ two</sub>\n"
	if got != want {
		t.Errorf("got %q, want %q", got, want)
	}
}

func TestUpdateCheckNotice(t *testing.T) {
	t.Run("zero value is silent", func(t *testing.T) {
		t.Chdir(t.TempDir())
		if got := (UpdateCheck{}).Notice(); got != "" {
			t.Errorf("got %q, want empty", got)
		}
	})

	t.Run("outdated workflow and binary", func(t *testing.T) {
		dir := t.TempDir()
		t.Chdir(dir)
		writeWorkflow(t, dir, "codecanary.yml", testWorkflowV1)
		u := UpdateCheck{BinaryVersion: "v0.6.20", TemplateVersion: 2, LatestRelease: stubLatest("v0.6.24", nil, nil)}
		got := u.Notice()
		if strings.Count(got, "<sub>") != 2 {
			t.Errorf("want two notice lines, got %q", got)
		}
	})

	t.Run("missing workflow and failed fetch", func(t *testing.T) {
		t.Chdir(t.TempDir())
		u := UpdateCheck{BinaryVersion: "v0.6.20", TemplateVersion: 2, LatestRelease: stubLatest("", errors.New("timeout"), nil)}
		if got := u.Notice(); got != "" {
			t.Errorf("got %q, want empty", got)
		}
	})
}

func TestReviewBodiesCarryNotes(t *testing.T) {
	notice := renderUpdateNotice([]string{"update me"})
	summary := ReviewSummary{NewFindings: 1}
	bodies := map[string]string{
		"clean":    buildCleanReviewBody("abc123", notice, summary),
		"allclear": buildAllClearReviewBody("abc123", false, notice, summary),
		"activity": buildActivityReviewBody("abc123", notice, summary),
	}
	for name, body := range bodies {
		n := strings.Index(body, "update me")
		s := strings.Index(body, statusBlockOpen)
		if n < 0 || s < 0 || n > s {
			t.Errorf("%s: notice should precede the status block:\n%q", name, body)
		}
		if !strings.HasSuffix(body, embedBaselineMarker("abc123")) {
			t.Errorf("%s: baseline marker must stay last:\n%q", name, body)
		}
	}
}
