package review

import (
	"encoding/base64"
	"encoding/json"
	"fmt"
	"os"
	"regexp"
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
	commits, err := decodePaginated[struct {
		Commit struct {
			Message string `json:"message"`
		} `json:"commit"`
	}](out)
	if err != nil {
		return nil, fmt.Errorf("parsing pull request commits: %w", err)
	}
	messages := make([]string, 0, len(commits))
	for _, c := range commits {
		messages = append(messages, c.Commit.Message)
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
	comments, err := fetchIssueComments(g.Repo, g.PRNumber)
	if err != nil {
		return err
	}
	for _, c := range comments {
		if isBotAuthor(c.User.Login) && strings.Contains(c.Body, key) {
			fmt.Fprintf(os.Stderr, "Notice already posted on pull request #%d — not posting it again\n", g.PRNumber)
			return nil
		}
	}
	return PostComment(g.Repo, g.PRNumber, key+"\n"+body)
}

func (g *GithubPlatform) UpsertNotice(key, body string) error {
	comments, err := fetchIssueComments(g.Repo, g.PRNumber)
	if err != nil {
		return err
	}
	for i := len(comments) - 1; i >= 0; i-- {
		c := comments[i]
		if !isBotAuthor(c.User.Login) || !strings.Contains(c.Body, key) {
			continue
		}
		owner, name, err := parseRepoSlug(g.Repo)
		if err != nil {
			return err
		}
		payload, err := json.Marshal(struct {
			Body string `json:"body"`
		}{Body: key + "\n" + body})
		if err != nil {
			return fmt.Errorf("marshaling comment payload: %w", err)
		}
		_, err = ghAPIRequest("PATCH", fmt.Sprintf("repos/%s/%s/issues/comments/%d", owner, name, c.ID), payload)
		return err
	}
	return PostComment(g.Repo, g.PRNumber, key+"\n"+body)
}

// issueComment is the subset of a pull request's conversation comment the
// fix reads back.
type issueComment struct {
	ID      int64  `json:"id"`
	Body    string `json:"body"`
	HTMLURL string `json:"html_url"`
	User    struct {
		Login string `json:"login"`
	} `json:"user"`
}

// fetchIssueComments returns a pull request's conversation comments, oldest
// first.
func fetchIssueComments(repo string, prNumber int) ([]issueComment, error) {
	owner, name, err := parseRepoSlug(repo)
	if err != nil {
		return nil, err
	}
	out, err := runGH("gh api issue comments", "api", "--paginate",
		fmt.Sprintf("repos/%s/%s/issues/%d/comments", owner, name, prNumber))
	if err != nil {
		return nil, err
	}
	all, err := decodePaginated[issueComment](out)
	if err != nil {
		return nil, fmt.Errorf("parsing pull request comments: %w", err)
	}
	return all, nil
}

// FixStop reports that the CI fix gave up on a pull request's head and left
// the open findings to the author: `codecanary findings` surfaces it as
// `fix_stop`, the signal for the codecanary-fix skill to take over.
type FixStop struct {
	// Kind is "limit" (a loop limit was reached), "failed" (the fix
	// could not finish: provider error, rate limit, rejected push) or
	// "left-open" (it committed nothing and left findings to the author).
	Kind string `json:"kind"`
	// Reason is the human-visible reason, e.g. "stopped after 2 fixes
	// since your last push".
	Reason string `json:"reason"`
	// CommentURL is the bot's notice comment, when one was found.
	CommentURL string `json:"comment_url,omitempty"`
}

var (
	limitNoticeReasonRe    = regexp.MustCompile(`CodeCanary (stopped after \d+ fix(?:es)? (?:since your last push|on this PR))`)
	failedNoticeReasonRe   = regexp.MustCompile("did not finish on `[0-9a-f]+`: ([^\n]+?)\\.\n")
	leftOpenNoticeReasonRe = regexp.MustCompile(`with nothing to commit: it (left \d+ findings? open)`)
	stoppedStatusRe        = regexp.MustCompile(`; (stopped after \d+ fix(?:es)?)$`)
)

// FetchFixStop reports whether the CI fix stopped on headSHA, from the
// bot's notice comments for that head and, failing those, the
// `CodeCanary / review` status description. Nil when it did not stop, or
// stopped only on an earlier head (a push starts over).
func FetchFixStop(repo string, prNumber int, headSHA string) (*FixStop, error) {
	if headSHA == "" {
		return nil, nil
	}
	comments, err := fetchIssueComments(repo, prNumber)
	if err != nil {
		return nil, err
	}
	desc, err := fetchReviewStatusDescription(repo, headSHA)
	if err != nil {
		return nil, err
	}
	return fixStopFromSignals(comments, desc, headSHA), nil
}

// fixStopFromSignals is FetchFixStop's decision on fetched data. The newest
// notice for headSHA wins; the status suffix covers a limit whose notice
// could not be posted.
func fixStopFromSignals(comments []issueComment, statusDesc, headSHA string) *FixStop {
	for i := len(comments) - 1; i >= 0; i-- {
		c := comments[i]
		if !isBotAuthor(c.User.Login) {
			continue
		}
		switch {
		case strings.Contains(c.Body, fixNoticeKey("limit", headSHA)):
			stop := &FixStop{Kind: "limit", Reason: "stopped by a fix limit", CommentURL: c.HTMLURL}
			if m := limitNoticeReasonRe.FindStringSubmatch(c.Body); m != nil {
				stop.Reason = m[1]
			}
			return stop
		case strings.Contains(c.Body, fixNoticeKey("failed", headSHA)):
			stop := &FixStop{Kind: "failed", Reason: "the fix did not finish", CommentURL: c.HTMLURL}
			if m := failedNoticeReasonRe.FindStringSubmatch(c.Body); m != nil {
				stop.Reason = m[1]
			}
			return stop
		case strings.Contains(c.Body, fixNoticeKey("left-open", headSHA)):
			stop := &FixStop{Kind: "left-open", Reason: "findings left open for the author", CommentURL: c.HTMLURL}
			if m := leftOpenNoticeReasonRe.FindStringSubmatch(c.Body); m != nil {
				stop.Reason = m[1]
			}
			return stop
		}
	}
	if m := stoppedStatusRe.FindStringSubmatch(statusDesc); m != nil {
		return &FixStop{Kind: "limit", Reason: m[1]}
	}
	return nil
}

// fetchReviewStatusDescription returns the description of the latest
// `CodeCanary / review` commit status on sha, or "" when there is none.
func fetchReviewStatusDescription(repo, sha string) (string, error) {
	owner, name, err := parseRepoSlug(repo)
	if err != nil {
		return "", err
	}
	out, err := runGH("gh api commit status", "api", fmt.Sprintf("repos/%s/%s/commits/%s/status", owner, name, sha))
	if err != nil {
		return "", err
	}
	var combined struct {
		Statuses []struct {
			Context     string `json:"context"`
			Description string `json:"description"`
		} `json:"statuses"`
	}
	if err := json.Unmarshal(out, &combined); err != nil {
		return "", fmt.Errorf("parsing commit status: %w", err)
	}
	// The combined status holds the latest status per context.
	for _, st := range combined.Statuses {
		if st.Context == ReviewCommitStatusContext {
			return st.Description, nil
		}
	}
	return "", nil
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
