package worktree

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"github.com/kecbigmt/plecture/plugins/github/src/internal/ghapi"
	"github.com/kecbigmt/plecture/plugins/github/src/internal/github"
	"github.com/kecbigmt/plecture/plugins/github/src/internal/workspace"
)

type fakeGHClient struct {
	responses map[string]string // args (space-joined) -> JSON body
	err       error
}

func (f *fakeGHClient) JSON(ctx context.Context, args ...string) ([]byte, error) {
	if f.err != nil {
		return nil, f.err
	}
	if body, ok := f.responses[strings.Join(args, " ")]; ok {
		return []byte(body), nil
	}
	return nil, errors.New("fakeGHClient: no response configured for " + strings.Join(args, " "))
}

type fakeManager struct {
	adds      []workspace.AddParams
	removes   []removeCall
	addInfo   *workspace.WorkspaceInfo
	addErr    error
	findErr   error
	removeErr error
}

type removeCall struct {
	workspaceDir string
	gitDir       string
	branch       string
	force        bool
	deleteBranch bool
}

func (m *fakeManager) Add(ctx context.Context, params workspace.AddParams) (*workspace.WorkspaceInfo, error) {
	m.adds = append(m.adds, params)
	if m.addErr != nil {
		return nil, m.addErr
	}
	if m.addInfo != nil {
		return m.addInfo, nil
	}
	return &workspace.WorkspaceInfo{WorktreePath: "/roots/wt/issue-1"}, nil
}

func (m *fakeManager) FindGitDir(string, ...string) (string, error) {
	if m.findErr != nil {
		return "", m.findErr
	}
	return "/roots/src/acme/widgets", nil
}

func (m *fakeManager) RemoveByPath(ctx context.Context, workspaceDir, gitDir, branch string, force, deleteBranch bool) error {
	m.removes = append(m.removes, removeCall{workspaceDir: workspaceDir, gitDir: gitDir, branch: branch, force: force, deleteBranch: deleteBranch})
	return m.removeErr
}

func TestSelectGHClient_AppAuthWinsOverInjectedClient(t *testing.T) {
	app := &ghapi.App{AppID: "123456"}
	injected := &fakeGHClient{}
	if got := selectGHClient(app, injected); got != github.GHClient(app) {
		t.Errorf("selectGHClient = %v, want the app auth client", got)
	}
}

func TestSelectGHClient_FallsBackToInjectedClientWhenAppAuthIsNotOptedIn(t *testing.T) {
	injected := &fakeGHClient{}
	if got := selectGHClient(nil, injected); got != github.GHClient(injected) {
		t.Errorf("selectGHClient = %v, want the injected client", got)
	}
}

func TestSelectGHClient_DefaultsToDirectWhenNeitherIsSet(t *testing.T) {
	got := selectGHClient(nil, nil)
	if _, ok := got.(*ghapi.Client); !ok {
		t.Errorf("selectGHClient = %T, want *ghapi.Client (ghapi.Direct())", got)
	}
}

func TestSessionTag(t *testing.T) {
	tests := []struct {
		name    string
		session string
		want    string
	}{
		{"untagged", "acme/widgets-42", ""},
		{"tagged", "acme/widgets-42+review", "review"},
		{"last separator wins", "acme/widgets-42+a+b", "b"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := SessionTag(tt.session); got != tt.want {
				t.Errorf("SessionTag(%q) = %q, want %q", tt.session, got, tt.want)
			}
		})
	}
}

