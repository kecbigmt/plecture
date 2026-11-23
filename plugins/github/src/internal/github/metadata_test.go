package github

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/kecbigmt/plecture/plugins/github/src/internal/ghapi"
)

type fakeRunner struct {
	stdout []byte
	stderr []byte
	err    error
}

func (f *fakeRunner) Run(ctx context.Context, dir string, mirror bool, name string, args ...string) ([]byte, []byte, error) {
	return f.stdout, f.stderr, f.err
}

func TestFetchPullMeta_ParsesHeadTitleAndState(t *testing.T) {
	client := ghapi.Direct()
	client.Runner = &fakeRunner{stdout: []byte(`{"head":{"ref":"feat/login"},"title":"Add login","state":"OPEN"}`)}

	meta, err := FetchPullMeta(context.Background(), client, "acme/widgets", 44)
	if err != nil {
		t.Fatalf("FetchPullMeta: %v", err)
	}
	if meta.HeadRef != "feat/login" || meta.Title != "Add login" || meta.State != "open" {
		t.Errorf("meta = %+v", meta)
	}
}

func TestFetchPullMeta_FetchFailurePropagates(t *testing.T) {
	client := ghapi.Direct()
	client.Runner = &fakeRunner{stderr: []byte("HTTP 404"), err: context.DeadlineExceeded}

	_, err := FetchPullMeta(context.Background(), client, "acme/widgets", 44)
	if err == nil {
		t.Fatal("expected a fetch failure to propagate for a pull request")
	}
}

func TestFetchIssueMeta_ParsesTitleAndState(t *testing.T) {
	client := ghapi.Direct()
	client.Runner = &fakeRunner{stdout: []byte(`{"title":"Fix crash","state":"CLOSED"}`)}

	meta, err := FetchIssueMeta(context.Background(), client, "acme/widgets", 7)
	if err != nil {
		t.Fatalf("FetchIssueMeta: %v", err)
	}
	if meta.Title != "Fix crash" || meta.State != "closed" {
		t.Errorf("meta = %+v", meta)
	}
}

// TestFetchIssueMeta_FetchFailureIsTolerated: branch work can start before
// the issue exists, so a missing issue degrades to empty metadata instead of
// failing setup.
func TestFetchIssueMeta_FetchFailureIsTolerated(t *testing.T) {
	client := ghapi.Direct()
	client.Runner = &fakeRunner{stderr: []byte("HTTP 404"), err: context.DeadlineExceeded}

	meta, err := FetchIssueMeta(context.Background(), client, "acme/widgets", 999)
	if err != nil {
		t.Fatalf("FetchIssueMeta: %v, want the fetch failure tolerated", err)
	}
	if meta.Title != "" || meta.State != "" {
		t.Errorf("meta = %+v, want zero value on fetch failure", meta)
	}
}

func TestFetchIssueMeta_MalformedJSONIsTolerated(t *testing.T) {
	client := ghapi.Direct()
	client.Runner = &fakeRunner{stdout: []byte("not json")}

	meta, err := FetchIssueMeta(context.Background(), client, "acme/widgets", 7)
	if err != nil {
		t.Fatalf("FetchIssueMeta: %v, want the unparsable response tolerated", err)
	}
	if meta.Title != "" || meta.State != "" {
		t.Errorf("meta = %+v, want zero value on unparsable response", meta)
	}
}

// A failed token command is a broken credential source, not a missing issue,
// so tolerating it would let setup go on as if authentication worked.
func TestFetchIssueMeta_TokenCommandFailureIsNotTolerated(t *testing.T) {
	path := filepath.Join(t.TempDir(), "installation-token")
	if err := os.WriteFile(path, []byte("#!/bin/sh\nexit 3\n"), 0o700); err != nil {
		t.Fatal(err)
	}
	client := &ghapi.App{TokenCommand: []string{path}, BaseURL: "http://127.0.0.1:0"}

	_, err := FetchIssueMeta(context.Background(), client, "acme/widgets", 7)
	if err == nil || !strings.Contains(err.Error(), path) || !strings.Contains(err.Error(), "exit status 3") {
		t.Fatalf("error = %v, want the token command failure", err)
	}
}

func TestFetchPullMeta_UsesRESTPath(t *testing.T) {
	// Regression guard: production reads via `gh api` REST, never the `gh
	// pr view` porcelain — porcelain reads consume GraphQL quota that
	// write-side `gh` calls also share (see config/tasks/review.md).
	runner := &recordingRunner{stdout: []byte(`{"head":{"ref":"main"},"title":"t","state":"open"}`)}
	client := &ghapi.Client{Program: "gh", Args: []string{"api"}, Runner: runner}

	if _, err := FetchPullMeta(context.Background(), client, "acme/widgets", 1); err != nil {
		t.Fatalf("FetchPullMeta: %v", err)
	}
	if !strings.Contains(strings.Join(runner.args, " "), "repos/acme/widgets/pulls/1") {
		t.Errorf("args = %v, want a REST pulls path", runner.args)
	}
}

type recordingRunner struct {
	args   []string
	stdout []byte
}

func (r *recordingRunner) Run(ctx context.Context, dir string, mirror bool, name string, args ...string) ([]byte, []byte, error) {
	r.args = args
	return r.stdout, nil, nil
}
