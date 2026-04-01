package auth

import (
	"bufio"
	"encoding/json"
	"fmt"
	"os/exec"
	"strconv"
	"strings"
)

const codeCanaryAppInstallURL = "https://github.com/apps/codecanary-bot/installations/new"

type installation struct {
	ID                  int    `json:"id"`
	AppSlug             string `json:"app_slug"`
	RepositorySelection string `json:"repository_selection"`
	Account             struct {
		Login string `json:"login"`
	} `json:"account"`
}

type installationsResponse struct {
	Installations []installation `json:"installations"`
}

type reposResponse struct {
	Repositories []struct {
		FullName string `json:"full_name"`
	} `json:"repositories"`
}

// CheckAppInstalled checks whether a GitHub App (by slug) is installed on the
// given repo. It uses the gh CLI's user-level installation listing so it works
// with regular OAuth tokens. Returns false on any error (best-effort check).
func CheckAppInstalled(repo, appSlug string) bool {
	owner := strings.SplitN(repo, "/", 2)[0]

	// List installations that the current user can see.
	out, err := exec.Command("gh", "api", "/user/installations", "--paginate").Output()
	if err != nil {
		return false
	}

	var resp installationsResponse
	if err := json.Unmarshal(out, &resp); err != nil {
		return false
	}

	for _, inst := range resp.Installations {
		if inst.AppSlug != appSlug || !strings.EqualFold(inst.Account.Login, owner) {
			continue
		}

		// "all" means every repo in this account is covered.
		if inst.RepositorySelection == "all" {
			return true
		}

		// Otherwise verify the specific repo is in the selected set.
		endpoint := "/user/installations/" + strconv.Itoa(inst.ID) + "/repositories"
		repoOut, err := exec.Command("gh", "api", endpoint, "--paginate").Output()
		if err != nil {
			continue
		}
		var repoResp reposResponse
		if err := json.Unmarshal(repoOut, &repoResp); err != nil {
			continue
		}
		for _, r := range repoResp.Repositories {
			if r.FullName == repo {
				return true
			}
		}
	}

	return false
}

// InstallCodeCanaryApp opens the browser to install the CodeCanary Review app on a repo.
func InstallCodeCanaryApp(repo string, reader *bufio.Reader) error {
	fmt.Printf("Opening browser to install the CodeCanary Review app...\n")
	fmt.Printf("  → Select the repository: %s\n\n", repo)

	if err := openBrowser(codeCanaryAppInstallURL); err != nil {
		fmt.Printf("Open this URL in your browser:\n%s\n\n", codeCanaryAppInstallURL)
	}

	fmt.Printf("Press Enter after installing the app...")
	reader.ReadString('\n')
	fmt.Println()
	return nil
}
