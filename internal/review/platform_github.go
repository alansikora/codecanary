package review

import (
	"encoding/json"
	"fmt"
	"os"
	"strconv"
	"strings"
)

// allResolved checks if all review threads have been resolved by code changes.
// Threads resolved by other reasons (dismissed, acknowledged, rebutted) are kept
// open and do not count as resolved.
func allResolved(threads []ReviewThread, fixed []fixedThread) bool {
	fixedSet := make(map[int]bool, len(fixed))
	for _, f := range fixed {
		if isTrueResolution(f.Reason) {
			fixedSet[f.Index] = true
		}
	}
	for i := range threads {
		if !fixedSet[i] {
			return false
		}
	}
	return true
}

// resolvedFindingIDs builds the set of finding IDs that are resolved.
// It combines threads already resolved on GitHub with threads just fixed by code changes.
// Threads resolved by other reasons (dismissed, acknowledged, rebutted) are not included
// since they are kept open for re-triage.
func resolvedFindingIDs(allThreads, unresolved []ReviewThread, fixed []fixedThread) map[string]bool {
	resolved := make(map[string]bool)
	for _, t := range allThreads {
		if t.Resolved {
			if id := FindingIDFromThread(t.Body); id != "" {
				resolved[id] = true
			}
		}
	}
	fixedSet := make(map[int]bool, len(fixed))
	for _, f := range fixed {
		if isTrueResolution(f.Reason) {
			fixedSet[f.Index] = true
		}
	}
	for i, t := range unresolved {
		if fixedSet[i] {
			if id := FindingIDFromThread(t.Body); id != "" {
				resolved[id] = true
			}
		}
	}
	return resolved
}

// minimizeFullyResolvedReviews minimizes reviews whose findings are all resolved.
func minimizeFullyResolvedReviews(repo string, prNumber int, resolvedIDs map[string]bool) {
	reviews, err := FindReviews(repo, prNumber)
	if err != nil {
		fmt.Fprintf(os.Stderr, "Warning: could not fetch reviews for minimization: %v\n", err)
		return
	}
	minimized := 0
	for _, rev := range reviews {
		allResolved := len(rev.FindingIDs) > 0
		for _, fid := range rev.FindingIDs {
			if !resolvedIDs[fid] {
				allResolved = false
				break
			}
		}
		if !allResolved {
			continue
		}
		if err := MinimizeComment(rev.NodeID); err != nil {
			fmt.Fprintf(os.Stderr, "Warning: could not minimize review: %v\n", err)
		} else {
			minimized++
		}
	}
	if minimized > 0 {
		fmt.Fprintf(os.Stderr, "Minimized %d previous review(s)\n", minimized)
	}
}

// acknowledgmentMessage returns a reply body for non-code-change resolutions.
// Each message includes a hidden HTML marker for dedup detection.
func acknowledgmentMessage(reason string) string {
	marker := fmt.Sprintf("%s%s -->", ackMarkerPrefix, reason)
	switch reason {
	case "dismissed":
		return marker + "\nAuthor dismissed this finding. Keeping open \u2014 will re-check if related code changes."
	case "acknowledged":
		return marker + "\nAuthor acknowledged this finding. Keeping open \u2014 will re-check on future pushes."
	case "rebutted":
		return marker + "\nAuthor provided a technical rebuttal. Keeping open \u2014 will re-check if related code changes."
	default:
		return fmt.Sprintf("%sunknown -->", ackMarkerPrefix) + "\nFinding acknowledged. Keeping open \u2014 will re-check on future pushes."
	}
}

// acceptedReplyMessage is the reply on a thread resolve_accepted_replies
// closes because the author's reply settled it. It carries the ack marker,
// so the thread counts as handled wherever it is read again.
func acceptedReplyMessage(reason, rationale string) string {
	marker := fmt.Sprintf("%s%s -->", ackMarkerPrefix, reason)
	msg := marker + "\nReply accepted — resolving"
	// The rationale is model output written from an untrusted reply:
	// escape it so it cannot carry a marker (an ack would close the finding).
	if r := escapeAllTags(strings.TrimSpace(rationale)); r != "" {
		msg += ": " + r
	} else {
		msg += "."
	}
	return msg
}

// threadSettledByBot reports whether the bot already settled t without
// resolving it: an ack reply, or a "fixed" verdict the resolve step has yet
// to act on. Neither counts as open or blocking. The finding's own author,
// the bot, must have written the fixed reply, as triage requires.
func threadSettledByBot(t ReviewThread) bool {
	return hasAcknowledgmentReply(t) || hasFixedReply(t, t.Author)
}

