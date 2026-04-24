package review

import (
	"os"
	"path/filepath"
	"testing"
)

func TestWorkflowUsesCodecanary(t *testing.T) {
	cases := []struct {
		name string
		yaml string
		want bool
	}{
		{
			name: "direct match",
			yaml: "      - uses: alansikora/codecanary@canary\n",
			want: true,
		},
		{
			name: "quoted value",
			yaml: `      - uses: "alansikora/codecanary@v1"` + "\n",
			want: true,
		},
		{
			name: "bare uses without list dash",
			yaml: "uses: alansikora/codecanary@main\n",
			want: true,
		},
		{
			name: "commented out",
			yaml: "      # - uses: alansikora/codecanary@canary\n",
			want: false,
		},
		{
			name: "different action",
			yaml: "      - uses: actions/checkout@v6\n",
			want: false,
		},
		{
			name: "empty",
			yaml: "",
			want: false,
		},
		{
			name: "prefix mention in another context",
			yaml: "      run: echo alansikora/codecanary\n",
			want: false,
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := workflowUsesCodecanary(tc.yaml)
			if got != tc.want {
				t.Errorf("workflowUsesCodecanary() = %v, want %v", got, tc.want)
			}
		})
	}
}

// writeWorkflow creates .github/workflows/<name> relative to dir.
func writeWorkflow(t *testing.T, dir, name, body string) {
	t.Helper()
	path := filepath.Join(dir, ".github", "workflows", name)
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	if err := os.WriteFile(path, []byte(body), 0o644); err != nil {
		t.Fatalf("write %s: %v", path, err)
	}
}

func TestDetectCodecanaryWorkflow(t *testing.T) {
	t.Run("workflow present", func(t *testing.T) {
		dir := t.TempDir()
		t.Chdir(dir)
		writeWorkflow(t, dir, "codecanary.yml", "jobs:\n  review:\n    steps:\n      - uses: alansikora/codecanary@canary\n")

		path, ok := detectCodecanaryWorkflow()
		if !ok {
			t.Fatal("expected workflow to be detected")
		}
		if filepath.Base(path) != "codecanary.yml" {
			t.Errorf("got %q, want basename codecanary.yml", path)
		}
	})

	t.Run("no workflows directory", func(t *testing.T) {
		dir := t.TempDir()
		t.Chdir(dir)
		if _, ok := detectCodecanaryWorkflow(); ok {
			t.Error("expected no detection in empty dir")
		}
	})

	t.Run("unrelated workflow only", func(t *testing.T) {
		dir := t.TempDir()
		t.Chdir(dir)
		writeWorkflow(t, dir, "ci.yml", "jobs:\n  build:\n    steps:\n      - uses: actions/checkout@v6\n")
		if _, ok := detectCodecanaryWorkflow(); ok {
			t.Error("expected no detection when only unrelated workflows present")
		}
	})

	t.Run("yaml extension also matched", func(t *testing.T) {
		dir := t.TempDir()
		t.Chdir(dir)
		writeWorkflow(t, dir, "codecanary.yaml", "      - uses: alansikora/codecanary@main\n")
		if _, ok := detectCodecanaryWorkflow(); !ok {
			t.Error("expected .yaml extension to be detected")
		}
	})

	t.Run("commented-out uses ignored", func(t *testing.T) {
		dir := t.TempDir()
		t.Chdir(dir)
		writeWorkflow(t, dir, "codecanary.yml", "      # - uses: alansikora/codecanary@canary\n")
		if _, ok := detectCodecanaryWorkflow(); ok {
			t.Error("expected commented-out uses to be ignored")
		}
	})
}
