package watcher

import (
	"io"
	"log/slog"
	"os"
	"path/filepath"
	"testing"
)

func TestProtocolStoreMembershipOverlapAndStateSurviveRestart(t *testing.T) {
	dataDir, cacheDir := t.TempDir(), t.TempDir()
	store := NewProtocolStore(dataDir, cacheDir)
	row := MembershipRow{Session: "s1", Source: "runtime", Resource: MembershipResource{ID: "r1", Type: "github.issue", Locator: "https://github.com/org/repo/issues/1"}}
	task := row
	task.Source = "task"
	if err := store.Replace([]MembershipRow{row, task}); err != nil {
		t.Fatal(err)
	}
	all, err := store.All()
	if err != nil || len(all) != 1 {
		t.Fatalf("membership = %v, %v", all, err)
	}
	if err := store.SetLast("s1", row.Resource.Locator, map[string]string{"issue_state": "open"}); err != nil {
		t.Fatal(err)
	}
	store.Apply(nil, []MembershipRow{row})
	all, err = store.All()
	if err != nil || len(all) != 1 {
		t.Fatalf("overlapping membership = %v, %v", all, err)
	}
	restarted := NewProtocolStore(dataDir, cacheDir)
	if err := restarted.Replace([]MembershipRow{task}); err != nil {
		t.Fatal(err)
	}
	all, err = restarted.All()
	if err != nil || all[subKey("s1", row.Resource.Locator)].Last["issue_state"] != "open" {
		t.Fatalf("persisted state = %v, %v", all, err)
	}
	restarted.Apply(nil, []MembershipRow{task})
	all, err = restarted.All()
	if err != nil || len(all) != 0 {
		t.Fatalf("removed membership = %v, %v", all, err)
	}
	if _, err := os.Stat(filepath.Join(dataDir, "watch-state.json")); err != nil {
		t.Fatal(err)
	}
}

func TestProtocolStoreUsesBranchFromOverlappingMembership(t *testing.T) {
	store := NewProtocolStore(t.TempDir(), t.TempDir())
	row := MembershipRow{Session: "s1", Source: "runtime", InputsJSON: `{}`, Resource: MembershipResource{ID: "r1", Locator: "https://github.com/org/repo/issues/1"}}
	withBranch := row
	withBranch.Source = "dispatch"
	withBranch.InputsJSON = `{"branch":"issue/1"}`
	if err := store.Replace([]MembershipRow{row, withBranch}); err != nil {
		t.Fatal(err)
	}
	for range 50 {
		all, err := store.All()
		if err != nil || all[subKey("s1", row.Resource.Locator)].Branch != "issue/1" {
			t.Fatalf("branch = %v, %v", all, err)
		}
	}
}

func TestProtocolStoreKeepsETagsInDisposableCache(t *testing.T) {
	dataDir, cacheDir := t.TempDir(), t.TempDir()
	store := NewProtocolStore(dataDir, cacheDir)
	if err := store.SetCache("route", "etag", "body"); err != nil {
		t.Fatal(err)
	}
	if got, body, ok := NewProtocolStore(dataDir, cacheDir).GetCache("route"); !ok || got != "etag" || body != "body" {
		t.Fatalf("cache = %q %q %v", got, body, ok)
	}
	if _, err := os.Stat(filepath.Join(cacheDir, "etag.json")); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(filepath.Join(dataDir, "etag.json")); !os.IsNotExist(err) {
		t.Fatalf("cache in data dir: %v", err)
	}
}

func TestProtocolStoreIgnoresSubscriptionsOutsideGitHub(t *testing.T) {
	store := NewProtocolStore(t.TempDir(), t.TempDir())
	github := MembershipRow{Session: "s1", Source: "runtime", Resource: MembershipResource{ID: "r1", Locator: "https://github.com/org/repo/issues/1"}}
	other := MembershipRow{Session: "s1", Source: "runtime", Resource: MembershipResource{ID: "r2", Locator: "https://example.com/issues/2"}}
	if err := store.Replace([]MembershipRow{github, other}); err != nil {
		t.Fatal(err)
	}
	all, err := store.All()
	if err != nil || len(all) != 1 || all[subKey("s1", github.Resource.Locator)] == nil {
		t.Fatalf("GitHub subscriptions = %v, %v", all, err)
	}
}

func TestPollerPublishesResourceWithoutSessionTransport(t *testing.T) {
	var resource, kind string
	p := &Poller{Logger: slog.New(slog.NewTextHandler(io.Discard, nil)), PublishResource: func(_, r, k, summary string, metadata map[string]string) error { resource, kind = r, k; return nil }}
	p.notify(&Subscription{SessionName: "s1", Resource: "https://github.com/org/repo/issues/1"}, change{Type: "new_commits", Summary: "push"})
	if resource != "https://github.com/org/repo/issues/1" || kind != "github.new_commits" {
		t.Fatalf("published %q %q", resource, kind)
	}
}

func TestProtocolStoreKeepsCoreMembershipAfterResourceCloses(t *testing.T) {
	store := NewProtocolStore(t.TempDir(), t.TempDir())
	row := MembershipRow{Session: "s1", Source: "runtime", Resource: MembershipResource{ID: "r1", Locator: "https://github.com/org/repo/issues/1"}}
	if err := store.Replace([]MembershipRow{row}); err != nil {
		t.Fatal(err)
	}
	p := &Poller{Store: store, Logger: slog.New(slog.NewTextHandler(io.Discard, nil))}
	p.apply(&Subscription{SessionName: row.Session, Resource: row.Resource.Locator}, &Observed{State: "closed", Kind: "issues"})
	all, err := store.All()
	if err != nil || len(all) != 1 {
		t.Fatalf("core membership after terminal observation = %v, %v", all, err)
	}
}
