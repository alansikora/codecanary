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
	// Find codecanary-bot installations among the user's accessible installs.
	// The same app can appear multiple times (e.g. personal + org installs).
	out, err := exec.Command("gh", "api", "/user/installations",
		"--paginate", "--jq",
		`.installations[] | select(.app_slug == "codecanary-bot") | "\(.id)\t\(.repository_selection)"`,
	).Output()
	if err != nil {
		return false
	}

	raw := strings.TrimSpace(string(out))
	if raw == "" {
		return false
	}

	for _, line := range strings.Split(raw, "\n") {
		line = strings.TrimRight(line, "\r")
		parts := strings.SplitN(line, "\t", 2)
		if len(parts) != 2 {
			continue
		}
		installID, selection := parts[0], parts[1]

		// Validate installID is numeric to prevent path traversal.
		if installID == "" {
			continue
		}
		valid := true
		for _, c := range installID {
			if c < '0' || c > '9' {
				valid = false
				break
			}
		}
		if !valid {
			continue
		}

		// "all" means every repo in the account/org is covered.
		if selection == "all" {
			return true
		}

		// Check whether the specific repo is in this installation's selected repos.
		repoOut, err := exec.Command("gh", "api",
			fmt.Sprintf("/user/installations/%s/repositories", installID),
			"--paginate", "--jq", `.repositories[] | "\(.full_name)"`,
		).Output()
		if err != nil {
			continue
		}

		for _, name := range strings.Split(strings.TrimSpace(string(repoOut)), "\n") {
			if strings.TrimRight(name, "\r") == repo {
				return true
			}
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
