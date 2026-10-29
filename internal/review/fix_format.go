package review

import (
	"bytes"
	"context"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"sort"
	"strings"
	"time"
)

// formatTimeout bounds fix.format_command. The command may install the
// toolchains it formats with (a pinned rustup toolchain, gems) on a fresh
// runner, so it gets minutes, not seconds.
const formatTimeout = 10 * time.Minute

// formatEnvKeys are the only variables the format command inherits, never a
// token or secret. Toolchain homes (CARGO_HOME, RUSTUP_HOME, GEM_HOME) are
// left out on purpose: they point at read-only directories in the sandbox,
// so installs go to the sandbox's own HOME instead.
var formatEnvKeys = []string{"PATH", "USER", "LANG", "LC_ALL", "TZ"}

// formatHome is the format command's HOME: an empty directory on the
// sandbox's /tmp, writable and thrown away with it.
const formatHome = "/tmp/home"

// formatSystemDirs are mounted read-only into the sandbox when they exist.
var formatSystemDirs = []string{"/usr", "/bin", "/sbin", "/lib", "/lib32", "/lib64", "/etc", "/opt"}

// formatSandbox builds the command that runs argv in root. It is a variable
// so tests can run the command without bubblewrap, which exists on Linux only.
var formatSandbox = bwrapCommand

// formatFixChanges runs fix.format_command on the files the fix changed, so
// the commit passes the repository's formatters. The command comes from the
// pull request's checkout, so it runs only inside the sandbox, and only its
// changes to those files survive: anything else it touches is reverted.
// Failures are warnings: the fix is still committed, and the repository's CI
// remains the proof.
func formatFixChanges(cfg *ReviewConfig) {
	argv := cfg.FixFormatCommand()
	if len(argv) == 0 {
		return
	}
	root := gitRepoRoot()
	if root == "" {
		return
	}
	entries, err := workingTreeStatus()
	if err != nil {
		Stderrf(ansiYellow, "Warning: fix.format_command skipped: %v\n", err)
		return
	}
	scope := map[string]bool{}
	var files []string
	for _, e := range entries {
		scope[e.Path] = true
		if !strings.Contains(e.Code, "D") {
			files = append(files, e.Path)
		}
	}
	if len(files) == 0 {
		return
	}
	sort.Strings(files)

	ctx, cancel := context.WithTimeout(context.Background(), formatTimeout)
	defer cancel()
	cmd, err := formatSandbox(ctx, root, append(append([]string{}, argv...), files...))
	if err != nil {
		Stderrf(ansiYellow, "Warning: fix.format_command skipped: %v\n", err)
		return
	}
	var out bytes.Buffer
	cmd.Stdout = &out
	cmd.Stderr = &out
	Stderrf(ansiBold, "Formatting %d file%s with %s...\n", len(files), pluralS(len(files)), strings.Join(argv, " "))
	if err := cmd.Run(); err != nil {
		Stderrf(ansiYellow, "Warning: fix.format_command failed (%v); committing what it left:\n%s\n", err, truncateUTF8(out.String(), 4000))
	}

	reverted, err := revertChangesOutside(scope)
	if err != nil {
		Stderrf(ansiYellow, "Warning: %v\n", err)
	}
	for _, p := range reverted {
		Stderrf(ansiYellow, "Warning: reverted fix.format_command's change to %s — it may change only the fix's files\n", p)
	}
}

// revertChangesOutside undoes every change to a path not in scope, and to
// protectedWritePaths, and returns the paths it reverted.
func revertChangesOutside(scope map[string]bool) ([]string, error) {
	entries, err := workingTreeStatus()
	if err != nil {
		return nil, err
	}
	var reverted []string
	for _, e := range entries {
		if scope[e.Path] && !isProtectedPath(e.Path) {
			continue
		}
		if err := revertEntry(e); err != nil {
			return reverted, err
		}
		reverted = append(reverted, e.Path)
	}
	return reverted, nil
}

