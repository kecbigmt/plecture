package watcher

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"maps"
	"net/http"
	"os/exec"
	"regexp"
	"slices"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/kecbigmt/plect/contracts/event"
)

// resourceRE parses the GitHub issue/PR resource identifiers the watcher
// understands. Anything else is skipped with a warning (the subscription
// stays; a future watcher version may learn the shape).
var resourceRE = regexp.MustCompile(`^https://github\.com/([^/]+)/([^/]+)/(issues|pull)/(\d+)`)

// Observed is the set of GitHub values the watcher uses to detect meaningful
// transitions for notification delivery. Each *Resolved flag gates a value
// fetched by its own gh/REST call, split into several independent probes
// instead of one `gh pr view`/`gh issue view` call: a
// failed probe must leave its value out of outputsFor entirely so a transient
// error can never retract or falsely seed a baseline — only a resolved fetch
// is authoritative.
type Observed struct {
	State string // pr_state or issue_state depending on kind
	Kind  string // "pull" | "issues"

	ChecksStatus   string
	ChecksResolved bool

	// HeadSHA is the PR head commit (PR resources). A change is a new push.
	HeadSHA string

	// Draft mirrors the PR object's `draft` field, bundled in the same
	// all-or-nothing primary probe as State/HeadSHA (fetchPRCore) — no
	// separate Resolved flag needed for the direct-PR path.
	Draft bool

	// MergeableState is GitHub's mergeable_state vocabulary (clean/dirty/
	// unstable/blocked/behind/draft/has_hooks/...). MergeableResolved is
	// false whenever GitHub reports "unknown" (still computing) — that value
	// must never become an observed baseline or fire a notification, or a
	// PR would flap clean→unknown→clean on every poll while GitHub recomputes
	// mergeability.
	MergeableState    string
	MergeableResolved bool

	// LinkedPRResolved reports that the linked-PR discovery for an issue
	// resource SUCCEEDED (even if it found no PR). LinkedPRFound distinguishes
	// "successful lookup, no PR" (authoritative retraction of every PR-scoped
	// field below) from "lookup itself failed" (leave everything absent, never
	// retract a stale value on a transient error).
	LinkedPRResolved bool
	LinkedPRFound    bool

	// LinkedPRState/HeadSHA/ChecksStatus/MergeableState/Draft each have their
	// own Resolved flag because, unlike the direct-PR path, they come from a
	// SEPARATE follow-up `pulls/{n}` call made only after discovery finds a
	// PR number — that call can fail independently of discovery succeeding.
	LinkedPRState             string
	LinkedPRStateResolved     bool
	LinkedPRHeadSHA           string
	LinkedPRChecksStatus      string
	LinkedPRChecksResolved    bool
	LinkedPRMergeableState    string
	LinkedPRMergeableResolved bool
	LinkedPRDraft             bool
	LinkedPRDraftResolved     bool
}

// Poller drives one tick of the watch loop.
type Poller struct {
	Store  *Store
	Logger *slog.Logger
	// Bus, when set, is the tws event bus the watcher publishes github.* events
	// to (the P4 delivery path). When set it takes precedence over NotifyURL:
	// the tws session dispatcher, not POST /notify, fans the change out through
	// workflow channels. Leaving it nil keeps the legacy /notify path.
	Bus       *event.Client
	NotifyURL string // slack-adapter /notify endpoint; empty disables delivery
	GhBin     string // gh binary; default "gh"
	HTTP      *http.Client
}

func (p *Poller) gh() string {
	if p.GhBin != "" {
		return p.GhBin
	}
	return "gh"
}

func (p *Poller) httpClient() *http.Client {
	if p.HTTP != nil {
		return p.HTTP
	}
	return &http.Client{Timeout: 10 * time.Second}
}

// Tick polls every subscription once, deduplicating fetches per fetchKey
// (see that func for why branch is part of the key for issues but not PRs).
// Per-repo GraphQL batching is a future optimization (see ADR-001).
func (p *Poller) Tick() {
	subs, err := p.Store.All()
	if err != nil {
		p.Logger.Error("load subscriptions", "error", err)
		return
	}
	// Deterministic order, dedupe fetches per fetchKey.
	names := make([]string, 0, len(subs))
	for name := range subs {
		names = append(names, name)
	}
	sort.Strings(names)

	fetched := map[string]*Observed{} // fetch key → observation (nil = fetch failed)
	for _, name := range names {
		sub := subs[name]
		if sub == nil {
			continue
		}
		key := fetchKey(sub)
		obs, ok := fetched[key]
		if !ok {
			obs = p.fetch(sub)
			fetched[key] = obs
		}
		if obs == nil {
			continue
		}
		p.apply(sub, obs)
	}
}