// hasAcknowledgmentReply reports whether the thread already carries any
// codecanary ack reply. Dedup is intentionally reason-agnostic: all three
// ack reasons (dismissed/rebutted/acknowledged) keep the thread open and
// convey the same outcome to the author, and triage classification across
// runs is not deterministic — so checking only the same reason let a
// rebutted ack get followed by a dismissed ack on the next run, stacking
// two replies on one skip. One ack per thread is enough.
func hasAcknowledgmentReply(t ReviewThread) bool {
	for _, r := range t.Replies {
		if strings.Contains(r.Body, ackMarkerPrefix) || strings.Contains(r.Body, legacyAckPrefix) {
			return true
		}
	}
	return false
}

// GithubPlatform implements ReviewPlatform for GitHub PR mode. It always
// posts to the PR; "local preview" against a GitHub PR is no longer a thing —
// `codecanary review` without --post uses LocalPlatform.
type GithubPlatform struct {
	Repo     string
	PRNumber int
	DryRun   bool
	// Updates drives the outdated-install note on posted reviews; the
	// zero value disables it.
	Updates UpdateCheck
	// FixOnCI is the repo's fix.enabled. Publish reports it as the action's
	// `fix` output, which, with `findings`, gates the workflow's fix job.
	FixOnCI bool
	// StatusContext names the commit status (status_context); "" means
	// DefaultStatusContext. Set by LoadRules from the base branch's config.
	StatusContext string
	// ResolveAcceptedReplies is the config's resolve_accepted_replies, set
	// by LoadRules.
	ResolveAcceptedReplies bool
	// ReplyOnly marks a run triggered by a thread reply. It never posts a
	// new top-level review for a head no review has run on: that head's
	// push review is queued behind it and posts its own.
	ReplyOnly bool
	// reviewOn is the config's review_on, set by LoadRules.
	reviewOn string
	// deferredResolves are the threads this run settled but its token could
	// not resolve; see resolveOrDefer.
	deferredResolves []string
	// lastStatus is the review status this run last posted. With deferred
	// threads it goes to the action as `resolve_status`, so a resolve step
	// that fails can say so on that status; see writeResolveOutput.
	lastStatus *ResolveStatus
	// resolveThread replaces ResolveThread in tests.
	resolveThread func(threadID string) error
	// replyToThread replaces ReplyToThread in tests.
	replyToThread func(threadID, body string) error
}

// statusContext is the name of the commit status the platform posts.
func (g *GithubPlatform) statusContext() string {
	if g.StatusContext != "" {
		return g.StatusContext
	}
	return DefaultStatusContext
}

// setStatus posts the review status on sha, logging the outcome. A failed
// post is a warning: what the run published stays published.
func (g *GithubPlatform) setStatus(sha, state, desc string) {
	if sha == "" || g.DryRun {
		return
	}
	if err := g.SetReviewStatus(sha, state, desc); err != nil {
		fmt.Fprintf(os.Stderr, "Warning: could not post %s commit status: %v\n", g.statusContext(), err)
		return
	}
	Stderrf(ansiGreen, "Posted %s = %s on %s (%s)\n", g.statusContext(), state, shortSHA(sha), desc)
	g.lastStatus = &ResolveStatus{SHA: sha, Context: g.statusContext(), Description: desc}
	g.writeResolveOutput()
}

func (g *GithubPlatform) SetRunStatus(sha, state, desc string) {
	g.setStatus(sha, state, desc)
}

func (g *GithubPlatform) LoadPreviousFindings() ([]ReviewThread, string, int) {
	allThreads, err := FetchReviewThreads(g.Repo, g.PRNumber)
	if err != nil {
		fmt.Fprintf(os.Stderr, "Warning: could not fetch review threads: %v\n", err)
		return nil, "", 0
	}
	startIndex := len(allThreads)
	var unresolved []ReviewThread
	for _, t := range allThreads {
		if !t.Resolved {
			unresolved = append(unresolved, t)
		}
	}
	previousSHA := FetchPreviousReviewSHA(g.Repo, g.PRNumber)
	return unresolved, previousSHA, startIndex
}

func (g *GithubPlatform) ExcludedAuthor(threads []ReviewThread) string {
	if len(threads) > 0 {
		if login := threads[0].Author; login != "" {
			return login
		}
		fmt.Fprintf(os.Stderr, "Warning: could not determine bot login from thread author\n")
	}
	return ""
}

