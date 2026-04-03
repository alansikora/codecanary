package auth

import (
	"bufio"
	"fmt"
	"os"
	"os/exec"
	"strings"
)

const codeCanaryAppInstallURL = "https://github.com/apps/codecanary-bot/installations/new"

// CheckCodeCanaryAppInstalled reports whether the CodeCanary GitHub App is
// installed with access to the given repository (owner/name format).
// Returns false on any error so the caller can fall back to the install flow.
func CheckCodeCanaryAppInstalled(repo string) bool {
	// Find the codecanary-bot installation among the user's accessible installs.
	out, err := exec.Command("gh", "api", "/user/installations",
		"--paginate", "--jq",
		`.installations[] | select(.app_slug == "codecanary-bot") | "\(.id)\t\(.repository_selection)"`,
	).Output()
	if err != nil {
		return false
	}

	line := strings.TrimSpace(string(out))
	if line == "" {
		return false
	}
	// Take first match if multiple (shouldn't happen for the same app).
	if idx := strings.IndexByte(line, '\n'); idx >= 0 {
		line = line[:idx]
	}

	parts := strings.SplitN(line, "\t", 2)
	if len(parts) != 2 {
		return false
	}
	installID, selection := parts[0], parts[1]

	// "all" means every repo in the account/org is covered.
	if selection == "all" {
		return true
	}

	// Check whether the specific repo is in the installation's selected repos.
	repoOut, err := exec.Command("gh", "api",
		fmt.Sprintf("/user/installations/%s/repositories", installID),
		"--paginate", "--jq", `.repositories[].full_name`,
	).Output()
	if err != nil {
		return false
	}

	for _, name := range strings.Split(strings.TrimSpace(string(repoOut)), "\n") {
		if name == repo {
			return true
		}
	}
	return false
}

// InstallCodeCanaryApp opens the browser to install the CodeCanary Review app on a repo.
func InstallCodeCanaryApp(repo string, reader *bufio.Reader) error {
	fmt.Fprintf(os.Stderr, "Opening browser to install the CodeCanary Review app...\n")
	fmt.Fprintf(os.Stderr, "  → Select the repository: %s\n\n", repo)

	if err := OpenBrowser(codeCanaryAppInstallURL); err != nil {
		fmt.Fprintf(os.Stderr, "Could not open browser: %v\nOpen this URL in your browser:\n%s\n\n", err, codeCanaryAppInstallURL)
	}

	fmt.Fprintf(os.Stderr, "Press Enter after installing the app...")
	_, _ = reader.ReadString('\n')
	fmt.Fprintln(os.Stderr)
	return nil
}
