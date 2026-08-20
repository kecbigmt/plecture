package main

import (
	"testing"

	"github.com/kecbigmt/plecture/plugins/github/src/internal/observe"
)

func TestObserveDocumentOmitsPRURLWhenNoPullRequestExists(t *testing.T) {
	doc := observeDocument(observe.Result{
		ResourceKind:   "issue",
		ChecksStatus:   "NULL",
		IssueStatus:    "PENDING",
		Revision:       "issue:2026-08-18T00:53:22Z",
		MergeableState: "NULL",
		ReviewDecision: "NULL",
	})

	if _, ok := doc["pr_url"]; ok {
		t.Fatalf("pr_url present with no pull request observed: %#v", doc)
	}
	for _, key := range []string{"resource_kind", "checks_status", "issue_status", "revision", "mergeable_state", "review_decision"} {
		if _, ok := doc[key]; !ok {
			t.Errorf("%s missing: %#v", key, doc)
		}
	}
}

func TestObserveDocumentCarriesPRURLWhenOneExists(t *testing.T) {
	doc := observeDocument(observe.Result{
		ResourceKind: "pull",
		PRURL:        "https://github.com/o/r/pull/1",
	})

	if doc["pr_url"] != "https://github.com/o/r/pull/1" {
		t.Fatalf("pr_url = %#v", doc["pr_url"])
	}
}