func (g *GithubPlatform) HandleResolutions(threads []ReviewThread, fixed []fixedThread) {
	for _, f := range fixed {
		if f.Index < 0 || f.Index >= len(threads) {
			continue
		}
		t := threads[f.Index]
		label := threadLabel(t)
		if isTrueResolution(f.Reason) {
			// Post an explanatory comment for file_removed before resolving.
			if f.Reason == "file_removed" {
				msg := "File removed from PR — resolving."
				if err := ReplyToThread(t.ID, msg); err != nil {
					fmt.Fprintf(os.Stderr, "  ! %s — failed to post file-removed comment: %v\n", label, err)
				}
			}
			if g.resolveOrDefer(t, "resolved, but failed to update thread") && f.Reason == "code_change" {
				g.recordFixedVerdict(t)
			}
		} else if g.ResolveAcceptedReplies {
			// The reply settles the finding: say so and close the thread.
			msg := acceptedReplyMessage(f.Reason, f.Rationale)
			if err := ReplyToThread(t.ID, msg); err != nil {
				fmt.Fprintf(os.Stderr, "  ! %s — failed to post acknowledgment: %v\n", label, err)
			}
			g.resolveOrDefer(t, "accepted, but failed to resolve the thread")
		} else {
			if !hasAcknowledgmentReply(t) {
				msg := acknowledgmentMessage(f.Reason)
				if err := ReplyToThread(t.ID, msg); err != nil {
					fmt.Fprintf(os.Stderr, "  ! %s — failed to post acknowledgment: %v\n", label, err)
				}
			}
		}
	}
	g.writeResolveOutput()
}

// resolveOrDefer resolves t on GitHub. GitHub refuses resolveReviewThread to
// an app installation token without contents: write, and the action's review
// token is deliberately contents: read (the model reviews an untrusted diff
// next to it). Such a thread is deferred instead: its ID goes into the
// `resolve_threads` step output, and the action's next step resolves it with
// `codecanary resolve` and a token minted after the model has finished.
// Outside GitHub Actions there is no next step, so the refusal is reported.
// It reports whether the thread was deferred.
func (g *GithubPlatform) resolveOrDefer(t ReviewThread, failure string) bool {
	resolve := g.resolveThread
	if resolve == nil {
		resolve = ResolveThread
	}
	err := resolve(t.ID)
	if err == nil {
		return false
	}
	label := threadLabel(t)
	switch {
	case !isResolvePermissionError(err):
		fmt.Fprintf(os.Stderr, "  ! %s — %s: %v\n", label, failure, err)
	case os.Getenv("GITHUB_OUTPUT") == "":
		fmt.Fprintf(os.Stderr, "  ! %s — the thread stays open: this token cannot resolve review threads (GitHub requires contents: write); resolve it on the PR\n", label)
	default:
		g.deferredResolves = append(g.deferredResolves, t.ID)
		fmt.Fprintf(os.Stderr, "  ~ %s (this token cannot resolve threads; left for the resolve step)\n", label)
		return true
	}
	return false
}

// recordFixedVerdict replies on a deferred thread that the review judged it
// fixed, with fixedMarker. The verdict otherwise lives only in this run: if
// the resolve step then fails, a re-run on the same head sees no new changes
// and would leave the finding open and blocking. With the reply, triage
// resolves it on any later run (TriagePreviouslyFixed). One reply per thread.
func (g *GithubPlatform) recordFixedVerdict(t ReviewThread) {
	for _, r := range t.Replies {
		if strings.Contains(r.Body, fixedMarker) {
			return
		}
	}
	reply := g.replyToThread
	if reply == nil {
		reply = ReplyToThread
	}
	if err := reply(t.ID, fixedMarker+"\nFixed by a code change — resolving."); err != nil {
		fmt.Fprintf(os.Stderr, "  ! %s — failed to record the fixed verdict on the thread: %v\n", threadLabel(t), err)
	}
}

// writeResolveOutput sets the `resolve_threads` step output to every thread
// deferred so far. HandleResolutions runs more than once per review and a
// step output keeps its last value, so each call writes the whole list.
// With them goes the review status last posted (`resolve_status`), which
// the action's report step amends when the threads can't be resolved.
func (g *GithubPlatform) writeResolveOutput() {
	if len(g.deferredResolves) == 0 {
		return
	}
	if err := writeGithubOutput(resolveThreadsOutput, strings.Join(g.deferredResolves, ",")); err != nil {
		fmt.Fprintf(os.Stderr, "Warning: could not write the %s output; %d thread(s) stay open: %v\n", resolveThreadsOutput, len(g.deferredResolves), err)
	}
	if g.lastStatus == nil {
		return
	}
	data, err := json.Marshal(g.lastStatus)
	if err == nil {
		err = writeGithubOutput(resolveStatusOutput, string(data))
	}
	if err != nil {
		fmt.Fprintf(os.Stderr, "Warning: could not write the %s output: %v\n", resolveStatusOutput, err)
	}
}