func TestSetup_IssueAcquiresTaggedWorktree(t *testing.T) {
	mgr := &fakeManager{addInfo: &workspace.WorkspaceInfo{WorktreePath: "/roots/worktrees/github.com/acme/widgets/issue-42-review"}}
	outputs, err := Setup(context.Background(), SetupOptions{
		ResourceID:  "https://github.com/acme/widgets/issues/42",
		SessionName: "acme/widgets-42+review",
		Manager:     mgr,
		GHClient:    &fakeGHClient{err: errors.New("no gh-api client configured for this test")},
	})
	if err != nil {
		t.Fatalf("Setup: %v", err)
	}
	if len(mgr.adds) != 1 {
		t.Fatalf("issued %d manager adds, want 1", len(mgr.adds))
	}
	add := mgr.adds[0]
	if add.Repo != "github.com/acme/widgets" || add.Branch != "issue/42+review" || add.BaseBranch != "issue/42" || add.SessionName != "acme/widgets-42+review" {
		t.Errorf("add params = %+v", add)
	}
	if add.FallbackRefspec != "" {
		t.Error("an issue acquisition must not pass a fallback refspec")
	}

	want := map[string]any{
		"workspace_dir": "/roots/worktrees/github.com/acme/widgets/issue-42-review",
		"branch":        "issue/42+review",
		"url":           "https://github.com/acme/widgets/issues/42",
		"owner_repo":    "acme/widgets",
		"owner":         "acme",
		"repo":          "widgets",
		"number":        42,
	}
	if !reflect.DeepEqual(outputs, want) {
		t.Errorf("outputs = %v, want %v", outputs, want)
	}
}

func TestSetup_UntaggedSessionKeepsBaseBranch(t *testing.T) {
	mgr := &fakeManager{addInfo: &workspace.WorkspaceInfo{WorktreePath: "/roots/wt/issue-7"}}
	outputs, err := Setup(context.Background(), SetupOptions{
		ResourceID:  "https://github.com/acme/widgets/issues/7",
		SessionName: "acme/widgets-7",
		Manager:     mgr,
		GHClient:    &fakeGHClient{err: errors.New("no gh-api client configured for this test")},
	})
	if err != nil {
		t.Fatalf("Setup: %v", err)
	}
	if outputs["branch"] != "issue/7" {
		t.Errorf("branch = %v, want issue/7", outputs["branch"])
	}
	if got := mgr.adds[0].Branch; got != "issue/7" {
		t.Errorf("branch = %q, want issue/7", got)
	}
}

func TestSetup_IssueTitleAndStateFromGHClient(t *testing.T) {
	mgr := &fakeManager{addInfo: &workspace.WorkspaceInfo{WorktreePath: "/roots/wt/issue-7"}}
	client := &fakeGHClient{responses: map[string]string{
		"repos/acme/widgets/issues/7": `{"title":"Fix crash","state":"CLOSED"}`,
	}}
	outputs, err := Setup(context.Background(), SetupOptions{
		ResourceID:  "https://github.com/acme/widgets/issues/7",
		SessionName: "acme/widgets-7",
		Manager:     mgr,
		GHClient:    client,
	})
	if err != nil {
		t.Fatalf("Setup: %v", err)
	}
	if outputs["title"] != "Fix crash" {
		t.Errorf("title = %v, want %q", outputs["title"], "Fix crash")
	}
	if outputs["issue_state"] != "closed" {
		t.Errorf("issue_state = %v, want %q", outputs["issue_state"], "closed")
	}
	if _, ok := outputs["pr_state"]; ok {
		t.Errorf("an issue resource must not emit pr_state, got %v", outputs["pr_state"])
	}
}

// TestSetup_IssueGHClientFailureIsTolerated: an unreachable or nonexistent
// issue must not fail setup — branch work can start before the issue exists.
func TestSetup_IssueGHClientFailureIsTolerated(t *testing.T) {
	mgr := &fakeManager{addInfo: &workspace.WorkspaceInfo{WorktreePath: "/roots/wt/issue-7"}}
	outputs, err := Setup(context.Background(), SetupOptions{
		ResourceID:  "https://github.com/acme/widgets/issues/7",
		SessionName: "acme/widgets-7",
		Manager:     mgr,
		GHClient:    &fakeGHClient{err: errors.New("HTTP 404")},
	})
	if err != nil {
		t.Fatalf("Setup: %v, want the gh-api failure tolerated", err)
	}
	if _, ok := outputs["title"]; ok {
		t.Errorf("title should be omitted on fetch failure, got %v", outputs["title"])
	}
	if _, ok := outputs["issue_state"]; ok {
		t.Errorf("issue_state should be omitted on fetch failure, got %v", outputs["issue_state"])
	}
}