// revertEntry returns one changed path to HEAD: an untracked file is removed.
func revertEntry(e statusEntry) error {
	if e.Code == "??" {
		_, err := fixGit("", nil, "clean", "-fq", "--", e.Path)
		return err
	}
	_, err := fixGit("", nil, "checkout", "-q", "HEAD", "--", e.Path)
	return err
}

// bwrapCommand runs argv in root inside bubblewrap. The sandbox keeps the
// fix job's credentials out of reach of the checkout's code:
//   - a cleared environment (formatEnvKeys only), for bwrap itself as well
//     as the command, so no token is inherited;
//   - its own user, PID, IPC and UTS namespaces and a fresh /proc, so it can't
//     read the environment of codecanary or the runner;
//   - system directories and the home directory read-only, the runner's temp
//     directory hidden, and only the checkout writable, with its .git
//     read-only so no git config or hook planted there runs afterwards;
//   - a fresh, empty HOME on the sandbox's own /tmp for caches and toolchain
//     installs, so nothing it writes outside the checkout outlives it (a
//     binary planted in ~/.cargo/bin would run in later steps, which can
//     mint the app token).
//
// The network stays shared: formatters may install their pinned toolchains.
func bwrapCommand(ctx context.Context, root string, argv []string) (*exec.Cmd, error) {
	if runtime.GOOS != "linux" {
		return nil, fmt.Errorf("the sandbox (bubblewrap) runs on Linux only")
	}
	bwrap, err := exec.LookPath("bwrap")
	if err != nil {
		return nil, fmt.Errorf("bubblewrap (bwrap) not found — the fix runs the checkout's code only inside it")
	}
	args := bwrapArgs(root, os.Getenv("HOME"), os.Getenv("RUNNER_TEMP"), existingPaths(formatSystemDirs), os.Getenv)
	cmd := exec.CommandContext(ctx, bwrap, append(args, argv...)...)
	// bwrap's own init process keeps the environment it was started with,
	// readable from inside the sandbox, so --clearenv alone is not enough.
	cmd.Env = formatEnv(os.Getenv)
	return cmd, nil
}

// formatEnv is formatEnvKeys taken from getenv, as KEY=value pairs.
func formatEnv(getenv func(string) string) []string {
	var env []string
	for _, k := range formatEnvKeys {
		if v := getenv(k); v != "" {
			env = append(env, k+"="+v)
		}
	}
	return env
}

// bwrapArgs are bubblewrap's arguments up to the command; see bwrapCommand.
func bwrapArgs(root, home, runnerTemp string, systemDirs []string, getenv func(string) string) []string {
	args := []string{
		"--die-with-parent", "--new-session",
		"--unshare-user", "--unshare-pid", "--unshare-ipc", "--unshare-uts", "--unshare-cgroup-try",
		"--clearenv",
	}
	for _, k := range formatEnvKeys {
		if v := getenv(k); v != "" {
			args = append(args, "--setenv", k, v)
		}
	}
	for _, d := range systemDirs {
		if target, err := os.Readlink(d); err == nil {
			args = append(args, "--symlink", target, d)
			continue
		}
		args = append(args, "--ro-bind", d, d)
	}
	args = append(args, "--dev", "/dev", "--proc", "/proc", "--tmpfs", "/tmp",
		"--dir", formatHome, "--setenv", "HOME", formatHome)
	if home != "" {
		args = append(args, "--ro-bind", home, home)
	}
	if runnerTemp != "" {
		args = append(args, "--tmpfs", runnerTemp)
	}
	args = append(args,
		"--bind", root, root,
		"--ro-bind", filepath.Join(root, ".git"), filepath.Join(root, ".git"),
		"--chdir", root,
		"--",
	)
	return args
}

// existingPaths keeps the paths that exist.
func existingPaths(paths []string) []string {
	var out []string
	for _, p := range paths {
		if _, err := os.Lstat(p); err == nil {
			out = append(out, p)
		}
	}
	return out
}