func (g *GithubPlatform) Publish(result *ReviewResult, pr *PRData, threads []ReviewThread, fixed []fixedThread) error {
	summary := computeReviewSummary(threads, fixed, result.Findings)
	// The workflow's fix job runs only when the repo enabled it and this
	// review left findings open.
	g.writeFixOutputs(openFindingCount(threads, fixed, result.Findings))

	// Decide edit-vs-post: if the latest CodeCanary review on the PR carries
	// the current HEAD SHA in its marker, this is either a reply-only run or
	// a duplicate synchronize webhook — refresh that review in place rather
	// than stacking another top-level comment.
	latest, latestErr := FetchLatestCodecanaryReview(g.Repo, g.PRNumber)
	if latestErr != nil {
		fmt.Fprintf(os.Stderr, "Warning: could not fetch latest review for dedup: %v\n", latestErr)
	}
	if latest != nil && result.SHA != "" && latest.SHA == result.SHA {
		updated := replaceSummaryBlock(latest.Body, summary)
		if updated == latest.Body {
			Stderrf(ansiGreen, "Latest review already current for %s — no update needed\n", shortSHA(result.SHA))
			// The status is refreshed anyway: a thread resolved since the
			// last run changes it without changing the body.
			g.postReviewCommitStatus(result.SHA, summary)
			return nil
		}
		if err := UpdateReviewBody(g.Repo, g.PRNumber, latest.ID, updated); err != nil {
			return fmt.Errorf("updating review body: %w", err)
		}
		Stderrf(ansiGreen, "Updated latest review on PR #%d\n", g.PRNumber)
		g.postReviewCommitStatus(result.SHA, summary)
		return nil
	}

	// A reply on a head no review has run on yet: under review_on: push
	// that head's review is queued behind this run and posts the result;
	// posting one here would mark the head reviewed before it is.
	if g.ReplyOnly && g.reviewOn != ReviewOnReady && result.SHA != "" && (latest == nil || latest.SHA != result.SHA) {
		Stderrf(ansiGreen, "Head %s is not reviewed yet — leaving the result to its review\n", shortSHA(result.SHA))
		return nil
	}

	// Minimize previous reviews before posting a fresh one.
	minimizeFailed := false
	if len(threads) > 0 {
		if nodeIDs, err := FindReviewNodeIDs(g.Repo, g.PRNumber); err == nil {
			if allResolved(threads, fixed) {
				for _, nodeID := range nodeIDs {
					if err := MinimizeComment(nodeID); err != nil {
						fmt.Fprintf(os.Stderr, "Warning: could not minimize review: %v\n", err)
						minimizeFailed = true
					}
				}
				if len(nodeIDs) > 0 && !minimizeFailed {
					fmt.Fprintf(os.Stderr, "Minimized %d previous review(s)\n", len(nodeIDs))
				}
			} else {
				allThreads, err := FetchReviewThreads(g.Repo, g.PRNumber)
				if err != nil {
					fmt.Fprintf(os.Stderr, "Warning: could not fetch review threads for minimization: %v\n", err)
				} else {
					resolvedIDs := resolvedFindingIDs(allThreads, threads, fixed)
					minimizeFullyResolvedReviews(g.Repo, g.PRNumber, resolvedIDs)
				}
			}
		} else {
			fmt.Fprintf(os.Stderr, "Warning: could not fetch reviews for minimization: %v\n", err)
			minimizeFailed = true
		}
	}

	// POST path — pick the body shape that fits the cycle outcome. Every
	// branch emits a top-level review so each push lands a visible status
	// comment on the PR. Open questions, the coverage note and the update
	// notice ride along in whichever body is posted; they never change which
	// one, and in-place edits above keep the notes the review was first
	// posted with. The full findings body already renders open questions and
	// coverage itself, so PostReview gets only the update notice.
	notice := g.Updates.Notice() + renderRulesChangedNote(result.RulesChanged) + renderCostNote(result.Cost)
	notes := formatOpenQuestions(result.Questions) + renderCoverageNote(result.Coverage) + notice
	switch {
	case len(result.Findings) > 0:
		if err := PostReview(g.Repo, g.PRNumber, result, pr.Files, pr.ValidationDiff(), result.SHA, notice, summary); err != nil {
			return fmt.Errorf("posting review: %w", err)
		}
		Stderrf(ansiGreen, "Review posted to PR #%d\n", g.PRNumber)
	case len(threads) > 0 && allResolved(threads, fixed):
		if err := PostAllClearReview(g.Repo, g.PRNumber, result.SHA, minimizeFailed, notes, summary); err != nil {
			return fmt.Errorf("posting all-clear review: %w", err)
		}
		Stderrf(ansiGreen, "All clear! No issues remaining.\n")
	case len(threads) > 0:
		if err := PostActivityReview(g.Repo, g.PRNumber, result.SHA, notes, summary); err != nil {
			return fmt.Errorf("posting activity review: %w", err)
		}
		Stderrf(ansiGreen, "Posted activity summary to PR #%d\n", g.PRNumber)
	default:
		if err := PostCleanReview(g.Repo, g.PRNumber, result.SHA, notes, summary); err != nil {
			return fmt.Errorf("posting review: %w", err)
		}
		Stderrf(ansiGreen, "Review posted to PR #%d\n", g.PRNumber)
	}

	g.postReviewCommitStatus(result.SHA, summary)
	return nil
}