func TestSetup_PRAcquiresHeadBranchAndTitleState(t *testing.T) {
	mgr := &fakeManager{addInfo: &workspace.WorkspaceInfo{WorktreePath: "/roots/wt/pr-44"}}
	client := &fakeGHClient{responses: map[string]string{
		"repos/acme/widgets/pulls/44": `{"head":{"ref":"feat/login"},"title":"Add login","state":"OPEN"}`,
	}}
	outputs, err := Setup(context.Background(), SetupOptions{
		ResourceID:  "https://github.com/acme/widgets/pull/44",
		SessionName: "acme/widgets-44",
		Manager:     mgr,
		GHClient:    client,
	})
	if err != nil {
		t.Fatalf("Setup: %v", err)
	}
	if len(mgr.adds) != 1 {
		t.Fatalf("issued %d manager adds, want 1", len(mgr.adds))
	}
	add := mgr.adds[0]
	if add.Branch != "feat/login" || add.BaseBranch != "feat/login" {
		t.Errorf("add params = %+v, want branch derived from the PR's head ref", add)
	}
	if add.FallbackRefspec == "" {
		t.Error("a pull request acquisition must pass a fallback refspec for the merged-PR case")
	}
	if outputs["branch"] != "feat/login" || outputs["title"] != "Add login" || outputs["pr_state"] != "open" {
		t.Errorf("outputs = %v", outputs)
	}
	if _, ok := outputs["issue_state"]; ok {
		t.Errorf("a PR resource must not emit issue_state, got %v", outputs["issue_state"])
	}
}

// TestSetup_PRGHClientFailurePropagates: unlike an issue, a pull request
// resource with no reachable metadata has no branch to acquire, so the
// failure must fail setup rather than degrade silently.
func TestSetup_PRGHClientFailurePropagates(t *testing.T) {
	mgr := &fakeManager{}
	_, err := Setup(context.Background(), SetupOptions{
		ResourceID:  "https://github.com/acme/widgets/pull/44",
		SessionName: "acme/widgets-44",
		Manager:     mgr,
		GHClient:    &fakeGHClient{err: errors.New("HTTP 500")},
	})
	if err == nil {
		t.Fatal("expected the gh-api failure to propagate for a pull request")
	}
	if len(mgr.adds) != 0 {
		t.Errorf("a failed metadata fetch must not reach the manager, got %v", mgr.adds)
	}
}

func TestSetup_InvalidResource(t *testing.T) {
	mgr := &fakeManager{}
	_, err := Setup(context.Background(), SetupOptions{
		ResourceID:  "not-a-github-resource",
		SessionName: "s",
		Manager:     mgr,
	})
	if err == nil {
		t.Fatal("expected an error for an unparsable resource identifier")
	}
	if len(mgr.adds) != 0 {
		t.Errorf("an unparsable resource must not reach the manager, got %v", mgr.adds)
	}
}

func TestSetup_AcquisitionFailurePropagates(t *testing.T) {
	_, err := Setup(context.Background(), SetupOptions{
		ResourceID:  "https://github.com/acme/widgets/issues/1",
		SessionName: "acme/widgets-1",
		Manager:     &fakeManager{addErr: errors.New("repository not found")},
		GHClient:    &fakeGHClient{err: errors.New("no gh-api client configured for this test")},
	})
	if err == nil || !strings.Contains(err.Error(), "repository not found") {
		t.Fatalf("error = %v, want the acquisition failure to propagate", err)
	}
}