// fetchKey is the per-tick fetch dedup key. branch is part of the key ONLY
// for issue resources: fetch reads sub.Branch solely to resolve an issue's
// linked PR (`gh api repos/.../pulls?head=...`), so tag variants of the same
// issue on different branches must fetch separately. A PR fetch ignores
// branch entirely, so PR subscriptions key on the resource alone — every
// subscriber of one PR shares the same set of `gh` calls no matter what
// branch (if any) it carries. This is what makes poll dedup independent of
// how the subscription was created: dispatch-time auto-subscribe (branch set)
// and runtime `tws subscribe` (branch empty) of the same PR collapse to one
// fetch.
func fetchKey(sub *Subscription) string {
	if m := resourceRE.FindStringSubmatch(sub.Resource); m != nil && m[3] == "pull" {
		return sub.Resource
	}
	return sub.Resource + "\x00" + sub.Branch
}

// fetch retrieves the current observation for a subscription's resource.
// Returns nil when the resource shape is unknown or the primary probe (state)
// failed — observed values simply stay stale until GitHub is reachable again.
func (p *Poller) fetch(sub *Subscription) *Observed {
	m := resourceRE.FindStringSubmatch(sub.Resource)
	if m == nil {
		p.Logger.Warn("unsupported resource shape; skipping", "session", sub.SessionName, "resource", sub.Resource)
		return nil
	}
	owner, repo, kind, number := m[1], m[2], m[3], m[4]

	if kind == "pull" {
		return p.fetchPR(sub, owner, repo, kind, number)
	}
	return p.fetchIssue(sub, owner, repo, kind, number)
}

// fetchPR gathers a PR's observation from independent probes: the primary
// REST read (state/head sha/mergeable/draft, one `gh api` call) is
// all-or-nothing like the old `gh pr view` failure path, but checks come from
// their own call and degrade independently via ChecksResolved (item 3).
func (p *Poller) fetchPR(sub *Subscription, owner, repo, kind, number string) *Observed {
	state, headSHA, mergeableState, draft, ok := p.fetchPRCore(owner, repo, number)
	if !ok {
		p.Logger.Warn("gh api pulls failed", "session", sub.SessionName, "resource", sub.Resource)
		return nil
	}
	obs := &Observed{State: state, Kind: kind, HeadSHA: headSHA, Draft: draft}
	if mergeableState != "" && mergeableState != "unknown" {
		obs.MergeableState, obs.MergeableResolved = mergeableState, true
	}
	if checks, ok := p.fetchChecks(owner, repo, headSHA); ok {
		obs.ChecksStatus, obs.ChecksResolved = checks, true
	} else {
		p.Logger.Warn("gh api checks failed", "session", sub.SessionName, "resource", sub.Resource)
	}
	return obs
}

