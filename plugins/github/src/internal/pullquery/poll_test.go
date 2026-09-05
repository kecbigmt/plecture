package pullquery

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"testing"
)

// fakeGHClient mirrors observe's own test double: fixture responses keyed
// by an args prefix, so a test can pin exactly the REST calls it expects
// without a real GitHub API.
type fakeGHClient struct {
	responses map[string]string
	errs      map[string]error
}

func (f *fakeGHClient) JSON(ctx context.Context, args ...string) ([]byte, error) {
	key := strings.Join(args, " ")
	for prefix, err := range f.errs {
		if strings.HasPrefix(key, prefix) {
			return nil, err
		}
	}
	for prefix, body := range f.responses {
		if strings.HasPrefix(key, prefix) {
			return []byte(body), nil
		}
	}
	return nil, fmt.Errorf("fakeGHClient: no response configured for %q", key)
}

func pullJSON(number int, state string, draft bool, labels ...string) string {
	labelObjs := make([]string, len(labels))
	for i, l := range labels {
		labelObjs[i] = fmt.Sprintf(`{"name":%q}`, l)
	}
	return fmt.Sprintf(`{"html_url":"https://github.com/acme/widgets/pull/%d","state":%q,"draft":%v,"labels":[%s]}`,
		number, state, draft, strings.Join(labelObjs, ","))
}

func TestPoll_OneCompletePageEmitsMatchingItems(t *testing.T) {
	client := &fakeGHClient{responses: map[string]string{
		"repos/acme/widgets/pulls?state=open&per_page=100&page=1": "[" + pullJSON(1, "open", false, "agent-review") + "," + pullJSON(2, "open", true, "agent-review") + "]",
		"repos/acme/widgets/pulls?state=open&per_page=100&page=2": "[]",
	}}
	items, err := Poll(context.Background(), client, Inputs{Repositories: []string{"acme/widgets"}, Labels: []string{"agent-review"}, State: "open", Draft: false})
	if err != nil {
		t.Fatalf("Poll: %v", err)
	}
	if len(items) != 1 || items[0].Resource != "https://github.com/acme/widgets/pull/1" {
		t.Fatalf("items = %+v, want exactly pull #1 (the non-draft match)", items)
	}
	if items[0].Owner != "acme" || items[0].Repository != "widgets" {
		t.Errorf("item = %+v, want owner/repository decomposition set", items[0])
	}
}

// TestPoll_SpansPagination pins the pagination contract the ADR requires:
// a full first page means there may be more, and enumeration is not
// complete until a page comes back short.
func TestPoll_SpansPagination(t *testing.T) {
	fullPage := make([]string, pollPageSize)
	for i := range fullPage {
		fullPage[i] = pullJSON(i+1, "open", false)
	}
	client := &fakeGHClient{responses: map[string]string{
		"repos/acme/widgets/pulls?state=open&per_page=100&page=1": "[" + strings.Join(fullPage, ",") + "]",
		"repos/acme/widgets/pulls?state=open&per_page=100&page=2": "[" + pullJSON(pollPageSize+1, "open", false) + "]",
		"repos/acme/widgets/pulls?state=open&per_page=100&page=3": "[]",
	}}
	items, err := Poll(context.Background(), client, Inputs{Repositories: []string{"acme/widgets"}, State: "open"})
	if err != nil {
		t.Fatalf("Poll: %v", err)
	}
	if len(items) != pollPageSize+1 {
		t.Fatalf("len(items) = %d, want %d spanning two pages", len(items), pollPageSize+1)
	}
}

func TestPoll_SpansMultipleRepositories(t *testing.T) {
	client := &fakeGHClient{responses: map[string]string{
		"repos/acme/widgets/pulls?state=open&per_page=100&page=1": "[" + pullJSON(1, "open", false) + "]",
		"repos/acme/widgets/pulls?state=open&per_page=100&page=2": "[]",
		"repos/acme/gadgets/pulls?state=open&per_page=100&page=1": "[" + pullJSON(9, "open", false) + "]",
		"repos/acme/gadgets/pulls?state=open&per_page=100&page=2": "[]",
	}}
	items, err := Poll(context.Background(), client, Inputs{Repositories: []string{"acme/widgets", "acme/gadgets"}, State: "open"})
	if err != nil {
		t.Fatalf("Poll: %v", err)
	}
	if len(items) != 2 {
		t.Fatalf("len(items) = %d, want 2 (one per repository)", len(items))
	}
}

// TestPoll_MidPaginationFailureFailsTheWholeSnapshot pins the ADR's poll
// contract: a partial result must never reach the caller, so a failure on
// a later page invalidates everything already fetched rather than
// returning what was gathered so far.
func TestPoll_MidPaginationFailureFailsTheWholeSnapshot(t *testing.T) {
	fullPage := make([]string, pollPageSize)
	for i := range fullPage {
		fullPage[i] = pullJSON(i+1, "open", false)
	}
	client := &fakeGHClient{
		responses: map[string]string{
			"repos/acme/widgets/pulls?state=open&per_page=100&page=1": "[" + strings.Join(fullPage, ",") + "]",
		},
		errs: map[string]error{
			"repos/acme/widgets/pulls?state=open&per_page=100&page=2": errors.New("secondary rate limit"),
		},
	}
	_, err := Poll(context.Background(), client, Inputs{Repositories: []string{"acme/widgets"}, State: "open"})
	if err == nil {
		t.Fatal("want an error when a later page fails, not a partial snapshot")
	}
}

func TestPoll_RequiresAtLeastOneRepository(t *testing.T) {
	_, err := Poll(context.Background(), &fakeGHClient{}, Inputs{State: "open"})
	if err == nil {
		t.Fatal("want an error for an empty repositories list")
	}
}

func TestPoll_RejectsInvalidState(t *testing.T) {
	_, err := Poll(context.Background(), &fakeGHClient{}, Inputs{Repositories: []string{"acme/widgets"}, State: "merged"})
	if err == nil {
		t.Fatal("want an error for a state value GitHub's REST API does not accept")
	}
}

func TestPoll_RejectsMalformedRepository(t *testing.T) {
	_, err := Poll(context.Background(), &fakeGHClient{}, Inputs{Repositories: []string{"not-a-repo"}, State: "open"})
	if err == nil {
		t.Fatal("want an error for a repository string with no owner/repo separator")
	}
}
