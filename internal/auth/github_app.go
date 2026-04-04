package auth

import (
	"bufio"
	"encoding/json"
	"fmt"
	"net/http"
	"os"
	"time"
)

const codeCanaryAppInstallURL = "https://github.com/apps/codecanary-bot/installations/new"
const checkInstallURL = "https://oidc.codecanary.sh/check-install"

// CheckCodeCanaryAppInstalled reports whether the CodeCanary GitHub App is
// installed with access to the given repository (owner/name format).
// Returns false on any error so the caller can fall back to the install flow.
func CheckCodeCanaryAppInstalled(repo string) bool {
	client := &http.Client{Timeout: 10 * time.Second}
	resp, err := client.Get(checkInstallURL + "?repo=" + repo)
	if err != nil {
		return false
	}
	defer resp.Body.Close() //nolint:errcheck
	if resp.StatusCode != http.StatusOK {
		return false
	}
	var result struct {
		Installed bool `json:"installed"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&result); err != nil {
		return false
	}
	return result.Installed
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