func TestSetup_MissingWorkspaceDirPathIsAnError(t *testing.T) {
	_, err := Setup(context.Background(), SetupOptions{
		ResourceID:  "https://github.com/acme/widgets/issues/1",
		SessionName: "acme/widgets-1",
		Manager:     &fakeManager{addInfo: &workspace.WorkspaceInfo{}},
		GHClient:    &fakeGHClient{err: errors.New("no gh-api client configured for this test")},
	})
	if err == nil || !strings.Contains(err.Error(), "worktree path") {
		t.Fatalf("error = %v, want a missing-worktree-path error", err)
	}
}

func TestAppAuth_UsesResourceOwnerRepoWhenInstallationIDIsOmitted(t *testing.T) {
	cacheRoot := t.TempDir()
	t.Setenv("XDG_CACHE_HOME", cacheRoot)
	parsed, err := github.ParseURL("https://github.com/acme/widgets/issues/42")
	if err != nil {
		t.Fatal(err)
	}

	auth, _, err := appAuth(SetupOptions{
		AppTokenBin:    "/plugins/github/bin/gh-app-token",
		AppID:          "123456",
		PrivateKeyPath: "/etc/plect/gh-app.pem",
	}, parsed)
	if err != nil {
		t.Fatalf("appAuth: %v", err)
	}
	got := strings.Join(auth.CredentialHelper, "\n")
	for _, want := range []string{
		"/plugins/github/bin/gh-app-token",
		"credential",
		"--app-id\n123456",
		"--owner\nacme",
		"--repo\nwidgets",
		"--private-key-path\n/etc/plect/gh-app.pem",
		filepath.Join(cacheRoot, "plect", "github-app-token", "worktree"),
	} {
		if !strings.Contains(got, want) {
			t.Errorf("credential helper missing %q:\n%s", want, got)
		}
	}
}

func TestAppAuth_UsesExplicitOwnerRepoForInstallationLookup(t *testing.T) {
	cacheRoot := t.TempDir()
	t.Setenv("XDG_CACHE_HOME", cacheRoot)
	parsed, err := github.ParseURL("https://github.com/acme/widgets/issues/42")
	if err != nil {
		t.Fatal(err)
	}

	auth, _, err := appAuth(SetupOptions{
		AppTokenBin:    "/plugins/github/bin/gh-app-token",
		AppID:          "123456",
		Owner:          "platform",
		Repo:           "monorepo",
		PrivateKeyPath: "/etc/plect/gh-app.pem",
	}, parsed)
	if err != nil {
		t.Fatalf("appAuth: %v", err)
	}
	got := strings.Join(auth.CredentialHelper, "\n")
	for _, want := range []string{"--owner\nplatform", "--repo\nmonorepo"} {
		if !strings.Contains(got, want) {
			t.Errorf("credential helper missing %q:\n%s", want, got)
		}
	}
}

func TestAppAuth_RejectsPartialConfiguration(t *testing.T) {
	parsed, err := github.ParseURL("https://github.com/acme/widgets/issues/42")
	if err != nil {
		t.Fatal(err)
	}
	if _, _, err := appAuth(SetupOptions{AppID: "123456"}, parsed); err == nil || !strings.Contains(err.Error(), "private_key_path") {
		t.Fatalf("error = %v, want missing private_key_path", err)
	}
	if _, _, err := appAuth(SetupOptions{PrivateKeyPath: "/etc/plect/gh-app.pem"}, parsed); err == nil || !strings.Contains(err.Error(), "app_id") {
		t.Fatalf("error = %v, want missing app_id", err)
	}
	if _, _, err := appAuth(SetupOptions{AppID: "123456", Owner: "acme", PrivateKeyPath: "/etc/plect/gh-app.pem"}, parsed); err == nil || !strings.Contains(err.Error(), "owner and repo") {
		t.Fatalf("error = %v, want missing owner/repo pair", err)
	}
}

