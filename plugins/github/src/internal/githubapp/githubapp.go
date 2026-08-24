// Package githubapp mints GitHub App installation access tokens: it signs
// the short-lived App JWT (RS256, per GitHub's App authentication flow) and
// exchanges it for an installation token, optionally resolving the
// installation id from an owner/repo pair first. It never persists
// anything — that is apptoken.Cache's job — and never logs a token or JWT,
// so every error path here is safe to print verbatim to an operator.
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
	"fmt"
	"io"
	"net/http"
	"os"
	"strings"
	"time"
)

// DefaultBaseURL is api.github.com. Overridable for GitHub Enterprise Server
// and for tests, which point it at an httptest server standing in for the
// real token endpoint.
const DefaultBaseURL = "https://api.github.com"

// jwtLeeway backdates "iat" to absorb clock drift between this host and
// GitHub's, per GitHub's own App-authentication guidance. jwtTTL stays
// under GitHub's 10-minute ceiling with margin for the mint round trip.
const (
	jwtLeeway = 60 * time.Second
	jwtTTL    = 9 * time.Minute
)

// LoadPrivateKey reads and parses a PEM-encoded RSA private key (PKCS#1 or
// PKCS#8, whichever GitHub's App-settings download hands the operator).
// Errors here come only from the OS or the parser and never echo the key
// material itself, so they are safe to surface to an operator as-is.
func LoadPrivateKey(path string) (*rsa.PrivateKey, error) {
	raw, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("read private key: %w", err)
	}
	block, _ := pem.Decode(raw)
	if block == nil {
		return nil, fmt.Errorf("private key at %s is not PEM-encoded", path)
	}
	if key, err := x509.ParsePKCS1PrivateKey(block.Bytes); err == nil {
		return key, nil
	}
	parsed, err := x509.ParsePKCS8PrivateKey(block.Bytes)
	if err != nil {
		return nil, fmt.Errorf("private key at %s is neither PKCS#1 nor PKCS#8 RSA", path)
	}
	key, ok := parsed.(*rsa.PrivateKey)
	if !ok {
		return nil, fmt.Errorf("private key at %s is not an RSA key", path)
	}
	return key, nil
}

// BuildJWT signs a GitHub App JWT identifying appID, valid from jwtLeeway
// before now to jwtTTL after now.
func BuildJWT(appID string, key *rsa.PrivateKey, now time.Time) (string, error) {
	header, err := jsonSegment(map[string]string{"alg": "RS256", "typ": "JWT"})
	if err != nil {
		return "", err
	}
	payload, err := jsonSegment(map[string]any{
		"iat": now.Add(-jwtLeeway).Unix(),
		"exp": now.Add(jwtTTL).Unix(),
		"iss": appID,
	})
	if err != nil {
		return "", err
	}
	signingInput := header + "." + payload
	hashed := sha256.Sum256([]byte(signingInput))
	sig, err := rsa.SignPKCS1v15(rand.Reader, key, crypto.SHA256, hashed[:])
	if err != nil {
		// rsa.SignPKCS1v15 never echoes hashed or key material in its error.
		return "", fmt.Errorf("sign app jwt: %w", err)
	}
	return signingInput + "." + base64.RawURLEncoding.EncodeToString(sig), nil
}

func jsonSegment(v any) (string, error) {
	raw, err := json.Marshal(v)
	if err != nil {
		return "", fmt.Errorf("encode jwt segment: %w", err)
	}
	return base64.RawURLEncoding.EncodeToString(raw), nil
}

// InstallationToken is a minted access token and its expiry, as reported by
// GitHub — the value apptoken.Cache persists.
type InstallationToken struct {
	Token     string
	ExpiresAt time.Time
}