func (g *GithubPlatform) SaveState(_ *ReviewResult, _ []Finding, _ bool) error {
	// No-op: GitHub mode stores state in PR review threads (embedded JSON
	// markers carry the SHA and findings). Local state files are owned by
	// LocalPlatform — this keeps the two adapters from fighting over the
	// same ~/.codecanary/state/<branch>.json file.
	return nil
}

func (g *GithubPlatform) GetIncrementalDiff(previousSHA string, pr *PRData) (string, error) {
	baseRef := fetchIncrementalHistory(previousSHA, pr.BaseBranch)
	return IncrementalDiff(previousSHA, baseRef)
}

func (g *GithubPlatform) ReportUsage(tracker *UsageTracker) {
	report := tracker.Report(g.Repo, g.PRNumber)
	if len(report.Calls) > 0 {
		if err := WriteUsageEnv(report); err != nil {
			fmt.Fprintf(os.Stderr, "Warning: could not write usage env: %v\n", err)
		}
	}
}

// postReviewCommitStatus POSTs a `CodeCanary / review` commit status on the
// reviewed SHA. state=failure while any blocking finding (severity at or
// above blockingSeverity) is unresolved — new this cycle or still open with
// no classification; state=success otherwise. Teams can require this check
// in branch protection to gate merges on a clean review.
//
// Skipped silently when the SHA is empty (non-pr-loop contexts that
// accidentally share the adapter). Failures are logged as warnings — the
// review itself has already been published, so a flaky status post should
// not abort the whole Publish flow.
func (g *GithubPlatform) postReviewCommitStatus(sha string, summary ReviewSummary) {
	if sha == "" {
		return
	}
	state, desc := commitStatusFromSummary(summary)
	g.setStatus(sha, state, desc)
}

// openFindingCount is how many findings a review leaves for the author: the
// new ones, plus the earlier threads this cycle neither resolved nor
// classified and that carry no acknowledgment from an earlier cycle.
func openFindingCount(threads []ReviewThread, fixed []fixedThread, newFindings []Finding) int {
	handled := make(map[int]bool, len(fixed))
	for _, f := range fixed {
		handled[f.Index] = true
	}
	n := len(newFindings)
	for i, t := range threads {
		if !handled[i] && !threadSettledByBot(t) {
			n++
		}
	}
	return n
}

// writeFixOutputs sets the step outputs the workflow's fix job is gated on.
func (g *GithubPlatform) writeFixOutputs(openFindings int) {
	for key, value := range map[string]string{
		"findings": strconv.Itoa(openFindings),
		"fix":      strconv.FormatBool(g.FixOnCI),
	} {
		if err := writeGithubOutput(key, value); err != nil {
			fmt.Fprintf(os.Stderr, "Warning: could not write the %s output: %v\n", key, err)
		}
	}
}

// writeGithubOutput sets a step output for the action (the action maps it to
// its own output). A no-op outside GitHub Actions.
func writeGithubOutput(key, value string) error {
	path := os.Getenv("GITHUB_OUTPUT")
	if path == "" {
		return nil
	}
	f, err := os.OpenFile(path, os.O_APPEND|os.O_WRONLY, 0644)
	if err != nil {
		return err
	}
	defer func() { _ = f.Close() }()
	_, err = fmt.Fprintf(f, "%s=%s\n", key, value)
	return err
}

// githubEventPayload is the part of the workflow's event payload the review
// reads.
type githubEventPayload struct {
	// Action is e.g. "synchronize", "ready_for_review", "labeled".
	Action string `json:"action"`
	Label  *struct {
		Name string `json:"name"`
	} `json:"label"`
	Changes struct {
		Body *struct{} `json:"body"`
	} `json:"changes"`
}