// fetchIssue mirrors fetchPR for issue resources, additionally resolving the
// linked PR via sub.Branch.
func (p *Poller) fetchIssue(sub *Subscription, owner, repo, kind, number string) *Observed {
	state, ok := p.fetchIssueCore(owner, repo, number)
	if !ok {
		p.Logger.Warn("gh api issues failed", "session", sub.SessionName, "resource", sub.Resource)
		return nil
	}
	obs := &Observed{State: state, Kind: kind}

	if sub.Branch == "" {
		return obs
	}
	prNumber, found, resolved := p.fetchLinkedPRNumber(owner, repo, sub.Branch)
	if !resolved {
		// Discovery itself failed (transient error): leave every PR-scoped
		// field absent rather than guess — never retract a stale value.
		return obs
	}
	obs.LinkedPRResolved = true
	obs.LinkedPRFound = found
	if !found {
		// A successful lookup finding no PR is authoritative: every PR-scoped
		// field retracts to its empty/zero value — a since-unlinked PR must
		// not freeze its last-observed checks/mergeable/draft forever.
		obs.LinkedPRStateResolved = true
		obs.LinkedPRChecksResolved = true
		obs.LinkedPRMergeableResolved = true
		obs.LinkedPRDraftResolved = true
		return obs
	}
	// mergeable_state/draft aren't in the pulls?head= list response, so the
	// linked-PR path needs one more REST call to the single-PR endpoint
	// — accepted per the revised evidence.
	prState, prHeadSHA, mergeableState, draft, ok := p.fetchPRCore(owner, repo, prNumber)
	if !ok {
		return obs
	}
	obs.LinkedPRState, obs.LinkedPRHeadSHA, obs.LinkedPRStateResolved = prState, prHeadSHA, true
	obs.LinkedPRDraft, obs.LinkedPRDraftResolved = draft, true
	if mergeableState != "" && mergeableState != "unknown" {
		obs.LinkedPRMergeableState, obs.LinkedPRMergeableResolved = mergeableState, true
	}
	if checks, ok := p.fetchChecks(owner, repo, prHeadSHA); ok {
		obs.LinkedPRChecksStatus, obs.LinkedPRChecksResolved = checks, true
	}
	return obs
}

// fetchPRCore is the primary REST probe: state/head sha/mergeable_state/draft
// in one `gh api` call, replacing the GraphQL-backed `gh pr view`.
// REST only reports open/closed, so a merged PR is recovered from
// the `merged` flag to keep the same lowercase open/closed/merged vocabulary
// the rest of the package (and its consumers) already use.
func (p *Poller) fetchPRCore(owner, repo, number string) (state, headSHA, mergeableState string, draft bool, ok bool) {
	out, err := exec.Command(p.gh(), "api",
		fmt.Sprintf("repos/%s/%s/pulls/%s", owner, repo, number),
		"--jq", "{state:.state,merged:.merged,sha:.head.sha,mergeable_state:.mergeable_state,draft:.draft}").Output()
	if err != nil {
		return "", "", "", false, false
	}
	var r struct {
		State          string `json:"state"`
		Merged         bool   `json:"merged"`
		SHA            string `json:"sha"`
		MergeableState string `json:"mergeable_state"`
		Draft          bool   `json:"draft"`
	}
	if err := json.Unmarshal(bytes.TrimSpace(out), &r); err != nil {
		return "", "", "", false, false
	}
	state = strings.ToLower(r.State)
	if r.Merged {
		state = "merged"
	}
	return state, r.SHA, strings.ToLower(r.MergeableState), r.Draft, true
}

// fetchIssueCore is the REST equivalent of `gh issue view --json state`.
func (p *Poller) fetchIssueCore(owner, repo, number string) (state string, ok bool) {
	out, err := exec.Command(p.gh(), "api",
		fmt.Sprintf("repos/%s/%s/issues/%s", owner, repo, number),
		"--jq", "{state:.state}").Output()
	if err != nil {
		return "", false
	}
	var r struct {
		State string `json:"state"`
	}
	if err := json.Unmarshal(bytes.TrimSpace(out), &r); err != nil {
		return "", false
	}
	return strings.ToLower(r.State), true
}

// fetchLinkedPRNumber discovers an issue's linked PR NUMBER via the REST
// `pulls?head=` filter — the same call resources/github.toml's observe script
// uses as its branch-name fallback. REST has no equivalent of the GraphQL
// closedByPullRequestsReferences lookup that script tries first, so branch is
// the only signal available to the watcher. An empty jq
// result (no matching PR) is a successful, authoritative "no PR" answer
// (found=false, resolved=true) — distinct from a failed call (resolved=false).
// The rest of the PR's fields come from a follow-up fetchPRCore call, since
// this list endpoint doesn't carry mergeable_state/draft.
func (p *Poller) fetchLinkedPRNumber(owner, repo, branch string) (number string, found, resolved bool) {
	// --method GET is required: gh api silently switches to POST (against the
	// PR-creation endpoint!) whenever -f/--raw-field parameters are present,
	// unless the method is pinned explicitly (same pattern resources/
	// github.toml's own pulls?head= discovery call uses).
	out, err := exec.Command(p.gh(), "api",
		fmt.Sprintf("repos/%s/%s/pulls", owner, repo), "--method", "GET",
		"-f", "head="+owner+":"+branch, "-f", "state=all",
		"--jq", "if length==0 then empty else .[0].number end").Output()
	if err != nil {
		return "", false, false
	}
	trimmed := strings.TrimSpace(string(out))
	if trimmed == "" {
		return "", false, true
	}
	n, err := strconv.Atoi(trimmed)
	if err != nil {
		return "", false, false
	}
	return strconv.Itoa(n), true, true
}

