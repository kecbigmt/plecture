package main

import (
	"crypto/rand"
	"crypto/rsa"
	"crypto/x509"
	"encoding/pem"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

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

// With none of the App env vars set, both entry points must be a no-op —
// ambient `gh auth` stays unchanged.
func TestAppAuthFromEnv_UnsetIsNoAppAuth(t *testing.T) {
	cfg, err := appAuthFromEnv(t.TempDir())
	if err != nil {
		t.Fatalf("appAuthFromEnv: %v", err)
	}
	if cfg != nil {
		t.Fatalf("cfg = %+v, want nil when GITHUB_WATCHER_APP_ID is unset", cfg)
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

// A missing/unreadable private key must fail loud here — at config-build
// time — not on the first mint attempt buried inside a poll tick.
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

// appAuthTokenFunc (gh-api's entry point) surfaces a mint failure directly
// from the returned func, with no separate eager-mint phase.
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

// configureAppAuth is serve's entry point: unconfigured, it leaves
// poller.TokenFunc nil (ambient auth unchanged).
func TestConfigureAppAuth_UnconfiguredLeavesTokenFuncNil(t *testing.T) {
	poller := &watcher.Poller{}
	if err := configureAppAuth(poller, t.TempDir()); err != nil {
		t.Fatalf("configureAppAuth: %v", err)
	}
	if poller.TokenFunc != nil {
		t.Fatal("want TokenFunc to stay nil when App auth is unconfigured")
	}
}

// A valid App auth configuration wires poller.TokenFunc and succeeds.
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

// The whole point of "fail loud": serve must not start with a bad key or a
// rejected mint left undiscovered until the first poll tick.
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

// A configured-but-broken (missing private key) App auth also fails startup
// loud, distinct from the mint-failure case above.
func TestConfigureAppAuth_UnreadableKeyFailsLoudAtStartup(t *testing.T) {
	t.Setenv(envAppID, "123456")
	t.Setenv(envInstallationID, "789012")
	t.Setenv(envPrivateKeyPath, filepath.Join(t.TempDir(), "missing.pem"))

	poller := &watcher.Poller{}
	if err := configureAppAuth(poller, t.TempDir()); err == nil {
		t.Fatal("want an error for an unreadable private key")
	}
}