// MintInstallationToken exchanges an App JWT for an installation access
// token via POST /app/installations/{id}/access_tokens.
func MintInstallationToken(client *http.Client, baseURL, jwt, installationID string) (InstallationToken, error) {
	url := strings.TrimRight(baseURL, "/") + "/app/installations/" + installationID + "/access_tokens"
	req, err := http.NewRequest(http.MethodPost, url, nil)
	if err != nil {
		return InstallationToken{}, fmt.Errorf("build mint request: %w", err)
	}
	setAppHeaders(req, jwt)

	resp, err := client.Do(req)
	if err != nil {
		return InstallationToken{}, fmt.Errorf("mint installation token: %w", redactErr(err, jwt))
	}
	defer resp.Body.Close()
	body, err := io.ReadAll(io.LimitReader(resp.Body, 1<<20))
	if err != nil {
		return InstallationToken{}, fmt.Errorf("read mint response: %w", err)
	}
	if resp.StatusCode != http.StatusCreated {
		return InstallationToken{}, fmt.Errorf("mint installation token: %s", apiErrorMessage(resp.StatusCode, body))
	}
	var decoded struct {
		Token     string    `json:"token"`
		ExpiresAt time.Time `json:"expires_at"`
	}
	if err := json.Unmarshal(body, &decoded); err != nil {
		// The body is discarded, not wrapped into the error: on a 201 it is
		// exactly the minted token, so echoing it back here would defeat the
		// package's no-token-in-error-text guarantee for the one response
		// that is guaranteed to actually contain one.
		return InstallationToken{}, fmt.Errorf("decode mint response: unexpected shape")
	}
	if decoded.Token == "" {
		return InstallationToken{}, fmt.Errorf("mint installation token: response carried no token")
	}
	return InstallationToken{Token: decoded.Token, ExpiresAt: decoded.ExpiresAt}, nil
}

// ResolveInstallationID looks up the installation id for a repository via
// GET /repos/{owner}/{repo}/installation, for an operator who provisioned
// owner/repo instead of a numeric installation id.
func ResolveInstallationID(client *http.Client, baseURL, jwt, owner, repo string) (string, error) {
	url := strings.TrimRight(baseURL, "/") + "/repos/" + owner + "/" + repo + "/installation"
	req, err := http.NewRequest(http.MethodGet, url, nil)
	if err != nil {
		return "", fmt.Errorf("build installation lookup request: %w", err)
	}
	setAppHeaders(req, jwt)

	resp, err := client.Do(req)
	if err != nil {
		return "", fmt.Errorf("resolve installation id: %w", redactErr(err, jwt))
	}
	defer resp.Body.Close()
	body, err := io.ReadAll(io.LimitReader(resp.Body, 1<<20))
	if err != nil {
		return "", fmt.Errorf("read installation lookup response: %w", err)
	}
	if resp.StatusCode != http.StatusOK {
		return "", fmt.Errorf("resolve installation id for %s/%s: %s", owner, repo, apiErrorMessage(resp.StatusCode, body))
	}
	var decoded struct {
		ID int64 `json:"id"`
	}
	if err := json.Unmarshal(body, &decoded); err != nil {
		return "", fmt.Errorf("decode installation lookup response: unexpected shape")
	}
	if decoded.ID == 0 {
		return "", fmt.Errorf("resolve installation id for %s/%s: response carried no id", owner, repo)
	}
	return fmt.Sprintf("%d", decoded.ID), nil
}

func setAppHeaders(req *http.Request, jwt string) {
	req.Header.Set("Authorization", "Bearer "+jwt)
	req.Header.Set("Accept", "application/vnd.github+json")
	req.Header.Set("X-GitHub-Api-Version", "2022-11-28")
}

// apiErrorMessage surfaces GitHub's own "message" field when the body
// parses as their error shape, and a bare status line otherwise — never the
// raw body, which on some failure modes (a JWT rejected as malformed) can
// echo back request fragments an operator would not expect in a log line.
func apiErrorMessage(status int, body []byte) string {
	var decoded struct {
		Message string `json:"message"`
	}
	if json.Unmarshal(body, &decoded) == nil && decoded.Message != "" {
		return fmt.Sprintf("%d %s", status, decoded.Message)
	}
	return fmt.Sprintf("unexpected status %d", status)
}

// redactErr strips jwt out of a transport error's text — net/http can fold
// request details (including headers, on some transport errors) into its
// error string, and the App JWT is a bearer credential like any other.
func redactErr(err error, jwt string) error {
	if jwt == "" {
		return err
	}
	msg := strings.ReplaceAll(err.Error(), jwt, "<redacted-jwt>")
	return fmt.Errorf("%s", msg)
}