// statusCheck is one classified check entry, fed by either REST check-runs
// (conclusion) or the REST combined status API (state).
type statusCheck struct {
	Conclusion string `json:"conclusion"`
	State      string `json:"state"`
}

// fetchChecks aggregates REST check-runs + the combined status API into the
// SUCCESS/PENDING/FAILURE vocabulary, replacing the GraphQL statusCheckRollup
// field. Either call failing leaves checks unresolved
// rather than guessing — a partial read must not overwrite a good baseline.
func (p *Poller) fetchChecks(owner, repo, sha string) (string, bool) {
	runsOut, err := exec.Command(p.gh(), "api",
		fmt.Sprintf("repos/%s/%s/commits/%s/check-runs", owner, repo, sha),
		"--paginate", "--jq", ".check_runs[]? | {conclusion:.conclusion}").Output()
	if err != nil {
		return "", false
	}
	statusOut, err := exec.Command(p.gh(), "api",
		fmt.Sprintf("repos/%s/%s/commits/%s/status", owner, repo, sha),
		"--jq", ".statuses[]? | {state:.state}").Output()
	if err != nil {
		return "", false
	}
	checks := parseCheckLines(runsOut)
	checks = append(checks, parseCheckLines(statusOut)...)
	return checksRollup(checks), true
}

func parseCheckLines(out []byte) []statusCheck {
	var checks []statusCheck
	scanner := bufio.NewScanner(bytes.NewReader(out))
	for scanner.Scan() {
		line := strings.TrimSpace(scanner.Text())
		if line == "" {
			continue
		}
		var c statusCheck
		if err := json.Unmarshal([]byte(line), &c); err == nil {
			checks = append(checks, c)
		}
	}
	return checks
}

// checksRollup mirrors resources/github.toml's checks_rollup() so the watcher
// and the dynamic-output observe agree on meaning: zero
// entries is a real PR whose checks haven't started, i.e. PENDING — not the
// watcher's old empty-string sentinel for "no CI at all".
func checksRollup(checks []statusCheck) string {
	if len(checks) == 0 {
		return "PENDING"
	}
	failed, pending := false, false
	for _, c := range checks {
		switch classifyCheck(c) {
		case "FAILURE":
			failed = true
		case "PENDING":
			pending = true
		}
	}
	switch {
	case failed:
		return "FAILURE"
	case pending:
		return "PENDING"
	default:
		return "SUCCESS"
	}
}

// classifyCheck buckets one entry using the same conclusion/state sets as
// resources/github.toml's checks_rollup(). Anything not explicitly FAILURE or
// SUCCESS defaults to PENDING (in-progress check runs, queued statuses, and
// unrecognized values all read as "not done yet").
var (
	failureConclusions = []string{"FAILURE", "TIMED_OUT", "CANCELLED", "ACTION_REQUIRED", "STARTUP_FAILURE", "STALE"}
	failureStates      = []string{"FAILURE", "ERROR"}
	successConclusions = []string{"SUCCESS", "SKIPPED", "NEUTRAL"}
)

func classifyCheck(c statusCheck) string {
	conclusion := strings.ToUpper(c.Conclusion)
	state := strings.ToUpper(c.State)
	switch {
	case slices.Contains(failureConclusions, conclusion) || slices.Contains(failureStates, state):
		return "FAILURE"
	case slices.Contains(successConclusions, conclusion) || state == "SUCCESS":
		return "SUCCESS"
	default:
		return "PENDING"
	}
}

