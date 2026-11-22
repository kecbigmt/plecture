package main

import (
	"crypto/rand"
	"crypto/rsa"
	"crypto/x509"
	"encoding/pem"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/kecbigmt/plecture/plugins/github/src/internal/ratebudget"
	"github.com/kecbigmt/plecture/plugins/github/src/internal/watcher"
)

func writeAppKey(t *testing.T) string {
	t.Helper()
	key, err := rsa.GenerateKey(rand.Reader, 2048)
	if err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(t.TempDir(), "app.pem")
	block := &pem.Block{Type: "RSA PRIVATE KEY", Bytes: x509.MarshalPKCS1PrivateKey(key)}
	if err := os.WriteFile(path, pem.EncodeToMemory(block), 0o600); err != nil {
		t.Fatal(err)
	}
	return path
}

// fakeInstallationTokenServer mints installation tokens and resolves
// owner/repo to an installation id, mirroring githubapp's own fake token
// endpoint.
func fakeInstallationTokenServer(t *testing.T, token string) *httptest.Server {
	t.Helper()
	return httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case strings.HasSuffix(r.URL.Path, "/access_tokens"):
			w.WriteHeader(http.StatusCreated)
			_, _ = w.Write([]byte(`{"token":"` + token + `","expires_at":"2099-01-01T00:00:00Z"}`))
		case strings.HasSuffix(r.URL.Path, "/installation"):
			_, _ = w.Write([]byte(`{"id":555}`))
		default:
			w.WriteHeader(http.StatusNotFound)
		}
	}))
}

func TestAppAuthFromEnv_UnsetIsNoAppAuth(t *testing.T) {
	cfg, err := appAuthFromEnv(t.TempDir())
	if err != nil {
		t.Fatalf("appAuthFromEnv: %v", err)
	}
	if cfg != nil {
		t.Fatalf("cfg = %+v, want nil when GITHUB_WATCHER_APP_ID is unset", cfg)
	}
}

func TestAppAuthFromEnv_PrivateKeyWithoutAppIDFails(t *testing.T) {
	t.Setenv(envPrivateKeyPath, "/host/key")
	_, err := appAuthFromEnv(t.TempDir())
	if err == nil || !strings.Contains(err.Error(), envAppID) || !strings.Contains(err.Error(), envPrivateKeyPath) {
		t.Fatalf("error = %v", err)
	}
}

func TestAppAuthFromEnv_AppIDWithoutPrivateKeyPathFailsLoud(t *testing.T) {
	t.Setenv(envAppID, "123456")
	t.Setenv(envInstallationID, "789012")
	_, err := appAuthFromEnv(t.TempDir())
	if err == nil {
		t.Fatal("want an error when the App id is set but the private key path is not")
	}
}

func TestAppAuthFromEnv_TokenCommandIsExclusive(t *testing.T) {
	for _, tc := range []struct {
		name, key, command string
		valid              bool
	}{
		{"command", "", `["/host/token","get"]`, true},
		{"both", "/private/key", `["/host/token"]`, false},
		{"neither", "", "", false},
		{"invalid JSON", "", `"/host/token"`, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Setenv(envAppID, "123456")
			t.Setenv(envInstallationID, "789012")
			t.Setenv(envPrivateKeyPath, tc.key)
			t.Setenv(envTokenCommand, tc.command)
			_, err := appAuthFromEnv(t.TempDir())
			if (err == nil) != tc.valid {
				t.Fatalf("error = %v", err)
			}
			if err != nil && (!strings.Contains(err.Error(), envPrivateKeyPath) || !strings.Contains(err.Error(), envTokenCommand)) {
				t.Fatalf("error = %v", err)
			}
		})
	}
}

func TestAppAuthFromEnv_CommandNeedsNoAppIdentifiers(t *testing.T) {
	t.Setenv(envAppID, "")
	t.Setenv(envInstallationID, "")
	t.Setenv(envOwner, "")
	t.Setenv(envRepo, "")
	t.Setenv(envTokenCommand, `["/host/token"]`)
	if _, err := appAuthFromEnv(t.TempDir()); err != nil {
		t.Fatal(err)
	}
}