func TestAppAuth_RejectsUnsafeRuntimeInputs(t *testing.T) {
	parsed, err := github.ParseURL("https://github.com/acme/widgets/issues/42")
	if err != nil {
		t.Fatal(err)
	}
	base := SetupOptions{
		AppID:          "123456",
		InstallationID: "789012",
		Owner:          "platform",
		Repo:           "monorepo",
		PrivateKeyPath: "/etc/plect/gh-app.pem",
	}

	tests := []struct {
		name string
		edit func(*SetupOptions)
		want string
	}{
		{
			name: "app id path traversal",
			edit: func(opts *SetupOptions) { opts.AppID = "../outside" },
			want: "app_id",
		},
		{
			name: "installation id shell characters",
			edit: func(opts *SetupOptions) { opts.InstallationID = "789;echo token" },
			want: "installation_id",
		},
		{
			name: "owner shell characters",
			edit: func(opts *SetupOptions) { opts.Owner = "platform;echo token" },
			want: "owner",
		},
		{
			name: "repo shell characters",
			edit: func(opts *SetupOptions) { opts.Repo = "mono repo" },
			want: "repo",
		},
		{
			name: "private key whitespace",
			edit: func(opts *SetupOptions) { opts.PrivateKeyPath = "/etc/plect/app key.pem" },
			want: "private_key_path",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			opts := base
			tt.edit(&opts)
			if _, _, err := appAuth(opts, parsed); err == nil || !strings.Contains(err.Error(), tt.want) {
				t.Fatalf("error = %v, want %q", err, tt.want)
			}
		})
	}
}

func TestAppAuth_NotOptedInReturnsNoMetadataClient(t *testing.T) {
	parsed, err := github.ParseURL("https://github.com/acme/widgets/issues/42")
	if err != nil {
		t.Fatal(err)
	}
	_, client, err := appAuth(SetupOptions{}, parsed)
	if err != nil {
		t.Fatalf("appAuth: %v", err)
	}
	if client != nil {
		t.Errorf("client = %v, want nil when app auth is not opted in", client)
	}
}

// Whichever runs first mints the installation token; the other reuses it
// instead of minting its own.
func TestAppAuth_MetadataClientSharesGitAuthsInstallationAndCache(t *testing.T) {
	cacheRoot := t.TempDir()
	t.Setenv("XDG_CACHE_HOME", cacheRoot)
	parsed, err := github.ParseURL("https://github.com/acme/widgets/issues/42")
	if err != nil {
		t.Fatal(err)
	}

	gitAuth, client, err := appAuth(SetupOptions{
		AppID:          "123456",
		InstallationID: "789012",
		PrivateKeyPath: "/etc/plect/gh-app.pem",
	}, parsed)
	if err != nil {
		t.Fatalf("appAuth: %v", err)
	}

	app, ok := client.(*ghapi.App)
	if !ok {
		t.Fatalf("client = %T, want *ghapi.App", client)
	}
	if app.AppID != "123456" || app.InstallationID != "789012" || app.Owner != "acme" || app.Repo != "widgets" || app.PrivateKeyPath != "/etc/plect/gh-app.pem" {
		t.Errorf("client = %+v, unexpected fields", app)
	}
	helperText := strings.Join(gitAuth.CredentialHelper, "\n")
	if !strings.Contains(helperText, app.CachePath) {
		t.Errorf("git credential helper cache path does not match the metadata client's CachePath %q:\n%s", app.CachePath, helperText)
	}
}

func TestSetup_RejectsPartialAppGitAuthBeforeAcquisition(t *testing.T) {
	mgr := &fakeManager{}
	_, err := Setup(context.Background(), SetupOptions{
		ResourceID:  "https://github.com/acme/widgets/issues/42",
		SessionName: "acme/widgets-42",
		AppID:       "123456",
		Manager:     mgr,
		GHClient:    &fakeGHClient{err: errors.New("no gh-api client configured for this test")},
	})
	if err == nil || !strings.Contains(err.Error(), "private_key_path") {
		t.Fatalf("error = %v, want missing private_key_path", err)
	}
	if len(mgr.adds) != 0 {
		t.Errorf("a partial app git auth configuration must not acquire a worktree, got %v", mgr.adds)
	}
}

