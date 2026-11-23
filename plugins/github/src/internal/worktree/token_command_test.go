package worktree

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"github.com/kecbigmt/plecture/plugins/github/src/internal/ghapi"
	"github.com/kecbigmt/plecture/plugins/github/src/internal/github"
)

func fakeTokenCommand(t *testing.T, script string) []string {
	t.Helper()
	path := filepath.Join(t.TempDir(), "installation-token")
	if err := os.WriteFile(path, []byte("#!/bin/sh\n"+script+"\n"), 0o700); err != nil {
		t.Fatal(err)
	}
	return []string{path, "get"}
}

type recordingGHClient struct{ calls int }

func (c *recordingGHClient) JSON(context.Context, ...string) ([]byte, error) {
	c.calls++
	return nil, fmt.Errorf("ambient client must not be consulted")
}

func TestAppAuth_TokenCommandDrivesBothGitAndMetadataWithoutAKey(t *testing.T) {
	t.Setenv("XDG_CACHE_HOME", t.TempDir())
	parsed, err := github.ParseURL("https://github.com/acme/widgets/pull/44")
	if err != nil {
		t.Fatal(err)
	}
	command := fakeTokenCommand(t, `printf 'ghs_from_command\n'`)

	gitAuth, client, err := appAuth(SetupOptions{
		AppTokenBin:  "/plugins/github/bin/gh-app-token",
		TokenCommand: command,
	}, parsed)
	if err != nil {
		t.Fatalf("appAuth: %v", err)
	}

	commandJSON, err := json.Marshal(command)
	if err != nil {
		t.Fatal(err)
	}
	wantHelper := []string{"/plugins/github/bin/gh-app-token", "credential", "--token-command", string(commandJSON)}
	if !reflect.DeepEqual(gitAuth.CredentialHelper, wantHelper) {
		t.Errorf("credential helper = %q, want %q", gitAuth.CredentialHelper, wantHelper)
	}

	app, ok := client.(*ghapi.App)
	if !ok {
		t.Fatalf("client = %T, want *ghapi.App", client)
	}
	if app.PrivateKeyPath != "" || app.CachePath != "" {
		t.Errorf("client = %+v, want no key path and no token cache", app)
	}
	var authorization string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		authorization = r.Header.Get("Authorization")
		if _, err := w.Write([]byte(`{"head":{"ref":"feat/login"},"title":"Add login","state":"open"}`)); err != nil {
			t.Error(err)
		}
	}))
	defer srv.Close()
	app.BaseURL = srv.URL
	if _, err := github.FetchPullMeta(context.Background(), app, parsed.OwnerRepo, parsed.Number); err != nil {
		t.Fatalf("FetchPullMeta: %v", err)
	}
	if authorization != "Bearer ghs_from_command" {
		t.Errorf("Authorization = %q, want the command's token", authorization)
	}
}

func TestAppAuth_TokenCommandAndPrivateKeyPathAreMutuallyExclusive(t *testing.T) {
	parsed, err := github.ParseURL("https://github.com/acme/widgets/issues/42")
	if err != nil {
		t.Fatal(err)
	}
	_, _, err = appAuth(SetupOptions{
		AppID:          "123456",
		InstallationID: "789012",
		PrivateKeyPath: "/etc/plect/gh-app.pem",
		TokenCommand:   []string{"/usr/local/bin/installation-token", "get"},
	}, parsed)
	if err == nil || !strings.Contains(err.Error(), "private_key_path") || !strings.Contains(err.Error(), "token_command") || !strings.Contains(err.Error(), "mutually exclusive") {
		t.Fatalf("error = %v, want private_key_path and token_command named mutually exclusive", err)
	}
}

func TestSetup_BrokenTokenCommandFailsSetupWithoutFallingBackToAmbientCredentials(t *testing.T) {
	for _, tc := range []struct{ name, script, want string }{
		{"non-zero exit", "printf 'ghs_leaked\\n'; echo ghs_leaked >&2; exit 3", "exit status 3"},
		{"empty output", "exit 0", "invalid output"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			mgr := &fakeManager{}
			ambient := &recordingGHClient{}
			command := fakeTokenCommand(t, tc.script)
			_, err := Setup(context.Background(), SetupOptions{
				ResourceID:   "https://github.com/acme/widgets/issues/42",
				SessionName:  "acme/widgets-42",
				TokenCommand: command,
				Manager:      mgr,
				GHClient:     ambient,
			})
			if err == nil || !strings.Contains(err.Error(), command[0]) || !strings.Contains(err.Error(), tc.want) {
				t.Fatalf("error = %v, want one naming %s and %q", err, command[0], tc.want)
			}
			if strings.Contains(err.Error(), "ghs_leaked") {
				t.Errorf("error leaks the token: %v", err)
			}
			if len(mgr.adds) != 0 {
				t.Errorf("git ran after the token command failed: %v", mgr.adds)
			}
			if ambient.calls != 0 {
				t.Errorf("metadata fell back to the ambient client %d time(s)", ambient.calls)
			}
		})
	}
}

func TestSetup_TokenCommandNeverReachesOutputsOrTheCredentialHelper(t *testing.T) {
	t.Setenv("XDG_CACHE_HOME", t.TempDir())
	command := fakeTokenCommand(t, `printf 'ghs_secret_value\n'`)
	parsed, err := github.ParseURL("https://github.com/acme/widgets/issues/42")
	if err != nil {
		t.Fatal(err)
	}
	gitAuth, _, err := appAuth(SetupOptions{TokenCommand: command}, parsed)
	if err != nil {
		t.Fatalf("appAuth: %v", err)
	}
	outputs, err := Setup(context.Background(), SetupOptions{
		ResourceID:   "https://github.com/acme/widgets/issues/42",
		SessionName:  "acme/widgets-42",
		TokenCommand: command,
		Manager:      &fakeManager{},
	})
	if err != nil {
		t.Fatalf("Setup: %v", err)
	}
	recorded, err := json.Marshal(outputs)
	if err != nil {
		t.Fatal(err)
	}
	for name, text := range map[string]string{
		"outputs":           string(recorded),
		"credential helper": strings.Join(gitAuth.CredentialHelper, " "),
	} {
		if strings.Contains(text, "ghs_secret_value") {
			t.Errorf("%s carries the token: %s", name, text)
		}
	}
}
