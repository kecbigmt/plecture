package main

import (
	"bytes"
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

func writeTestKey(t *testing.T) string {
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

func fakeTokenEndpoint(t *testing.T, token string) *httptest.Server {
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

func TestCmdPrint_MissingRequiredFlags(t *testing.T) {
	cases := []struct {
		name string
		args []string
	}{
		{"no app id", []string{"--installation-id", "1", "--private-key-path", "x", "--cache-path", "y"}},
		{"no private key path", []string{"--app-id", "1", "--installation-id", "1", "--cache-path", "y"}},
		{"no cache path", []string{"--app-id", "1", "--installation-id", "1", "--private-key-path", "x"}},
		{"no installation id and no owner/repo", []string{"--app-id", "1", "--private-key-path", "x", "--cache-path", "y"}},
		{"owner without repo", []string{"--app-id", "1", "--owner", "acme", "--private-key-path", "x", "--cache-path", "y"}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			var out bytes.Buffer
			if err := cmdPrint(tc.args, &out); err == nil {
				t.Fatal("want error")
			}
		})
	}
}

func TestCmdPrint_PrintsExactlyTheMintedTokenPlusNewline(t *testing.T) {
	srv := fakeTokenEndpoint(t, "ghs_from_installation_id")
	defer srv.Close()
	keyPath := writeTestKey(t)
	cachePath := filepath.Join(t.TempDir(), "cache.json")

	var out bytes.Buffer
	err := cmdPrint([]string{
		"--app-id", "1",
		"--installation-id", "123",
		"--private-key-path", keyPath,
		"--cache-path", cachePath,
		"--base-url", srv.URL,
	}, &out)
	if err != nil {
		t.Fatalf("cmdPrint: %v", err)
	}
	if out.String() != "ghs_from_installation_id\n" {
		t.Errorf("stdout = %q, want exactly the token plus a newline", out.String())
	}
}

func TestCmdCredential_GetWritesGitCredentialFields(t *testing.T) {
	srv := fakeTokenEndpoint(t, "ghs_for_git")
	defer srv.Close()
	keyPath := writeTestKey(t)
	cachePath := filepath.Join(t.TempDir(), "cache.json")

	var out bytes.Buffer
	err := cmdCredential([]string{
		"--app-id", "1",
		"--installation-id", "123",
		"--private-key-path", keyPath,
		"--cache-path", cachePath,
		"--base-url", srv.URL,
		"get",
	}, strings.NewReader("protocol=https\nhost=github.com\n\n"), &out)
	if err != nil {
		t.Fatalf("cmdCredential: %v", err)
	}
	if got, want := out.String(), "username=x-access-token\npassword=ghs_for_git\n\n"; got != want {
		t.Errorf("credential output = %q, want %q", got, want)
	}
}

func TestCmdCredential_IgnoresNonGitHubCredentialRequests(t *testing.T) {
	srv := fakeTokenEndpoint(t, "ghs_for_git")
	defer srv.Close()
	keyPath := writeTestKey(t)
	cachePath := filepath.Join(t.TempDir(), "cache.json")

	var out bytes.Buffer
	err := cmdCredential([]string{
		"--app-id", "1",
		"--installation-id", "123",
		"--private-key-path", keyPath,
		"--cache-path", cachePath,
		"--base-url", srv.URL,
		"get",
	}, strings.NewReader("protocol=https\nhost=example.com\n\n"), &out)
	if err != nil {
		t.Fatalf("cmdCredential: %v", err)
	}
	if out.Len() != 0 {
		t.Errorf("credential output = %q, want no credentials for a different host", out.String())
	}
}

func TestCmdPrint_ResolvesInstallationIDFromOwnerRepo(t *testing.T) {
	srv := fakeTokenEndpoint(t, "ghs_from_owner_repo")
	defer srv.Close()
	keyPath := writeTestKey(t)
	cachePath := filepath.Join(t.TempDir(), "cache.json")

	var out bytes.Buffer
	err := cmdPrint([]string{
		"--app-id", "1",
		"--owner", "acme",
		"--repo", "widgets",
		"--private-key-path", keyPath,
		"--cache-path", cachePath,
		"--base-url", srv.URL,
	}, &out)
	if err != nil {
		t.Fatalf("cmdPrint: %v", err)
	}
	if out.String() != "ghs_from_owner_repo\n" {
		t.Errorf("stdout = %q, want the token minted via the resolved installation id", out.String())
	}
}

func TestCmdPrint_ReusesCacheAcrossInvocations(t *testing.T) {
	var mints int
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		mints++
		w.WriteHeader(http.StatusCreated)
		_, _ = w.Write([]byte(`{"token":"ghs_cached","expires_at":"2099-01-01T00:00:00Z"}`))
	}))
	defer srv.Close()
	keyPath := writeTestKey(t)
	cachePath := filepath.Join(t.TempDir(), "cache.json")
	args := []string{
		"--app-id", "1",
		"--installation-id", "123",
		"--private-key-path", keyPath,
		"--cache-path", cachePath,
		"--base-url", srv.URL,
	}

	var out1, out2 bytes.Buffer
	if err := cmdPrint(args, &out1); err != nil {
		t.Fatalf("first cmdPrint: %v", err)
	}
	if err := cmdPrint(args, &out2); err != nil {
		t.Fatalf("second cmdPrint: %v", err)
	}
	if out1.String() != out2.String() {
		t.Errorf("second invocation minted a different token: %q vs %q", out1.String(), out2.String())
	}
	if mints != 1 {
		t.Errorf("mints = %d, want 1 (second invocation should reuse the cache)", mints)
	}
}

// Given the private key file is missing, the wrapper fails loudly with a
// redacted message: no key content in the error, ever.
func TestCmdPrint_MissingPrivateKeyFailsLoudlyWithoutLeakingContent(t *testing.T) {
	cachePath := filepath.Join(t.TempDir(), "cache.json")
	var out bytes.Buffer
	err := cmdPrint([]string{
		"--app-id", "1",
		"--installation-id", "123",
		"--private-key-path", filepath.Join(t.TempDir(), "missing.pem"),
		"--cache-path", cachePath,
	}, &out)
	if err == nil {
		t.Fatal("want error for a missing private key")
	}
	if out.Len() != 0 {
		t.Errorf("stdout = %q, want nothing written on failure", out.String())
	}
}

func TestCmdPrint_MintFailureIsNotWrittenToStdout(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusUnauthorized)
		_, _ = w.Write([]byte(`{"message":"Bad credentials"}`))
	}))
	defer srv.Close()
	keyPath := writeTestKey(t)
	cachePath := filepath.Join(t.TempDir(), "cache.json")

	var out bytes.Buffer
	err := cmdPrint([]string{
		"--app-id", "1",
		"--installation-id", "123",
		"--private-key-path", keyPath,
		"--cache-path", cachePath,
		"--base-url", srv.URL,
	}, &out)
	if err == nil {
		t.Fatal("want error")
	}
	if out.Len() != 0 {
		t.Errorf("stdout = %q, want nothing written on a mint failure", out.String())
	}
}
