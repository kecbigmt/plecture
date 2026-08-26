package ghapi

import (
	"context"
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
)

func writeTestAppKey(t *testing.T) string {
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

func fakeGitHubAPI(t *testing.T, token, resourcePath, resourceBody string) (*httptest.Server, *int) {
	t.Helper()
	mints := 0
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case strings.HasSuffix(r.URL.Path, "/access_tokens"):
			mints++
			w.WriteHeader(http.StatusCreated)
			_, _ = w.Write([]byte(`{"token":"` + token + `","expires_at":"2099-01-01T00:00:00Z"}`))
		case strings.HasSuffix(r.URL.Path, "/installation"):
			_, _ = w.Write([]byte(`{"id":555}`))
		case r.URL.Path == "/"+resourcePath:
			if got := r.Header.Get("Authorization"); got != "Bearer "+token {
				w.WriteHeader(http.StatusUnauthorized)
				_, _ = w.Write([]byte(`{"message":"missing or wrong installation token"}`))
				return
			}
			_, _ = w.Write([]byte(resourceBody))
		default:
			w.WriteHeader(http.StatusNotFound)
		}
	}))
	return srv, &mints
}

func TestApp_JSONFetchesResourceAuthenticatedAsTheInstallation(t *testing.T) {
	srv, _ := fakeGitHubAPI(t, "ghs_app_token", "repos/acme/widgets/pulls/7", `{"head":{"ref":"feature"}}`)
	defer srv.Close()

	app := &App{
		AppID:          "123456",
		InstallationID: "789012",
		PrivateKeyPath: writeTestAppKey(t),
		CachePath:      filepath.Join(t.TempDir(), "cache.json"),
		BaseURL:        srv.URL,
	}
	body, err := app.JSON(context.Background(), "repos/acme/widgets/pulls/7")
	if err != nil {
		t.Fatalf("JSON: %v", err)
	}
	if got, want := string(body), `{"head":{"ref":"feature"}}`; got != want {
		t.Errorf("body = %q, want %q", got, want)
	}
}

func TestApp_JSONResolvesInstallationIDFromOwnerRepo(t *testing.T) {
	srv, _ := fakeGitHubAPI(t, "ghs_from_owner_repo", "repos/acme/widgets/issues/9", `{"title":"x","state":"open"}`)
	defer srv.Close()

	app := &App{
		AppID:          "123456",
		Owner:          "acme",
		Repo:           "widgets",
		PrivateKeyPath: writeTestAppKey(t),
		CachePath:      filepath.Join(t.TempDir(), "cache.json"),
		BaseURL:        srv.URL,
	}
	body, err := app.JSON(context.Background(), "repos/acme/widgets/issues/9")
	if err != nil {
		t.Fatalf("JSON: %v", err)
	}
	if got, want := string(body), `{"title":"x","state":"open"}`; got != want {
		t.Errorf("body = %q, want %q", got, want)
	}
}

func TestApp_JSONReusesCachedTokenAcrossCalls(t *testing.T) {
	srv, mints := fakeGitHubAPI(t, "ghs_cached", "repos/acme/widgets/pulls/1", `{}`)
	defer srv.Close()

	app := &App{
		AppID:          "123456",
		InstallationID: "789012",
		PrivateKeyPath: writeTestAppKey(t),
		CachePath:      filepath.Join(t.TempDir(), "cache.json"),
		BaseURL:        srv.URL,
	}
	if _, err := app.JSON(context.Background(), "repos/acme/widgets/pulls/1"); err != nil {
		t.Fatalf("first JSON: %v", err)
	}
	if _, err := app.JSON(context.Background(), "repos/acme/widgets/pulls/1"); err != nil {
		t.Fatalf("second JSON: %v", err)
	}
	if *mints != 1 {
		t.Errorf("mints = %d, want 1 (second call should reuse the cached token)", *mints)
	}
}

func TestApp_JSONSurfacesGitHubErrorMessageOnFailure(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case strings.HasSuffix(r.URL.Path, "/access_tokens"):
			w.WriteHeader(http.StatusCreated)
			_, _ = w.Write([]byte(`{"token":"ghs_x","expires_at":"2099-01-01T00:00:00Z"}`))
		default:
			w.WriteHeader(http.StatusNotFound)
			_, _ = w.Write([]byte(`{"message":"Not Found"}`))
		}
	}))
	defer srv.Close()

	app := &App{
		AppID:          "123456",
		InstallationID: "789012",
		PrivateKeyPath: writeTestAppKey(t),
		CachePath:      filepath.Join(t.TempDir(), "cache.json"),
		BaseURL:        srv.URL,
	}
	_, err := app.JSON(context.Background(), "repos/acme/widgets/pulls/404")
	if err == nil || !strings.Contains(err.Error(), "Not Found") {
		t.Fatalf("error = %v, want it to surface GitHub's own message", err)
	}
}

func TestApp_JSONMissingPrivateKeyFailsLoudly(t *testing.T) {
	app := &App{
		AppID:          "123456",
		InstallationID: "789012",
		PrivateKeyPath: filepath.Join(t.TempDir(), "missing.pem"),
		CachePath:      filepath.Join(t.TempDir(), "cache.json"),
	}
	if _, err := app.JSON(context.Background(), "repos/acme/widgets/pulls/1"); err == nil {
		t.Fatal("want error for a missing private key")
	}
}
