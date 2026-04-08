package auth

import (
	"bufio"
	"encoding/json"
	"fmt"
	"net/http"
	"net/url"
	"os"
	"os/exec"
	"strings"
	"time"
)

const codeCanaryAppInstallURL = "https://github.com/apps/codecanary-bot/installations/new"
const checkInstallURL = "https://oidc.codecanary.sh/check-install"

// CheckCodeCanaryAppInstalled checks whether the CodeCanary GitHub App is
// installed with access to the given repository (owner/name format).
// Returns (installed, checkSucceeded). When checkSucceeded is false, the
// installed value is meaningless — the caller should handle the ambiguity.
func CheckCodeCanaryAppInstalled(repo string) (bool, bool) {
	tokenOut, err := exec.Command("gh", "auth", "token").Output()
	if err != nil {
		return false, false
	}
	token := strings.TrimSpace(string(tokenOut))

	params := url.Values{}
	params.Set("repo", repo)
	req, err := http.NewRequest("GET", checkInstallURL+"?"+params.Encode(), nil)
	if err != nil {
		return false, false
	}
	req.Header.Set("Authorization", "Bearer "+token)

	client := &http.Client{Timeout: 10 * time.Second}
	resp, err := client.Do(req)
	if err != nil {
		return false, false
	}
	defer resp.Body.Close() //nolint:errcheck
	if resp.StatusCode != http.StatusOK {
		return false, false
	}
	var result struct {
		Installed bool `json:"installed"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&result); err != nil {
		return false, false
	}
	return result.Installed, true
}

// CheckGitHubAppInstalled checks whether a GitHub App with the given slug
// (e.g. "claude") is installed on the repository using `gh api`.
// Returns (installed, checkSucceeded).
func CheckGitHubAppInstalled(appSlug, repo string) (bool, bool) {
	parts := strings.SplitN(repo, "/", 2)
	if len(parts) < 2 {
		return false, false
	}
	owner := strings.ToLower(parts[0])

	// List installations accessible to the authenticated user, filtering by
	// the target app slug. Then check that the installation covers the repo owner.
	jqFilter := fmt.Sprintf(
		`.installations[] | select(.app_slug == "%s") | .account.login`,
		appSlug,
	)
	out, err := exec.Command("gh", "api",
		"user/installations",
		"--jq", jqFilter,
	).Output()
	if err != nil {
		return false, false
	}

	for _, line := range strings.Split(strings.TrimSpace(string(out)), "\n") {
		if strings.ToLower(strings.TrimSpace(line)) == owner {
			return true, true
		}
	}
	return false, true
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