// apply diffs the observation against the subscription's baseline, notifies the
// delivery path, and persists the new baseline.
func (p *Poller) apply(sub *Subscription, obs *Observed) {
	current := outputsFor(obs)
	changes := diff(sub.Last, current)
	if len(changes) == 0 {
		return
	}

	for _, c := range summarizeChanges(sub.Last, current) {
		p.notify(sub, c)
	}

	// Merge current over the old baseline rather than replacing it, mirroring
	// diff: a key absent from current (e.g. pr_state when a linked-PR lookup
	// failed) is never retracted, while a present-but-empty key (a retracted
	// check) does overwrite. A wholesale replace would drop the omitted keys.
	merged := mergeBaseline(sub.Last, current)
	if err := p.Store.SetLast(sub.SessionName, sub.Resource, merged); err != nil {
		p.Logger.Warn("persist baseline", "session", sub.SessionName, "error", err)
	}
	p.Logger.Info("observed github change", "session", sub.SessionName, "keys", sortedKeys(changes))
}

// outputsFor flattens an observation into the value set used for change
// detection and notification summaries.
//
// pr_state/issue_state/head_sha/draft come from the primary all-or-nothing
// probe (fetchPRCore/fetchIssueCore), so they're emitted whenever obs is
// non-nil. checks_status/mergeable_state are gated by their own Resolved
// flag: a probe that failed (or, for mergeable_state, reported "unknown")
// must neither retract a good baseline value nor seed a wrong one.
func outputsFor(obs *Observed) map[string]string {
	out := map[string]string{}
	if obs.Kind == "pull" {
		if obs.State != "" {
			out["pr_state"] = obs.State
		}
		if obs.ChecksResolved {
			out["checks_status"] = obs.ChecksStatus
		}
		if obs.MergeableResolved {
			out["mergeable_state"] = obs.MergeableState
		}
		out["head_sha"] = obs.HeadSHA
		out["draft"] = strconv.FormatBool(obs.Draft)
		return out
	}
	if obs.State != "" {
		out["issue_state"] = obs.State
	}
	if obs.LinkedPRResolved {
		if obs.LinkedPRStateResolved {
			out["pr_state"] = obs.LinkedPRState
			out["head_sha"] = obs.LinkedPRHeadSHA
		}
		if obs.LinkedPRChecksResolved {
			out["checks_status"] = obs.LinkedPRChecksStatus
		}
		if obs.LinkedPRMergeableResolved {
			out["mergeable_state"] = obs.LinkedPRMergeableState
		}
		if obs.LinkedPRDraftResolved {
			out["draft"] = strconv.FormatBool(obs.LinkedPRDraft)
		}
	}
	return out
}

// mergeBaseline overlays new onto old (new wins per key), preserving keys old
// has but new omits. Keeps the persisted baseline aligned with diff, which
// likewise never retracts a key absent from new.
func mergeBaseline(old, new map[string]string) map[string]string {
	merged := make(map[string]string, len(old)+len(new))
	maps.Copy(merged, old)
	maps.Copy(merged, new)
	return merged
}

// diff returns the keys whose values changed from old to new. Keys absent
// from new are not reported (observed values never get retracted).
func diff(old, new map[string]string) map[string]bool {
	changed := map[string]bool{}
	for k, v := range new {
		if old[k] != v {
			changed[k] = true
		}
	}
	return changed
}

// change is one human-meaningful transition for notification delivery.
type change struct {
	Type    string
	Summary string
}

// summarizeChanges converts a diff into the change vocabulary the delivery
// path already understands (state / ci_status / mergeable / new_commits /
// draft). Initial observations (no baseline) produce no notifications — only
// transitions do.
func summarizeChanges(old, current map[string]string) []change {
	if len(old) == 0 {
		return nil
	}
	var out []change
	if v := current["pr_state"]; v != "" && old["pr_state"] != v && old["pr_state"] != "" {
		out = append(out, change{Type: "state", Summary: fmt.Sprintf("PR %s → %s", old["pr_state"], v)})
	}
	if v := current["issue_state"]; v != "" && old["issue_state"] != v && old["issue_state"] != "" {
		out = append(out, change{Type: "state", Summary: fmt.Sprintf("Issue %s → %s", old["issue_state"], v)})
	}
	// Retractions (new value empty) update state silently — there is no
	// meaningful "CI: <nothing>" message to deliver.
	if v := current["checks_status"]; v != "" && old["checks_status"] != v {
		out = append(out, change{Type: "ci_status", Summary: fmt.Sprintf("CI: %s", v)})
	}
	// mergeable_state is never "unknown" here (outputsFor only ever emits a
	// resolved, non-"unknown" value or omits the key) — so every observed
	// transition is real, not GitHub-still-computing noise.
	if v := current["mergeable_state"]; v != "" && old["mergeable_state"] != v {
		out = append(out, change{Type: "mergeable", Summary: fmt.Sprintf("Mergeable: %s", v)})
	}
	if v := current["head_sha"]; v != "" && old["head_sha"] != "" && v != old["head_sha"] {
		out = append(out, change{Type: "new_commits", Summary: fmt.Sprintf("🔧 new commit %s", shortSHA(v))})
	}
	// draft only ever notifies the "left draft" direction (draft → ready for
	// review); a value flipping the other way just advances the baseline
	// silently — reverting to draft mid-review isn't itself actionable.
	if v := current["draft"]; old["draft"] == "true" && v == "false" {
		out = append(out, change{Type: "draft", Summary: "PR ready for review"})
	}
	return out
}

