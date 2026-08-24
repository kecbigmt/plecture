package githubapp

import (
	"crypto"
	"crypto/rand"
	"crypto/rsa"
	"crypto/sha256"
	"crypto/x509"
	"encoding/base64"
	"encoding/json"
	"encoding/pem"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func testKey(t *testing.T) *rsa.PrivateKey {
	t.Helper()
	key, err := rsa.GenerateKey(rand.Reader, 2048)
	if err != nil {
		t.Fatalf("generate test key: %v", err)
	}
	return key
}

func writePEMKey(t *testing.T, key *rsa.PrivateKey) string {
	t.Helper()
	dir := t.TempDir()
	path := filepath.Join(dir, "app.pem")
	block := &pem.Block{Type: "RSA PRIVATE KEY", Bytes: x509.MarshalPKCS1PrivateKey(key)}
	if err := os.WriteFile(path, pem.EncodeToMemory(block), 0o600); err != nil {
		t.Fatal(err)
	}
	return path
}

func TestLoadPrivateKey_ParsesPKCS1(t *testing.T) {
	key := testKey(t)
	path := writePEMKey(t, key)

	got, err := LoadPrivateKey(path)
	if err != nil {
		t.Fatalf("LoadPrivateKey: %v", err)
	}
	if !got.Equal(key) {
		t.Fatal("parsed key does not match the written key")
	}
}

func TestLoadPrivateKey_ParsesPKCS8(t *testing.T) {
	key := testKey(t)
	raw, err := x509.MarshalPKCS8PrivateKey(key)
	if err != nil {
		t.Fatal(err)
	}
	dir := t.TempDir()
	path := filepath.Join(dir, "app.pem")
	block := &pem.Block{Type: "PRIVATE KEY", Bytes: raw}
	if err := os.WriteFile(path, pem.EncodeToMemory(block), 0o600); err != nil {
		t.Fatal(err)
	}

	got, err := LoadPrivateKey(path)
	if err != nil {
		t.Fatalf("LoadPrivateKey: %v", err)
	}
	if !got.Equal(key) {
		t.Fatal("parsed key does not match the written key")
	}
}

func TestLoadPrivateKey_MissingFile(t *testing.T) {
	_, err := LoadPrivateKey(filepath.Join(t.TempDir(), "missing.pem"))
	if err == nil {
		t.Fatal("want error for a missing key file")
	}
}

func TestLoadPrivateKey_NotPEM(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "app.pem")
	if err := os.WriteFile(path, []byte("not a pem file"), 0o600); err != nil {
		t.Fatal(err)
	}
	_, err := LoadPrivateKey(path)
	if err == nil {
		t.Fatal("want error for a non-PEM file")
	}
}

// The private key's own bytes must never surface in an error string: a
// wrapper composing this error onto stderr is trusted (by the wrapper's own
// acceptance criterion) to already be safe to print.
func TestLoadPrivateKey_ErrorsNeverEchoKeyBytes(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "app.pem")
	secretMarker := "MARKER-THAT-MUST-NOT-LEAK"
	if err := os.WriteFile(path, []byte(secretMarker), 0o600); err != nil {
		t.Fatal(err)
	}
	_, err := LoadPrivateKey(path)
	if err == nil {
		t.Fatal("want error")
	}
	if strings.Contains(err.Error(), secretMarker) {
		t.Fatalf("error echoed key content: %v", err)
	}
}

func TestBuildJWT_HasExpectedClaims(t *testing.T) {
	key := testKey(t)
	now := time.Date(2026, 8, 24, 12, 0, 0, 0, time.UTC)

	token, err := BuildJWT("12345", key, now)
	if err != nil {
		t.Fatalf("BuildJWT: %v", err)
	}
	parts := strings.Split(token, ".")
	if len(parts) != 3 {
		t.Fatalf("token has %d segments, want 3", len(parts))
	}
	payloadRaw, err := base64.RawURLEncoding.DecodeString(parts[1])
	if err != nil {
		t.Fatalf("decode payload: %v", err)
	}
	var claims struct {
		Iat int64  `json:"iat"`
		Exp int64  `json:"exp"`
		Iss string `json:"iss"`
	}
	if err := json.Unmarshal(payloadRaw, &claims); err != nil {
		t.Fatalf("unmarshal claims: %v", err)
	}
	if claims.Iss != "12345" {
		t.Errorf("iss = %q, want 12345", claims.Iss)
	}
	if claims.Iat >= now.Unix() {
		t.Errorf("iat = %d, want before now (%d) to absorb clock drift", claims.Iat, now.Unix())
	}
	if claims.Exp <= now.Unix() || claims.Exp > now.Add(10*time.Minute).Unix() {
		t.Errorf("exp = %d, want after now and within GitHub's 10-minute ceiling", claims.Exp)
	}
}

