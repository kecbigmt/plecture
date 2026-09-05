package adapter

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
)

// RunSubscribeUnboundMentions connects to the resident adapter's
// /unbound-mentions feed at baseURL and writes one query.subscribe item per
// line to out for each mention in a channel named by channelIDs. An empty
// channelIDs matches every channel.
//
// It returns nil only when ctx itself ends the connection — the process was
// asked to stop. Any other termination (the initial connect failing, the
// resident restarting mid-stream, or the stream otherwise ending) is a
// source failure and returns a non-nil error, so the caller's exit code
// tells the exec supervisor to restart it rather than treating quiet as
// absence — see docs/adr/2026-09-05-standing-session-dispatch.md's
// query.subscribe contract ("Quiet, failure, or restart never implies
// absence").
func RunSubscribeUnboundMentions(ctx context.Context, baseURL string, channelIDs []string, out io.Writer) error {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, strings.TrimRight(baseURL, "/")+"/unbound-mentions", nil)
	if err != nil {
		return fmt.Errorf("build request: %w", err)
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		if ctx.Err() != nil {
			return nil
		}
		return fmt.Errorf("connect to %s: %w", baseURL, err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("unbound-mentions stream returned %s", resp.Status)
	}

	dec := json.NewDecoder(resp.Body)
	for {
		var item unboundMentionItem
		if err := dec.Decode(&item); err != nil {
			if ctx.Err() != nil {
				return nil
			}
			return fmt.Errorf("unbound-mentions stream ended: %w", err)
		}
		if !channelAllowed(channelIDs, item.ChannelID) {
			continue
		}
		line, err := json.Marshal(item)
		if err != nil {
			return fmt.Errorf("encode item: %w", err)
		}
		if _, err := out.Write(append(line, '\n')); err != nil {
			return fmt.Errorf("write item: %w", err)
		}
	}
}

func channelAllowed(allowed []string, channelID string) bool {
	if len(allowed) == 0 {
		return true
	}
	for _, id := range allowed {
		if id == channelID {
			return true
		}
	}
	return false
}