// githubEvent reads the event the workflow runs for; the zero value outside
// Actions.
func githubEvent() githubEventPayload {
	var event githubEventPayload
	path := os.Getenv("GITHUB_EVENT_PATH")
	if path == "" {
		return event
	}
	data, err := os.ReadFile(path)
	if err != nil {
		return event
	}
	_ = json.Unmarshal(data, &event)
	return event
}

// metadataEventSkip decides a pull request event that changes no code —
// a label or the PR body. It reviews only when it changes something the
// review depends on (a tier label; the body, under intent review) and the
// head has not been reviewed yet: a PR skipped as tier low and relabeled, or
// one stopped for a missing intent section whose body was then fixed.
// Otherwise the change applies from the next push. reason explains a skip;
// "" means review.
func metadataEventSkip(cfg *ReviewConfig, event githubEventPayload, headReviewed func() bool) (skip bool, reason string) {
	switch event.Action {
	case "labeled", "unlabeled":
		if event.Label == nil || !cfg.IsTierLabel(event.Label.Name) {
			return true, "label is not a review tier"
		}
	case "edited":
		if event.Changes.Body == nil || !cfg.IntentEnabled() {
			return true, "edit does not change what the review reads"
		}
	default:
		return false, ""
	}
	if headReviewed() {
		return true, "this head was already reviewed; the change applies from the next push"
	}
	return false, ""
}

// SkipReview skips a push to a ready PR under review_on: ready. Statuses are
// keyed by commit, so the new HEAD would otherwise have no CodeCanary status
// and a required check would block the PR; it gets one derived from the open
// threads instead, without calling the model.
func (g *GithubPlatform) SkipReview(cfg *ReviewConfig, replyOnly bool) (bool, error) {
	event := githubEvent()
	if !replyOnly {
		skip, reason := metadataEventSkip(cfg, event, func() bool {
			head, err := currentHEAD()
			return err == nil && FetchPreviousReviewSHA(g.Repo, g.PRNumber) == head
		})
		if skip {
			fmt.Fprintf(os.Stderr, "Not reviewing on %q: %s\n", event.Action, reason)
			return true, nil
		}
	}
	if !skipsPush(cfg, replyOnly, event.Action) {
		return false, nil
	}
	fmt.Fprintf(os.Stderr, "review_on: ready — a push to a ready pull request is not reviewed; move it to draft and back to ready for a new review\n")
	sha, err := currentHEAD()
	if err != nil {
		return true, fmt.Errorf("skipped push: %w", err)
	}
	threads, _, _ := g.LoadPreviousFindings()
	state, desc := skippedPushStatus(threads)
	if err := g.SetReviewStatus(sha, state, desc); err != nil {
		return true, fmt.Errorf("posting %s status for a skipped push: %w", g.statusContext(), err)
	}
	Stderrf(ansiGreen, "Posted %s = %s on %s (%s)\n", g.statusContext(), state, shortSHA(sha), desc)
	return true, nil
}

// StopAtRoundLimit counts the heads other than sha that carry a CodeCanary
// review. Below the tier's max_rounds the run reviews. At it, sha gets the
// status the open threads warrant without a model call, and the PR a notice
// once per limit. A failure to read the reviews or threads, or to post the
// status, is an error: guessing would either spend a round or pass a head
// that has blocking findings.
func (g *GithubPlatform) StopAtRoundLimit(sha string, tier Tier) (bool, error) {
	maxRounds := tier.Config.MaxRounds
	reviewed, err := FetchReviewedSHAs(g.Repo, g.PRNumber)
	if err != nil {
		return false, fmt.Errorf("counting review rounds: %w", err)
	}
	rounds := roundsBefore(reviewed, sha)
	if rounds < maxRounds {
		fmt.Fprintf(os.Stderr, "Tier %s — round %d of %d\n", tier.Name, rounds+1, maxRounds)
		return false, nil
	}
	Stderrf(ansiYellow, "Tier %s — review limit reached (%d rounds); not reviewing %s\n", tier.Name, maxRounds, shortSHA(sha))
	all, err := FetchReviewThreads(g.Repo, g.PRNumber)
	if err != nil {
		return true, fmt.Errorf("review limit reached: fetching review threads: %w", err)
	}
	var open []ReviewThread
	for _, t := range all {
		if !t.Resolved {
			open = append(open, t)
		}
	}
	state, desc := roundLimitStatus(open, maxRounds)
	if err := g.SetReviewStatus(sha, state, desc); err != nil {
		return true, fmt.Errorf("posting %s status at the review limit: %w", g.statusContext(), err)
	}
	Stderrf(ansiGreen, "Posted %s = %s on %s (%s)\n", g.statusContext(), state, shortSHA(sha), desc)
	if err := g.PostNotice(roundLimitNoticeKey(maxRounds), roundLimitNotice(tier, g.statusContext())); err != nil {
		fmt.Fprintf(os.Stderr, "Warning: could not post the review limit notice: %v\n", err)
	}
	return true, nil
}