func TestCleanup_ReleasesWorktreeWithoutReclaimingBranchByDefault(t *testing.T) {
	mgr := &fakeManager{}
	if err := Cleanup(context.Background(), CleanupOptions{
		WorkspaceDir: "/roots/wt/issue-42-review",
		Branch:       "issue/42+review",
		Manager:      mgr,
	}); err != nil {
		t.Fatalf("Cleanup: %v", err)
	}
	if len(mgr.removes) != 1 {
		t.Fatalf("issued %d manager removes, want 1", len(mgr.removes))
	}
	remove := mgr.removes[0]
	if remove.workspaceDir != "/roots/wt/issue-42-review" || remove.branch != "issue/42+review" {
		t.Errorf("remove = %+v", remove)
	}
	if remove.deleteBranch {
		t.Error("cleanup must not reclaim the branch unless DeleteBranch was requested")
	}
}

func TestCleanup_DeleteBranchOptIn(t *testing.T) {
	mgr := &fakeManager{}
	if err := Cleanup(context.Background(), CleanupOptions{
		WorkspaceDir: "/roots/wt/issue-42-review",
		Branch:       "issue/42+review",
		DeleteBranch: "true",
		Manager:      mgr,
	}); err != nil {
		t.Fatalf("Cleanup: %v", err)
	}
	if !mgr.removes[0].deleteBranch {
		t.Error("cleanup must reclaim the branch when DeleteBranch is requested")
	}
}

func TestCleanup_ForcePassesForceFlagToWorkspaceRemove(t *testing.T) {
	mgr := &fakeManager{}
	if err := Cleanup(context.Background(), CleanupOptions{
		WorkspaceDir: "/roots/wt/issue-42-review",
		Force:        true,
		Manager:      mgr,
	}); err != nil {
		t.Fatalf("Cleanup: %v", err)
	}
	if !mgr.removes[0].force {
		t.Errorf("remove = %+v, want force passed through", mgr.removes[0])
	}
}

func TestCleanup_NoWorkspaceDirIsANoOp(t *testing.T) {
	mgr := &fakeManager{}
	if err := Cleanup(context.Background(), CleanupOptions{Manager: mgr}); err != nil {
		t.Fatalf("Cleanup with no workspace dir: %v", err)
	}
	if len(mgr.removes) != 0 {
		t.Errorf("nothing was acquired, so nothing may be released, got %v", mgr.removes)
	}
}

func TestCleanup_FailurePropagates(t *testing.T) {
	err := Cleanup(context.Background(), CleanupOptions{
		WorkspaceDir: "/roots/wt/issue-1",
		Manager:      &fakeManager{removeErr: errors.New("worktree is dirty")},
	})
	if err == nil || !strings.Contains(err.Error(), "worktree is dirty") {
		t.Fatalf("error = %v, want the removal failure to propagate", err)
	}
}

func TestLayoutRootComesFromHookArgumentNotCoreConfig(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	configDir := filepath.Join(home, ".config", "plect")
	if err := os.MkdirAll(configDir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(configDir, "config.toml"), []byte(`workspace_dirs_root = "/configured/root"`), 0o644); err != nil {
		t.Fatal(err)
	}

	if got, want := layoutRoot("", ""), filepath.Join(home, "workspace_dirs"); got != want {
		t.Fatalf("layoutRoot with neither set = %q, want default %q instead of config.toml", got, want)
	}
	if got, want := layoutRoot("", "~/custom"), filepath.Join(home, "custom"); got != want {
		t.Fatalf("layoutRoot from the workspace-dirs root = %q, want %q", got, want)
	}
	if got := layoutRoot("", "/explicit/root"); got != "/explicit/root" {
		t.Fatalf("layoutRoot from the workspace-dirs root = %q, want /explicit/root", got)
	}
}

