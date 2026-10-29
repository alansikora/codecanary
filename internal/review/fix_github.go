package review

import (
	"bytes"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"strings"
)

// GithubPlatform implements FixPlatform alongside ReviewPlatform, so
// `codecanary fix` runs against the same pull request adapter the review
// posts through.
var _ FixPlatform = (*GithubPlatform)(nil)

// ghPullRequest is the subset of the REST pull request payload the fix needs.
type ghPullRequest struct {
	Number int `json:"number"`
	User   struct {
		Login string `json:"login"`
		ID    int64  `json:"id"`
	} `json:"user"`
	Head struct {
		SHA  string `json:"sha"`
		Ref  string `json:"ref"`
		Repo *struct {
			FullName string `json:"full_name"`
		} `json:"repo"`
	} `json:"head"`
}

// ghUser is the subset of the REST user payload the fix needs.
type ghUser struct {
	Login string `json:"login"`
	ID    int64  `json:"id"`
	Name  string `json:"name"`
}

func fetchGHUser(login string) (ghUser, error) {
	var u ghUser
	out, err := runGH("gh api users", "api", "users/"+login)
	if err != nil {
		return u, err
	}
	if err := json.Unmarshal(out, &u); err != nil {
		return u, fmt.Errorf("parsing user %s: %w", login, err)
	}
	return u, nil
}

// noreplyIdentity is a GitHub account's git identity: its display name (the
// login when it has none) and its id-based noreply address, which GitHub
// attributes to the account.
func noreplyIdentity(u ghUser) CommitIdentity {
	name := u.Name
	if name == "" {
		name = u.Login
	}
	return CommitIdentity{Name: name, Email: fmt.Sprintf("%d+%s@users.noreply.github.com", u.ID, u.Login)}
}

func (g *GithubPlatform) LoadFixTarget() (*FixTarget, error) {
	owner, name, err := parseRepoSlug(g.Repo)
	if err != nil {
		return nil, err
	}
	out, err := runGH("gh api pulls", "api", fmt.Sprintf("repos/%s/%s/pulls/%d", owner, name, g.PRNumber))
	if err != nil {
		return nil, err
	}
	var pr ghPullRequest
	if err := json.Unmarshal(out, &pr); err != nil {
		return nil, fmt.Errorf("parsing pull request: %w", err)
	}
	author, err := fetchGHUser(pr.User.Login)
	if err != nil {
		return nil, err
	}
	bot, err := fetchGHUser(BotLogin)
	if err != nil {
		return nil, err
	}
	return &FixTarget{
		Number:     pr.Number,
		HeadSHA:    pr.Head.SHA,
		HeadBranch: pr.Head.Ref,
		// A deleted fork has no head repo.
		SameRepo:  pr.Head.Repo != nil && strings.EqualFold(pr.Head.Repo.FullName, g.Repo),
		Author:    noreplyIdentity(author),
		Committer: noreplyIdentity(bot),
	}, nil
}

func (g *GithubPlatform) LoadOpenFindings() ([]PRFinding, error) {
	return FetchPRFindings(g.Repo, g.PRNumber, false)
}

func (g *GithubPlatform) LoadCommitMessages() ([]string, error) {
	owner, name, err := parseRepoSlug(g.Repo)
	if err != nil {
		return nil, err
	}
	out, err := runGH("gh api pulls commits", "api", "--paginate",
		fmt.Sprintf("repos/%s/%s/pulls/%d/commits", owner, name, g.PRNumber))
	if err != nil {
		return nil, err
	}
	// --paginate emits one JSON array per page, back to back.
	dec := json.NewDecoder(bytes.NewReader(out))
	var messages []string
	for {
		var page []struct {
			Commit struct {
				Message string `json:"message"`
			} `json:"commit"`
		}
		if err := dec.Decode(&page); err != nil {
			if errors.Is(err, io.EOF) {
				break
			}
			return nil, fmt.Errorf("parsing pull request commits: %w", err)
		}
		for _, c := range page {
			messages = append(messages, c.Commit.Message)
		}
	}
	return messages, nil
}

func (g *GithubPlatform) OpenThreadsStatus() (state, desc string, err error) {
	threads, err := FetchReviewThreads(g.Repo, g.PRNumber)
	if err != nil {
		return "", "", err
	}
	var unresolved []ReviewThread
	for _, t := range threads {
		if !t.Resolved {
			unresolved = append(unresolved, t)
		}
	}
	state, desc = openThreadsStatus(unresolved)
	return state, desc, nil
}

func (g *GithubPlatform) SetReviewStatus(sha, state, desc string) error {
	return PostReviewCommitStatus(g.Repo, sha, state, desc)
}

func (g *GithubPlatform) ReplyToFinding(f PRFinding, body string) error {
	if f.CommentURL == "" {
		return fmt.Errorf("finding %s has no comment URL", f.FixRef)
	}
	return ReplyToReviewComment(f.CommentURL, body)
}

func (g *GithubPlatform) PostNotice(key, body string) error {
	owner, name, err := parseRepoSlug(g.Repo)
	if err != nil {
		return err
	}
	out, err := runGH("gh api issue comments", "api", "--paginate", "--jq", ".[].body",
		fmt.Sprintf("repos/%s/%s/issues/%d/comments", owner, name, g.PRNumber))
	if err != nil {
		return err
	}
	if strings.Contains(string(out), key) {
		fmt.Fprintf(os.Stderr, "Notice already posted on pull request #%d — not posting it again\n", g.PRNumber)
		return nil
	}
	return PostComment(g.Repo, g.PRNumber, key+"\n"+body)
}

// Push pushes HEAD to the head branch with the installation token in
// GH_TOKEN. A push made with that token, unlike one with the workflow's
// GITHUB_TOKEN, triggers workflows, so CI and the next review run on the fix.
// The token travels as an HTTP header in the environment, never in the
// remote URL or .git/config. The push is a plain fast-forward: if the branch
// moved since the checkout, it is rejected rather than overwritten.
func (g *GithubPlatform) Push(target *FixTarget) error {
	token := os.Getenv("GH_TOKEN")
	if token == "" {
		return fmt.Errorf("GH_TOKEN is not set — the fix pushes with the CodeCanary app's installation token")
	}
	server := strings.TrimSuffix(os.Getenv("GITHUB_SERVER_URL"), "/")
	if server == "" {
		server = "https://github.com"
	}
	auth := base64.StdEncoding.EncodeToString([]byte("x-access-token:" + token))
	env := []string{
		"GIT_CONFIG_COUNT=1",
		"GIT_CONFIG_KEY_0=http." + server + "/.extraheader",
		"GIT_CONFIG_VALUE_0=AUTHORIZATION: basic " + auth,
		"GIT_TERMINAL_PROMPT=0",
	}
	_, err := fixGit("", env, "push", "-q", server+"/"+g.Repo+".git", "HEAD:refs/heads/"+target.HeadBranch)
	return err
}
