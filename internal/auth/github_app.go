package auth

import (
	"bufio"
	"fmt"
)

const codeCanaryAppInstallURL = "https://github.com/apps/codecanary-bot/installations/new"

// InstallCodeCanaryApp opens the browser to install the CodeCanary Review app on a repo.
func InstallCodeCanaryApp(repo string, reader *bufio.Reader) error {
	fmt.Printf("Opening browser to install the CodeCanary Review app...\n")
	fmt.Printf("  → Select the repository: %s\n\n", repo)

	if err := openBrowser(codeCanaryAppInstallURL); err != nil {
		fmt.Printf("Open this URL in your browser:\n%s\n\n", codeCanaryAppInstallURL)
	}

	fmt.Printf("Press Enter after installing the app...")
	_, _ = reader.ReadString('\n')
	fmt.Println()
	return nil
}