// roundsBefore is how many review rounds a head has behind it: the reviewed
// heads other than itself. A head that is itself reviewed is in its round.
func roundsBefore(reviewed []string, sha string) int {
	n := 0
	for _, s := range reviewed {
		if s != sha {
			n++
		}
	}
	return n
}

// roundLimitStatus is the commit status for a head past the round limit:
// the open threads' status, marked as not reviewed.
func roundLimitStatus(threads []ReviewThread, maxRounds int) (state, desc string) {
	state, desc = openThreadsStatus(threads)
	return state, fmt.Sprintf("%s; review limit reached (%d rounds)", desc, maxRounds)
}

// roundLimitNoticeKey makes the limit notice once per PR and limit, so a
// raised limit that is reached again gets a fresh notice.
func roundLimitNoticeKey(maxRounds int) string {
	return fmt.Sprintf("<!-- codecanary:round-limit %d -->", maxRounds)
}

// roundLimitNotice is the PR comment posted when a tier's round limit stops
// the review.
func roundLimitNotice(tier Tier, statusContext string) string {
	name := strings.ReplaceAll(tier.Name, "`", "'")
	ctx := strings.ReplaceAll(statusContext, "`", "'")
	plural := "s"
	if tier.Config.MaxRounds == 1 {
		plural = ""
	}
	return fmt.Sprintf("## \U0001F425 CodeCanary\n\n"+
		"This PR has been reviewed %d time%s, the limit for tier `%s`, so later pushes are not reviewed. "+
		"Each new commit gets a `%s` status from the findings still open: they stay open, and blocking ones keep it failing, until they are resolved. "+
		"A thread resolved by hand is picked up on the next push or reply.\n\n"+
		"To review this PR again, raise `max_rounds` for `%s` in `.codecanary/config.yml` on the base branch.\n",
		tier.Config.MaxRounds, plural, name, ctx, name)
}

// skippedPushStatus is the commit status for a push review_on: ready didn't
// review: the open threads' status, marked as not reviewed.
func skippedPushStatus(threads []ReviewThread) (state, desc string) {
	state, desc = openThreadsStatus(threads)
	return state, desc + "; push not reviewed (review_on: ready)"
}

// openThreadsStatus is the commit status the unresolved threads warrant
// without a review: the same rule as a reviewed push, applied to the threads
// still open. Threads the author already answered and the bot acknowledged
// count as handled, as they do in a review.
func openThreadsStatus(threads []ReviewThread) (state, desc string) {
	var open []ReviewThread
	for _, t := range threads {
		if !threadSettledByBot(t) {
			open = append(open, t)
		}
	}
	return commitStatusFromSummary(computeReviewSummary(open, nil, nil))
}

// reviewNoticeKey is the hidden marker that makes a review notice
// once-per-head, like fixNoticeKey for the fix: kind is "failed" or
// "replies-failed", sha the head it was posted for.
func reviewNoticeKey(kind, sha string) string {
	return fmt.Sprintf("<!-- codecanary:review-notice %s %s -->", kind, sha)
}

// buildReviewFailureNotice is the comment posted when a run did not finish.
// The cause is failureSummary's one line, as in the fix's failure notice,
// with trailing punctuation trimmed so the sentence ends cleanly; the full
// error stays in the workflow logs. A later failure on the same head edits
// the notice, but a later success does not remove it, so it points to where
// the current result is shown.
func buildReviewFailureNotice(sha string, runErr error, replyOnly bool, runURL, statusContext string) string {
	what := fmt.Sprintf("The review did not finish on `%s`, so this commit was **not reviewed**", shortSHA(sha))
	if replyOnly {
		what = fmt.Sprintf("The latest replies on `%s` were not processed", shortSHA(sha))
	}
	cause := strings.TrimRight(failureSummary(runErr), ".: ")
	body := fmt.Sprintf("## \U0001F425 CodeCanary\n\n\u26a0\ufe0f %s: %s.\n\nFix the cause and re-run the job.", what, cause)
	if runURL != "" {
		body += fmt.Sprintf(" [Workflow run](%s)", runURL)
	}
	body += "\n\nThis notice is not removed when a later run succeeds: the latest review and the `" + statusContext + "` status show the current result."
	return body + "\n"
}

