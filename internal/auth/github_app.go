package auth

import (
	"bufio"
	"encoding/json"
	"fmt"
	"net/http"
	"net/url"
	"os"
	"os/exec"
	"regexp"
	"strings"
	"time"
)

var validAppSlug = regexp.MustCompile(`^[a-zA-Z0-9-]+$`)
var validInstallID = regexp.MustCompile(`^[0-9]+$`)

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
	if resp.StatusCode >= 500 {
		return false, false // server error — check inconclusive
	}
	if resp.StatusCode != http.StatusOK {
		return false, true // 4xx — definitive "not installed" or auth issue
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
	if !validAppSlug.MatchString(appSlug) {
		return false, false
	}
	parts := strings.SplitN(repo, "/", 2)
	if len(parts) < 2 {
		return false, false
	}
	owner := strings.ToLower(parts[0])
	repoName := strings.ToLower(parts[1])

	// List installations accessible to the authenticated user, filtering by
	// the target app slug. Return the installation ID and account login.
	jqFilter := fmt.Sprintf(
		`.installations[] | select(.app_slug == "%s") | "\(.id) \(.account.login)"`,
		appSlug,
	)
	out, err := exec.Command("gh", "api",
		"user/installations",
		"--jq", jqFilter,
	).Output()
	if err != nil {
		return false, false
	}

	// Find the installation ID for the repo owner.
	var installID string
	for _, line := range strings.Split(strings.TrimSpace(string(out)), "\n") {
		line = strings.TrimSpace(line)
		if line == "" {
			continue
		}
		parts := strings.SplitN(line, " ", 2)
		if len(parts) == 2 && strings.ToLower(parts[1]) == owner {
			installID = parts[0]
			break
		}
	}
	if installID == "" {
		return false, true // app not installed on this owner
	}
	if !validInstallID.MatchString(installID) {
		return false, false
	}

	// Verify the specific repository is accessible to this installation.
	repoJQ := `.repositories[] | .full_name`
	repoOut, err := exec.Command("gh", "api",
		fmt.Sprintf("user/installations/%s/repositories", installID),
		"--paginate",
		"--jq", repoJQ,
	).Output()
	if err != nil {
		return false, false
	}

	for _, line := range strings.Split(strings.TrimSpace(string(repoOut)), "\n") {
		line = strings.TrimSpace(line)
		if line == "" {
			continue
		}
		if strings.ToLower(line) == owner+"/"+repoName {
			return true, true
		}
	}
	return false, true // installed on owner but not on this specific repo
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
