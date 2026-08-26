package githubapp

import (
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/kecbigmt/plecture/plugins/github/src/internal/apptoken"
)

func fakeTokenServer(t *testing.T, token string, mints *int) *httptest.Server {
	t.Helper()
	return httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case strings.HasSuffix(r.URL.Path, "/access_tokens"):
			*mints++
			w.WriteHeader(http.StatusCreated)
			_, _ = w.Write([]byte(`{"token":"` + token + `","expires_at":"2099-01-01T00:00:00Z"}`))
		case strings.HasSuffix(r.URL.Path, "/installation"):
			_, _ = w.Write([]byte(`{"id":555}`))
		default:
			w.WriteHeader(http.StatusNotFound)
		}
	}))
}

func TestToken_MintsWithExplicitInstallationID(t *testing.T) {
	var mints int
	srv := fakeTokenServer(t, "ghs_direct", &mints)
	defer srv.Close()

	cache := apptoken.NewCache(filepath.Join(t.TempDir(), "cache.json"))
	got, err := Token(cache, 5*time.Minute, srv.Client(), srv.URL, "123", testKey(t), "789", "", "")
	if err != nil {
		t.Fatalf("Token: %v", err)
	}
	if got != "ghs_direct" {
		t.Errorf("token = %q, want ghs_direct", got)
	}
}

func TestToken_ResolvesInstallationIDFromOwnerRepoOnlyWhenMinting(t *testing.T) {
	var mints int
	srv := fakeTokenServer(t, "ghs_from_owner_repo", &mints)
	defer srv.Close()

	cache := apptoken.NewCache(filepath.Join(t.TempDir(), "cache.json"))
	got, err := Token(cache, 5*time.Minute, srv.Client(), srv.URL, "123", testKey(t), "", "acme", "widgets")
	if err != nil {
		t.Fatalf("Token: %v", err)
	}
	if got != "ghs_from_owner_repo" {
		t.Errorf("token = %q, want ghs_from_owner_repo", got)
	}
}

func TestToken_ReusesCachedTokenAcrossCalls(t *testing.T) {
	var mints int
	srv := fakeTokenServer(t, "ghs_cached", &mints)
	defer srv.Close()

	cache := apptoken.NewCache(filepath.Join(t.TempDir(), "cache.json"))
	key := testKey(t)
	if _, err := Token(cache, 5*time.Minute, srv.Client(), srv.URL, "123", key, "789", "", ""); err != nil {
		t.Fatalf("first Token: %v", err)
	}
	if _, err := Token(cache, 5*time.Minute, srv.Client(), srv.URL, "123", key, "789", "", ""); err != nil {
		t.Fatalf("second Token: %v", err)
	}
	if mints != 1 {
		t.Errorf("mints = %d, want 1 (second call should reuse the cached token)", mints)
	}
}

func TestToken_MintFailurePropagates(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusUnauthorized)
		_, _ = w.Write([]byte(`{"message":"Bad credentials"}`))
	}))
	defer srv.Close()

	cache := apptoken.NewCache(filepath.Join(t.TempDir(), "cache.json"))
	_, err := Token(cache, 5*time.Minute, srv.Client(), srv.URL, "123", testKey(t), "789", "", "")
	if err == nil || !strings.Contains(err.Error(), "Bad credentials") {
		t.Fatalf("error = %v, want it to surface GitHub's message", err)
	}
}
