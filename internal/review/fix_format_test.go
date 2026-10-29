package review

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"strings"
	"testing"
)

// unsandboxed runs the format command directly, standing in for bubblewrap.
func unsandboxed(t *testing.T) {
	t.Helper()
	prev := formatSandbox
	formatSandbox = func(ctx context.Context, root string, argv []string) (*exec.Cmd, error) {
		cmd := exec.CommandContext(ctx, argv[0], argv[1:]...)
		cmd.Dir = root
		return cmd, nil
	}
	t.Cleanup(func() { formatSandbox = prev })
}

// withFormatter commits fix.format_command: [sh, fmt.sh] and the given
// fmt.sh to the fix repo and returns the new head.
func withFormatter(t *testing.T, dir, script string) string {
	t.Helper()
	files := map[string]string{
		".codecanary/config.yml": "version: 1\nprovider: claude\nreview_model: sonnet\ntriage_model: haiku\nfix:\n  enabled: true\n  format_command: [sh, fmt.sh]\n",
		"fmt.sh":                 script,
		"b.go":                   "package a\n",
	}
	for name, body := range files {
		if err := os.WriteFile(filepath.Join(dir, name), []byte(body), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	runGit(t, dir, "add", "-A")
	runGit(t, dir, "commit", "-q", "-m", "chore: formatter")
	return runGit(t, dir, "rev-parse", "HEAD")
}

func TestRunFix_FormatsTheFixedFilesOnly(t *testing.T) {
	unsandboxed(t)
	// The fake model edits a.go and adds c.go. The formatter records its
	// arguments, formats them, and strays into b.go, a new file and the
	// CodeCanary config, which must all be reverted.
	dir, bin := fixRepo(t, `printf 'package a\n\nvar m = map[string]int{}\n' > a.go
printf 'package a\n' > c.go`, fixAnswer)
	head := withFormatter(t, dir, `echo "$@" > ../fmt-args
for f in "$@"; do echo '// formatted' >> "$f"; done
echo '// stray' >> b.go
touch stray.txt
echo '# stray' >> .codecanary/config.yml
`)
	p := newFakeFixPlatform(head)

	if err := RunFix(FixOptions{ConfigPath: ".codecanary/config.yml", ClaudePath: bin, ReviewedSHA: head, Platform: p}); err != nil {
		t.Fatalf("RunFix: %v", err)
	}
	args, err := os.ReadFile(filepath.Join(dir, "..", "fmt-args"))
	if err != nil {
		t.Fatalf("formatter did not run: %v", err)
	}
	if got := strings.TrimSpace(string(args)); got != "a.go c.go" {
		t.Errorf("formatter arguments = %q, want the fix's files %q", got, "a.go c.go")
	}
	if p.pushed != 1 {
		t.Fatalf("pushed %d times, want 1", p.pushed)
	}
	if files := runGit(t, dir, "show", "--name-only", "--format=", "HEAD"); files != "a.go\nc.go" {
		t.Errorf("fix commit touched %q, want a.go and c.go", files)
	}
	for _, f := range []string{"a.go", "c.go"} {
		if body := runGit(t, dir, "show", "HEAD:"+f); !strings.HasSuffix(body, "// formatted") {
			t.Errorf("%s was committed unformatted:\n%s", f, body)
		}
	}
	if status := runGit(t, dir, "status", "--porcelain"); status != "" {
		t.Errorf("formatter's stray changes left in the checkout:\n%s", status)
	}
}

func TestRunFix_CommitsWhenTheFormatterFails(t *testing.T) {
	unsandboxed(t)
	dir, bin := fixRepo(t, `printf 'package a\n\nvar m = map[string]int{}\n' > a.go`, fixAnswer)
	head := withFormatter(t, dir, "echo '// formatted' >> a.go\necho 'lint offense' >&2\nexit 1\n")
	p := newFakeFixPlatform(head)

	if err := RunFix(FixOptions{ConfigPath: ".codecanary/config.yml", ClaudePath: bin, ReviewedSHA: head, Platform: p}); err != nil {
		t.Fatalf("RunFix: %v", err)
	}
	if p.pushed != 1 || len(p.notices) != 0 {
		t.Fatalf("pushed=%d notices=%v; a failing formatter must not stop the fix", p.pushed, p.notices)
	}
	if body := runGit(t, dir, "show", "HEAD:a.go"); !strings.HasSuffix(body, "// formatted") {
		t.Errorf("the formatter's edits before it failed were dropped:\n%s", body)
	}
}

func TestRunFix_CommitsUnformattedWithoutTheSandbox(t *testing.T) {
	prev := formatSandbox
	formatSandbox = func(context.Context, string, []string) (*exec.Cmd, error) {
		return nil, os.ErrNotExist
	}
	t.Cleanup(func() { formatSandbox = prev })
	dir, bin := fixRepo(t, `printf 'package a\n\nvar m = map[string]int{}\n' > a.go`, fixAnswer)
	head := withFormatter(t, dir, "touch ../formatter-ran\n")
	p := newFakeFixPlatform(head)

	if err := RunFix(FixOptions{ConfigPath: ".codecanary/config.yml", ClaudePath: bin, ReviewedSHA: head, Platform: p}); err != nil {
		t.Fatalf("RunFix: %v", err)
	}
	if _, err := os.Stat(filepath.Join(dir, "..", "formatter-ran")); err == nil {
		t.Error("the format command ran outside the sandbox")
	}
	if p.pushed != 1 {
		t.Errorf("pushed %d times, want 1", p.pushed)
	}
}

func TestBwrapArgs(t *testing.T) {
	sys := t.TempDir()
	lib := filepath.Join(sys, "lib")
	usr := filepath.Join(sys, "usr")
	if err := os.Mkdir(usr, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink("usr/lib", lib); err != nil {
		t.Fatal(err)
	}
	env := map[string]string{"PATH": "/usr/bin", "HOME": "/home/runner", "GH_TOKEN": "secret", "CODECANARY_PROVIDER_SECRET": "secret"}
	args := bwrapArgs("/home/runner/work/r/r", "/home/runner", "/home/runner/work/_temp", []string{usr, lib}, func(k string) string { return env[k] })
	joined := strings.Join(args, " ")

	for _, want := range []string{
		"--clearenv", "--unshare-user", "--unshare-pid", "--new-session", "--die-with-parent",
		"--setenv PATH /usr/bin", "--setenv HOME /tmp/home", "--ro-bind /home/runner /home/runner",
		"--ro-bind " + usr + " " + usr, "--symlink usr/lib " + lib,
		"--proc /proc",
		"--tmpfs /home/runner/work/_temp",
		"--bind /home/runner/work/r/r /home/runner/work/r/r",
		"--ro-bind /home/runner/work/r/r/.git /home/runner/work/r/r/.git",
		"--chdir /home/runner/work/r/r",
	} {
		if !strings.Contains(joined, want) {
			t.Errorf("bwrap args lack %q:\n%s", want, joined)
		}
	}
	if strings.Contains(joined, "secret") {
		t.Errorf("a secret reaches the sandbox:\n%s", joined)
	}
	if slices.Contains(args, "--unshare-net") || slices.Contains(args, "--unshare-all") {
		t.Error("the network is unshared; formatters may need to install toolchains")
	}
	// Mount order matters: the runner's temp is hidden and the checkout bound
	// writable over the read-only home, and .git read-only over the checkout;
	// "--" ends the options.
	home, temp, checkout, git := slices.Index(args, "/home/runner"), slices.Index(args, "/home/runner/work/_temp"), slices.Index(args, "--bind"), slices.Index(args, "/home/runner/work/r/r/.git")
	if home >= temp || temp >= checkout || checkout >= git || args[len(args)-1] != "--" {
		t.Errorf("mount order wrong: home %d, temp %d, checkout %d, .git %d, last %q", home, temp, checkout, git, args[len(args)-1])
	}
	if slices.Contains(args, "/home/runner/.cargo") {
		t.Error("a toolchain home is writable")
	}
}

// The real sandbox, where bubblewrap can run (Linux with user namespaces).
func TestBwrapCommand_KeepsSecretsOut(t *testing.T) {
	// CI sets CODECANARY_REQUIRE_BWRAP so a runner image that can't run the
	// sandbox fails the build instead of skipping silently.
	skip := t.Skipf
	if os.Getenv("CODECANARY_REQUIRE_BWRAP") != "" {
		skip = t.Fatalf
	}
	if _, err := exec.LookPath("bwrap"); err != nil {
		skip("bwrap not installed")
	}
	if out, err := exec.Command("bwrap", "--ro-bind", "/", "/", "--unshare-user", "--unshare-pid", "true").CombinedOutput(); err != nil {
		skip("bwrap can't create namespaces here: %v %s", err, out)
	}
	home := t.TempDir()
	root := filepath.Join(home, "work", "repo")
	runnerTemp := filepath.Join(home, "work", "_temp")
	for _, d := range []string{filepath.Join(root, ".git"), runnerTemp} {
		if err := os.MkdirAll(d, 0o755); err != nil {
			t.Fatal(err)
		}
	}
	if err := os.WriteFile(filepath.Join(runnerTemp, "token"), []byte("secret"), 0o600); err != nil {
		t.Fatal(err)
	}
	t.Setenv("HOME", home)
	t.Setenv("RUNNER_TEMP", runnerTemp)
	t.Setenv("GH_TOKEN", "secret")

	script := `set -u
env | grep -c secret || true
cat /proc/*/environ 2>/dev/null | tr '\0' '\n' | grep -c secret || true
cat "$RUNNER_TEMP_PATH/token" 2>/dev/null || echo no-runner-temp
{ echo x > .git/config; } 2>/dev/null && echo git-writable || echo git-readonly
echo formatted > a.go && echo checkout-writable
echo x > "$HOME/cache" && echo home-writable
{ echo x > "$REAL_HOME/planted"; } 2>/dev/null && echo real-home-writable || echo real-home-readonly`
	cmd, err := bwrapCommand(context.Background(), root, []string{"sh", "-c", strings.NewReplacer("$RUNNER_TEMP_PATH", runnerTemp, "$REAL_HOME", home).Replace(script)})
	if err != nil {
		t.Fatal(err)
	}
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("sandbox: %v\n%s", err, out)
	}
	want := "0\n0\nno-runner-temp\ngit-readonly\ncheckout-writable\nhome-writable\nreal-home-readonly\n"
	if string(out) != want {
		t.Errorf("sandbox output:\n%s\nwant:\n%s", out, want)
	}
	if body, _ := os.ReadFile(filepath.Join(root, "a.go")); string(body) != "formatted\n" {
		t.Errorf("checkout write did not persist: %q", body)
	}
	if _, err := os.Stat(filepath.Join(home, "planted")); err == nil {
		t.Error("a home write persisted outside the sandbox")
	}
}