func TestBuildJWT_SignatureVerifiesUnderTheSamePublicKey(t *testing.T) {
	key := testKey(t)
	token, err := BuildJWT("1", key, time.Now())
	if err != nil {
		t.Fatalf("BuildJWT: %v", err)
	}
	parts := strings.Split(token, ".")
	sig, err := base64.RawURLEncoding.DecodeString(parts[2])
	if err != nil {
		t.Fatal(err)
	}
	hashed := sha256.Sum256([]byte(parts[0] + "." + parts[1]))
	if err := rsa.VerifyPKCS1v15(&key.PublicKey, crypto.SHA256, hashed[:], sig); err != nil {
		t.Fatalf("signature does not verify: %v", err)
	}
}

func TestMintInstallationToken_Success(t *testing.T) {
	var gotAuth, gotAccept, gotPath string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotAuth = r.Header.Get("Authorization")
		gotAccept = r.Header.Get("Accept")
		gotPath = r.URL.Path
		if r.Method != http.MethodPost {
			t.Errorf("method = %s, want POST", r.Method)
		}
		w.WriteHeader(http.StatusCreated)
		_, _ = w.Write([]byte(`{"token":"ghs_faketoken123","expires_at":"2026-08-24T13:00:00Z"}`))
	}))
	defer srv.Close()

	got, err := MintInstallationToken(srv.Client(), srv.URL, "jwt-value", "999")
	if err != nil {
		t.Fatalf("MintInstallationToken: %v", err)
	}
	if got.Token != "ghs_faketoken123" {
		t.Errorf("token = %q, want ghs_faketoken123", got.Token)
	}
	want, _ := time.Parse(time.RFC3339, "2026-08-24T13:00:00Z")
	if !got.ExpiresAt.Equal(want) {
		t.Errorf("expires_at = %v, want %v", got.ExpiresAt, want)
	}
	if gotAuth != "Bearer jwt-value" {
		t.Errorf("Authorization = %q, want Bearer jwt-value", gotAuth)
	}
	if gotAccept != "application/vnd.github+json" {
		t.Errorf("Accept = %q", gotAccept)
	}
	if gotPath != "/app/installations/999/access_tokens" {
		t.Errorf("path = %q", gotPath)
	}
}

func TestMintInstallationToken_ErrorSurfacesGitHubMessageNotRawBody(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusUnauthorized)
		_, _ = w.Write([]byte(`{"message":"Bad credentials","documentation_url":"https://docs.example/x"}`))
	}))
	defer srv.Close()

	_, err := MintInstallationToken(srv.Client(), srv.URL, "jwt-value", "999")
	if err == nil {
		t.Fatal("want error")
	}
	if !strings.Contains(err.Error(), "Bad credentials") {
		t.Errorf("error = %v, want it to surface GitHub's message", err)
	}
}

func TestMintInstallationToken_MissingTokenInResponseIsError(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusCreated)
		_, _ = w.Write([]byte(`{"expires_at":"2026-08-24T13:00:00Z"}`))
	}))
	defer srv.Close()

	_, err := MintInstallationToken(srv.Client(), srv.URL, "jwt-value", "999")
	if err == nil {
		t.Fatal("want error for a response with no token field")
	}
}

func TestResolveInstallationID_Success(t *testing.T) {
	var gotPath string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotPath = r.URL.Path
		_, _ = w.Write([]byte(`{"id":424242}`))
	}))
	defer srv.Close()

	id, err := ResolveInstallationID(srv.Client(), srv.URL, "jwt-value", "acme", "widgets")
	if err != nil {
		t.Fatalf("ResolveInstallationID: %v", err)
	}
	if id != "424242" {
		t.Errorf("id = %q, want 424242", id)
	}
	if gotPath != "/repos/acme/widgets/installation" {
		t.Errorf("path = %q", gotPath)
	}
}

func TestResolveInstallationID_NotFoundSurfacesStatus(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusNotFound)
		_, _ = w.Write([]byte(`{"message":"Not Found"}`))
	}))
	defer srv.Close()

	_, err := ResolveInstallationID(srv.Client(), srv.URL, "jwt-value", "acme", "widgets")
	if err == nil {
		t.Fatal("want error")
	}
	if !strings.Contains(err.Error(), "Not Found") {
		t.Errorf("error = %v, want GitHub's message surfaced", err)
	}
}