func shortSHA(sha string) string {
	if len(sha) > 7 {
		return sha[:7]
	}
	return sha
}

// notify delivers one change. With Bus set it publishes a github.* event to the
// tws event bus (P4); otherwise it POSTs to slack-adapter's /notify (legacy).
// Either way the resulting Slack-thread + channel-server delivery is the same;
// the bus path just removes the direct adapter coupling (and the hook chain).
func (p *Poller) notify(sub *Subscription, c change) {
	if p.Bus != nil {
		p.publish(sub, c)
		return
	}
	if p.NotifyURL == "" {
		return
	}
	body, _ := json.Marshal(map[string]any{
		"session_name":          sub.SessionName,
		"change_type":           c.Type,
		"summary":               c.Summary,
		"url":                   sub.Resource,
		"notify_channel_server": true,
		"notify_slack":          true,
	})
	resp, err := p.httpClient().Post(p.NotifyURL, "application/json", bytes.NewReader(body))
	if err != nil {
		p.Logger.Warn("notify failed", "session", sub.SessionName, "error", err)
		return
	}
	defer resp.Body.Close()
	if resp.StatusCode >= 300 {
		p.Logger.Warn("notify rejected", "session", sub.SessionName, "status", resp.StatusCode)
	}
}

// publish appends a github.<change_type> event to the session's bus log. The
// type suffix carries the change kind (the slack-adapter subscriber recovers it
// and applies the same emoji/"[GitHub …]" framing the old /notify did); the
// resource URL rides in metadata so a session funneling several PRs identifies
// which one changed. Direction is inbound — the origin
// (GitHub) is outside this session, the same "external origin" rule
// service.EventPublish applies via TWS_SESSION_NAME — so a quiet standing
// goal's tick backoff resets when a subscribed resource changes. A
// publish failure is logged, not fatal: the baseline still advances, matching
// the at-most-once /notify behavior (the durable log, not a retry, is the
// safety net).
//
// The payload is intentionally minimal — type, summary, metadata.url,
// metadata.resource — and carries no other structured facts: the event is
// a trigger, not the source of truth. Consumers re-read
// current values from resource observe / dynamic outputs, never from this
// payload.
func (p *Poller) publish(sub *Subscription, c change) {
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	_, _, err := p.Bus.Publish(ctx, event.Event{
		SessionName: sub.SessionName,
		Type:        event.TypeGitHubPrefix + c.Type,
		Source:      event.SourceGitHub,
		Direction:   event.Inbound,
		Summary:     c.Summary,
		Metadata:    map[string]string{"url": sub.Resource, "resource": sub.Resource},
	})
	if err != nil {
		p.Logger.Warn("bus publish failed", "session", sub.SessionName, "type", c.Type, "error", err)
	}
}

func sortedKeys(m map[string]bool) []string {
	keys := make([]string, 0, len(m))
	for k := range m {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	return keys
}

// ParseInterval parses the --interval flag with a floor that protects the
// GitHub rate limit from accidental hot loops.
func ParseInterval(s string) (time.Duration, error) {
	if s == "" {
		return 60 * time.Second, nil
	}
	d, err := time.ParseDuration(s)
	if err != nil {
		// Allow bare seconds ("60").
		if n, nerr := strconv.Atoi(s); nerr == nil {
			d = time.Duration(n) * time.Second
		} else {
			return 0, err
		}
	}
	if d < 10*time.Second {
		return 0, fmt.Errorf("interval %s too small (min 10s)", d)
	}
	return d, nil
}