func TestAppAuthFromEnv_AppIDWithoutInstallationOrOwnerRepoFailsLoud(t *testing.T) {
	t.Setenv(envAppID, "123456")
	t.Setenv(envPrivateKeyPath, writeAppKey(t))
	_, err := appAuthFromEnv(t.TempDir())
	if err == nil {
		t.Fatal("want an error when neither installation id nor owner+repo identify an installation")
	}
}

func TestAppAuthFromEnv_OwnerWithoutRepoFailsLoud(t *testing.T) {
	t.Setenv(envAppID, "123456")
	t.Setenv(envPrivateKeyPath, writeAppKey(t))
	t.Setenv(envOwner, "acme")
	_, err := appAuthFromEnv(t.TempDir())
	if err == nil {
		t.Fatal("want an error when owner is set without repo")
	}
}

func TestAppAuthFromEnv_CachePathDefaultsUnderDataDir(t *testing.T) {
	dataDir := t.TempDir()
	t.Setenv(envAppID, "123456")
	t.Setenv(envInstallationID, "789012")
	t.Setenv(envPrivateKeyPath, writeAppKey(t))
	cfg, err := appAuthFromEnv(dataDir)
	if err != nil {
		t.Fatalf("appAuthFromEnv: %v", err)
	}
	want := filepath.Join(dataDir, ".app-token-cache.json")
	if cfg.cachePath != want {
		t.Errorf("cachePath = %q, want %q", cfg.cachePath, want)
	}
}

func TestAppAuthConfig_TokenFuncFailsLoudOnUnreadablePrivateKey(t *testing.T) {
	cfg := &appAuthConfig{
		appID:          "123456",
		installationID: "789012",
		privateKeyPath: filepath.Join(t.TempDir(), "missing.pem"),
		cachePath:      filepath.Join(t.TempDir(), "cache.json"),
		baseURL:        "http://unused.invalid",
	}
	if _, err := cfg.tokenFunc(); err == nil {
		t.Fatal("want an error for an unreadable private key")
	}
}

func TestAppAuthConfig_TokenFuncMintsAndReusesTheSharedCache(t *testing.T) {
	srv := fakeInstallationTokenServer(t, "ghs_minted")
	defer srv.Close()

	cfg := &appAuthConfig{
		appID:          "123456",
		installationID: "789012",
		privateKeyPath: writeAppKey(t),
		cachePath:      filepath.Join(t.TempDir(), "cache.json"),
		baseURL:        srv.URL,
	}
	tokenFn, err := cfg.tokenFunc()
	if err != nil {
		t.Fatalf("tokenFunc: %v", err)
	}
	got, err := tokenFn()
	if err != nil {
		t.Fatalf("tokenFn: %v", err)
	}
	if got != "ghs_minted" {
		t.Errorf("token = %q, want ghs_minted", got)
	}
}

func TestAppAuthTokenFunc_UnconfiguredReturnsNilFunc(t *testing.T) {
	tokenFn, err := appAuthTokenFunc(t.TempDir())
	if err != nil {
		t.Fatalf("appAuthTokenFunc: %v", err)
	}
	if tokenFn != nil {
		t.Fatal("want a nil token func when App auth is unconfigured")
	}
}

func TestAppAuthTokenFunc_InvalidConfigFailsLoud(t *testing.T) {
	t.Setenv(envAppID, "123456")
	// No private key path set.
	if _, err := appAuthTokenFunc(t.TempDir()); err == nil {
		t.Fatal("want an error for an incomplete App auth configuration")
	}
}

func TestConfigureAppAuth_UnconfiguredLeavesTokenFuncNil(t *testing.T) {
	poller := &watcher.Poller{}
	if err := configureAppAuth(poller, t.TempDir()); err != nil {
		t.Fatalf("configureAppAuth: %v", err)
	}
	if poller.TokenFunc != nil {
		t.Fatal("want TokenFunc to stay nil when App auth is unconfigured")
	}
}