// ReportFailure posts a PR notice, one per head, so a failed run never
// looks like a clean review. A re-run that fails again on the head edits
// it, so it shows the latest cause. The "error" commit status is Run's
// (SetRunStatus), not posted here.
func (g *GithubPlatform) ReportFailure(runErr error, replyOnly bool) {
	if g.Repo == "" || g.PRNumber == 0 {
		return
	}
	sha, err := currentHEAD()
	if err != nil {
		fmt.Fprintf(os.Stderr, "Warning: could not report the failed run: %v\n", err)
		return
	}
	kind := "failed"
	if replyOnly {
		kind = "replies-failed"
	}
	body := buildReviewFailureNotice(sha, runErr, replyOnly, workflowRunURL(), g.statusContext())
	if err := g.UpsertNotice(reviewNoticeKey(kind, sha), body); err != nil {
		fmt.Fprintf(os.Stderr, "Warning: could not post failure notice: %v\n", err)
	}
}

// workflowRunURL links the current GitHub Actions run, or "" outside Actions.
func workflowRunURL() string {
	server, repo, id := os.Getenv("GITHUB_SERVER_URL"), os.Getenv("GITHUB_REPOSITORY"), os.Getenv("GITHUB_RUN_ID")
	if server == "" || repo == "" || id == "" {
		return ""
	}
	return fmt.Sprintf("%s/%s/actions/runs/%s", server, repo, id)
}

// gateMarker keys a gate review to the head it was posted for, so a re-run
// on the same head does not post it twice.
func gateMarker(sha string) string {
	return fmt.Sprintf("<!-- codecanary:gate %s -->", sha)
}

// PublishGate posts the blockers that stopped the review as one top-level
// review, once per head, and fails the status. The review carries no
// baseline marker, so the head still counts as not reviewed.
func (g *GithubPlatform) PublishGate(sha string, blockers []string) error {
	g.setStatus(sha, "failure", statusCounts(len(blockers), 0))
	key := gateMarker(sha)
	if reviews, err := fetchPRReviews(g.Repo, g.PRNumber); err == nil {
		for _, r := range reviews {
			if strings.Contains(r.Body, key) {
				Stderrf(ansiGreen, "Gate already reported for %s\n", shortSHA(sha))
				return nil
			}
		}
	}
	if err := postSimpleReview(g.Repo, g.PRNumber, formatGateBody(sha, blockers)); err != nil {
		return fmt.Errorf("posting gate review: %w", err)
	}
	Stderrf(ansiYellow, "Not reviewed: %d blocking issue(s) posted to PR #%d\n", len(blockers), g.PRNumber)
	return nil
}

// formatGateBody renders the gate review.
func formatGateBody(sha string, blockers []string) string {
	var b strings.Builder
	fmt.Fprintf(&b, "## \U0001F425 CodeCanary\n\nThis PR was not reviewed at %s:\n\n", shortSHA(sha))
	for _, bl := range blockers {
		fmt.Fprintf(&b, "- %s %s\n", blockingTag, bl)
	}
	b.WriteString("\nThe review runs once this is fixed.\n")
	fmt.Fprintf(&b, "\n%s\n", gateMarker(sha))
	return b.String()
}

// AnswerRejectedReplies posts one reply per rejected author reply. The reply
// carries rejectedReplyMarker, which hasNewHumanReply treats as the point
// after which a new author reply is needed, so the same reply is not
// answered again; the finding stays open and blocking.
func (g *GithubPlatform) AnswerRejectedReplies(threads []ReviewThread, rejected []fixedThread) {
	for _, f := range rejected {
		if f.Index < 0 || f.Index >= len(threads) {
			continue
		}
		t := threads[f.Index]
		if err := ReplyToThread(t.ID, rejectedReplyMessage(f.Rationale)); err != nil {
			fmt.Fprintf(os.Stderr, "  ! %s — failed to answer the reply: %v\n", threadLabel(t), err)
		}
	}
}

// rejectedReplyMessage is the answer to an author reply triage did not
// accept.
func rejectedReplyMessage(rationale string) string {
	msg := rejectedReplyMarker + "\nThis reply does not settle the finding, so it stays open"
	// The rationale is model output written from an untrusted reply:
	// escape it so it cannot carry a marker (an ack would close the finding).
	if r := escapeAllTags(strings.TrimSpace(rationale)); r != "" {
		msg += ": " + r
	} else {
		msg += "."
	}
	return msg + "\n\nFix the code, or reply with the specific reason it does not apply."
}

// PostNotice posts body as a pull request comment unless the bot already
// posted one carrying key.
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

// UpsertNotice posts body as a pull request comment carrying key, or edits
// the bot's latest comment that carries it.
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
// notices read back.
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
