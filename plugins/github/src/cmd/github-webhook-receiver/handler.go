package main

import (
	"encoding/json"
	"io"
	"log/slog"
	"net/http"
	"sync"

	"github.com/kecbigmt/plecture/plugins/github/src/internal/pullquery"
)

// maxBodyBytes bounds one webhook delivery body; GitHub's own payload size
// limit is 25MB, but a pull_request event never approaches that, so a much
// smaller cap turns a malformed or hostile delivery into a clean 413
// instead of unbounded memory growth.
const maxBodyBytes = 5 << 20

func newHandler(secret string, in pullquery.Inputs, out io.Writer, logger *slog.Logger) http.HandlerFunc {
	var mu sync.Mutex
	return func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost {
			http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
			return
		}
		body, err := io.ReadAll(io.LimitReader(r.Body, maxBodyBytes+1))
		if err != nil {
			http.Error(w, "read body", http.StatusBadRequest)
			return
		}
		if len(body) > maxBodyBytes {
			http.Error(w, "payload too large", http.StatusRequestEntityTooLarge)
			return
		}
		if !pullquery.VerifySignature(secret, body, r.Header.Get("X-Hub-Signature-256")) {
			logger.Warn("rejected webhook delivery: signature mismatch", "delivery", r.Header.Get("X-GitHub-Delivery"))
			http.Error(w, "invalid signature", http.StatusUnauthorized)
			return
		}
		if r.Header.Get("X-GitHub-Event") != "pull_request" {
			w.WriteHeader(http.StatusNoContent)
			return
		}
		item, matched, err := pullquery.MatchPullRequestEvent(body, in)
		if err != nil {
			logger.Warn("malformed pull_request webhook payload", "error", err, "delivery", r.Header.Get("X-GitHub-Delivery"))
			http.Error(w, "malformed payload", http.StatusBadRequest)
			return
		}
		if !matched {
			w.WriteHeader(http.StatusNoContent)
			return
		}
		if err := writeItem(&mu, out, item); err != nil {
			logger.Error("write item", "error", err)
			http.Error(w, "write item", http.StatusInternalServerError)
			return
		}
		w.WriteHeader(http.StatusAccepted)
	}
}

// writeItem appends one item line under a mutex: net/http may run this
// handler concurrently for overlapping deliveries, but the subscribe
// contract is one complete JSON object per line, so two writes racing
// mid-line must never interleave.
func writeItem(mu *sync.Mutex, out io.Writer, item pullquery.Item) error {
	line, err := json.Marshal(item)
	if err != nil {
		return err
	}
	line = append(line, '\n')
	mu.Lock()
	defer mu.Unlock()
	_, err = out.Write(line)
	if f, ok := out.(interface{ Flush() error }); ok {
		if ferr := f.Flush(); err == nil {
			err = ferr
		}
	}
	return err
}