func TestLayoutRoot_DeclaredParameterWinsOverTheWorkspaceDirsRoot(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	if got, want := layoutRoot("~/worktrees", "/machine/root"), filepath.Join(home, "worktrees"); got != want {
		t.Errorf("layoutRoot = %q, want %q", got, want)
	}
}

func TestResolveDeleteBranch(t *testing.T) {
	tests := []struct {
		name      string
		requested string
		declared  string
		want      bool
	}{
		{name: "no intent and no default is off"},
		{name: "declared default on", declared: "true", want: true},
		{name: "caller opts in", requested: "true", want: true},
		{name: "caller opts out over a default that is on", requested: "false", declared: "true"},
		{name: "caller opts in over a default that is off", requested: "true", declared: "false", want: true},
		{name: "a misspelled default never turns deletion on", declared: "yes"},
		{name: "a misspelled request never turns deletion on", requested: "yes", declared: "true"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := ResolveDeleteBranch(tt.requested, tt.declared); got != tt.want {
				t.Errorf("ResolveDeleteBranch(%q, %q) = %v, want %v", tt.requested, tt.declared, got, tt.want)
			}
		})
	}
}

func TestCleanup_DeleteBranchDefaultAppliesWhenTheCallerExpressedNoIntent(t *testing.T) {
	mgr := &fakeManager{}
	if err := Cleanup(context.Background(), CleanupOptions{
		WorkspaceDir:        "/roots/wt/issue-42",
		Branch:              "issue/42",
		DeleteBranchDefault: "true",
		Manager:             mgr,
	}); err != nil {
		t.Fatalf("Cleanup: %v", err)
	}
	if !mgr.removes[0].deleteBranch {
		t.Error("cleanup must reclaim the branch when the workflow declares delete_branch_default = true")
	}
}

func TestSetup_DeclaredBranchNamingParametersSteerTheAcquiredBranch(t *testing.T) {
	mgr := &fakeManager{addInfo: &workspace.WorkspaceInfo{WorktreePath: "/roots/wt/work-issue-42-review"}}
	_, err := Setup(context.Background(), SetupOptions{
		ResourceID:          "https://github.com/acme/widgets/issues/42",
		SessionName:         "acme/widgets-42+review",
		IssueBranchTemplate: "work/{repo}-{number}",
		TaggedBranchSuffix:  "/{tag}",
		Manager:             mgr,
		GHClient:            &fakeGHClient{err: errors.New("no gh-api client configured for this test")},
	})
	if err != nil {
		t.Fatalf("Setup: %v", err)
	}
	add := mgr.adds[0]
	if add.BaseBranch != "work/widgets-42" || add.Branch != "work/widgets-42/review" {
		t.Errorf("add params = %+v", add)
	}
}

// A pull request's branch is the head ref GitHub reports, so the issue naming
// parameter must not reshape it.
func TestSetup_IssueBranchTemplateDoesNotApplyToAPullRequest(t *testing.T) {
	mgr := &fakeManager{addInfo: &workspace.WorkspaceInfo{WorktreePath: "/roots/wt/feat-login"}}
	_, err := Setup(context.Background(), SetupOptions{
		ResourceID:          "https://github.com/acme/widgets/pull/44",
		SessionName:         "acme/widgets-44",
		IssueBranchTemplate: "work/{number}",
		Manager:             mgr,
		GHClient: &fakeGHClient{responses: map[string]string{
			"repos/acme/widgets/pulls/44": `{"head":{"ref":"feat/login"},"title":"Add login","state":"open"}`,
		}},
	})
	if err != nil {
		t.Fatalf("Setup: %v", err)
	}
	if mgr.adds[0].Branch != "feat/login" {
		t.Errorf("branch = %q, want the head ref", mgr.adds[0].Branch)
	}
}