func TestConfigureAppAuth_ValidConfigWiresTokenFunc(t *testing.T) {
	srv := fakeInstallationTokenServer(t, "ghs_serve")
	defer srv.Close()

	t.Setenv(envAppID, "123456")
	t.Setenv(envInstallationID, "789012")
	t.Setenv(envPrivateKeyPath, writeAppKey(t))
	t.Setenv(envBaseURL, srv.URL)

	poller := &watcher.Poller{}
	if err := configureAppAuth(poller, t.TempDir()); err != nil {
		t.Fatalf("configureAppAuth: %v", err)
	}
	if poller.TokenFunc == nil {
		t.Fatal("want TokenFunc to be wired")
	}
	got, err := poller.TokenFunc()
	if err != nil {
		t.Fatalf("TokenFunc: %v", err)
	}
	if got != "ghs_serve" {
		t.Errorf("token = %q, want ghs_serve", got)
	}
}

func TestConfigureAppAuth_CommandRefreshesAfterUnauthorized(t *testing.T) {
	dir := t.TempDir()
	command := filepath.Join(dir, "token helper")
	count := filepath.Join(dir, "count")
	seen := filepath.Join(dir, "seen")
	gh := filepath.Join(dir, "gh")
	if err := os.WriteFile(command, []byte("#!/bin/sh\necho called >> '"+count+"'\nif [ \"$(wc -l < '"+count+"')\" -lt 3 ]; then echo first; else echo second; fi\n"), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(gh, []byte("#!/bin/sh\necho \"$GH_TOKEN\" >> '"+seen+"'\nif [ \"$GH_TOKEN\" = first ]; then printf 'HTTP/1.1 401 Unauthorized\\n\\n'; exit 1; fi\nprintf 'HTTP/1.1 200 OK\\n\\n{\"state\":\"open\",\"merged\":false,\"sha\":\"deadbeef\"}'\n"), 0o700); err != nil {
		t.Fatal(err)
	}
	t.Setenv(envAppID, "123456")
	t.Setenv(envInstallationID, "789012")
	t.Setenv(envTokenCommand, `["`+command+`"]`)
	store := watcher.NewProtocolStore(t.TempDir(), t.TempDir())
	if err := store.Replace([]watcher.MembershipRow{{Session: "org/repo-1", Source: "test", Resource: watcher.MembershipResource{ID: "pr-1", Type: "github.pull_request", Locator: "https://github.com/org/repo/pull/1"}}}); err != nil {
		t.Fatal(err)
	}
	poller := &watcher.Poller{Store: store, Logger: slog.New(slog.NewTextHandler(io.Discard, nil)), GhBin: gh, Guard: ratebudget.NewGuard(t.TempDir())}
	cacheDir := t.TempDir()
	if err := configureAppAuth(poller, cacheDir); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(count); err != nil {
		t.Fatalf("command did not validate at startup: %v", err)
	}
	poller.Tick()
	if _, err := os.Stat(count); err != nil {
		t.Fatalf("command was not called: %v", err)
	}
	data, err := os.ReadFile(seen)
	if err != nil || !strings.HasPrefix(string(data), "first\nsecond\n") {
		t.Fatalf("gh tokens %q, error %v", data, err)
	}
	if _, err := os.Stat(filepath.Join(cacheDir, ".app-token-cache.json")); !os.IsNotExist(err) {
		t.Fatalf("command token cache: %v", err)
	}
}

func TestConfigureAppAuth_MintFailureFailsLoudAtStartup(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusUnauthorized)
		_, _ = w.Write([]byte(`{"message":"Bad credentials"}`))
	}))
	defer srv.Close()

	t.Setenv(envAppID, "123456")
	t.Setenv(envInstallationID, "789012")
	t.Setenv(envPrivateKeyPath, writeAppKey(t))
	t.Setenv(envBaseURL, srv.URL)

	poller := &watcher.Poller{}
	err := configureAppAuth(poller, t.TempDir())
	if err == nil {
		t.Fatal("want an error when the eager startup mint fails")
	}
	if poller.TokenFunc != nil {
		t.Error("want TokenFunc to stay unset when the eager mint failed")
	}
}

// Distinct from the mint-failure case above: this fails on config, not on
// the mint round trip.
func TestConfigureAppAuth_UnreadableKeyFailsLoudAtStartup(t *testing.T) {
	t.Setenv(envAppID, "123456")
	t.Setenv(envInstallationID, "789012")
	t.Setenv(envPrivateKeyPath, filepath.Join(t.TempDir(), "missing.pem"))

	poller := &watcher.Poller{}
	if err := configureAppAuth(poller, t.TempDir()); err == nil {
		t.Fatal("want an error for an unreadable private key")
	}
}
