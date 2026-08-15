package ratebudget

import (
	"bytes"
	"strconv"
	"strings"
	"time"
)

// HTTPResponse is a `gh api -i` response, split into status/headers/body.
// Shared by every caller that needs the real Retry-After/X-RateLimit-Reset
// headers to report an accurate throttle to Guard, instead of falling back
// to the exponential default.
type HTTPResponse struct {
	Status  int
	Headers map[string]string // lower-cased keys
	Body    []byte
}

// ParseHTTPResponse parses `gh api -i` output: a status line, headers, a
// blank line, then the (possibly jq-projected) body. Returns ok=false when
// out doesn't start with a parseable HTTP status line (e.g. gh failed to
// start at all).
func ParseHTTPResponse(out []byte) (*HTTPResponse, bool) {
	// gh's own header block is CRLF-terminated but the leading status line is
	// bare-LF (observed live against api.github.com), so the blank-line
	// separator can be either "\r\n\r\n" or "\n\n" — whichever occurs first.
	headerBlock, body := out, []byte(nil)
	sepCRLF, sepLF := bytes.Index(out, []byte("\r\n\r\n")), bytes.Index(out, []byte("\n\n"))
	switch {
	case sepCRLF == -1 && sepLF == -1:
		// no blank line at all: treat everything as headers, empty body.
	case sepLF == -1 || (sepCRLF != -1 && sepCRLF <= sepLF):
		headerBlock, body = out[:sepCRLF], out[sepCRLF+4:]
	default:
		headerBlock, body = out[:sepLF], out[sepLF+2:]
	}
	lines := strings.Split(string(headerBlock), "\n")
	if len(lines) == 0 {
		return nil, false
	}
	statusLine := strings.TrimRight(strings.TrimSpace(lines[0]), "\r")
	parts := strings.SplitN(statusLine, " ", 3)
	if len(parts) < 2 || !strings.HasPrefix(parts[0], "HTTP/") {
		return nil, false
	}
	status, err := strconv.Atoi(parts[1])
	if err != nil {
		return nil, false
	}
	headers := map[string]string{}
	for _, line := range lines[1:] {
		line = strings.TrimRight(line, "\r")
		key, value, found := strings.Cut(line, ":")
		if !found {
			continue
		}
		headers[strings.ToLower(strings.TrimSpace(key))] = strings.TrimSpace(value)
	}
	return &HTTPResponse{Status: status, Headers: headers, Body: bytes.TrimSpace(body)}, true
}

// RetryAfterSeconds parses a Retry-After header value (seconds form; GitHub
// does not send the HTTP-date form for API rate limiting). An empty or
// unparsable value returns 0, telling the caller to fall back to the guard's
// exponential backoff.
func RetryAfterSeconds(v string) time.Duration {
	if v == "" {
		return 0
	}
	n, err := strconv.Atoi(v)
	if err != nil || n <= 0 {
		return 0
	}
	return time.Duration(n) * time.Second
}

// RateLimitReset parses an X-RateLimit-Reset header value (Unix seconds).
func RateLimitReset(v string) time.Time {
	if v == "" {
		return time.Time{}
	}
	n, err := strconv.ParseInt(v, 10, 64)
	if err != nil {
		return time.Time{}
	}
	return time.Unix(n, 0)
}
