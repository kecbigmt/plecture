package event

import (
	"encoding/base64"
	"encoding/json"
	"fmt"
)

// CursorVersion is the current opaque-cursor format version. A token carrying a
// different version is rejected as expired: the format moved on and the token's
// fields can no longer be trusted.
const CursorVersion = 1

// Order is the listing direction for a page of events.
type Order string

const (
	OrderAsc  Order = "asc"  // oldest first; supports forward pagination via next_cursor
	OrderDesc Order = "desc" // newest first; v1 returns only the most recent page
)

// NormalizeOrder maps "" to the default (asc) and rejects unknown values.
func NormalizeOrder(s string) (Order, error) {
	switch Order(s) {
	case "", OrderAsc:
		return OrderAsc, nil
	case OrderDesc:
		return OrderDesc, nil
	default:
		return "", fmt.Errorf("invalid order %q: want asc or desc", s)
	}
}

// Cursor is the decoded form of an opaque pagination token. Clients never
// construct or interpret it — they pass a prior page's next_cursor back
// verbatim. It is a keyset position (a byte offset into the log) wrapped with
// the context needed to detect misuse: the order it was issued for and the log
// generation it came from. A raw byte offset has neither, so a stale or
// cross-order cursor would silently point at the wrong record; the wrapping
// lets the server reject it instead.
type Cursor struct {
	V   int    `json:"v"`   // format version; must equal CursorVersion
	Off int64  `json:"off"` // byte offset into the log (keyset position)
	Ord Order  `json:"ord"` // order this cursor was issued for
	Gen string `json:"gen"` // log generation id at issue time
}

// Encode renders the cursor as an opaque base64url(JSON) token.
func (c Cursor) Encode() string {
	b, _ := json.Marshal(c)
	return base64.RawURLEncoding.EncodeToString(b)
}

// DecodeCursor parses a token produced by Cursor.Encode.
func DecodeCursor(token string) (Cursor, error) {
	raw, err := base64.RawURLEncoding.DecodeString(token)
	if err != nil {
		return Cursor{}, fmt.Errorf("invalid cursor: %w", err)
	}
	var c Cursor
	if err := json.Unmarshal(raw, &c); err != nil {
		return Cursor{}, fmt.Errorf("invalid cursor: %w", err)
	}
	return c, nil
}

// Validate reports whether c may be used for a request in order ord against a
// log whose current generation is gen. A version or generation mismatch means
// the cursor predates a format change or a log rotation/compaction and must be
// discarded (restart from the beginning). An order mismatch means the caller
// asked for a different direction than the cursor was issued for.
func (c Cursor) Validate(ord Order, gen string) error {
	if c.V != CursorVersion {
		return fmt.Errorf("cursor expired (version %d, want %d); restart from the beginning", c.V, CursorVersion)
	}
	if c.Gen != gen {
		return fmt.Errorf("cursor expired (stale log generation); restart from the beginning")
	}
	if c.Ord != ord {
		return fmt.Errorf("cursor was issued for order %q but request asks for %q", c.Ord, ord)
	}
	return nil
}

// StreamCursor is the cross-session counterpart of Cursor: a keyset position for
// paging a stream that spans many session logs. A per-session byte offset cannot
// address that merged view, so the keyset is the last event id (a ULID) returned
// — lexicographically ordered = time ordered, globally unique. There is no
// generation: ordering is by id, not by any one log's byte layout, so a
// per-session rotation cannot invalidate it. Carries the stream it was issued
// for so a token cannot be replayed against a different stream.
type StreamCursor struct {
	V      int    `json:"v"`   // format version; must equal CursorVersion
	Stream string `json:"s"`   // stream id this cursor was issued for
	After  string `json:"a"`   // last event id (ULID) returned; the next page starts after it
	Ord    Order  `json:"ord"` // order this cursor was issued for
}

// Encode renders the stream cursor as an opaque base64url(JSON) token.
func (c StreamCursor) Encode() string {
	b, _ := json.Marshal(c)
	return base64.RawURLEncoding.EncodeToString(b)
}

// DecodeStreamCursor parses a token produced by StreamCursor.Encode.
func DecodeStreamCursor(token string) (StreamCursor, error) {
	raw, err := base64.RawURLEncoding.DecodeString(token)
	if err != nil {
		return StreamCursor{}, fmt.Errorf("invalid cursor: %w", err)
	}
	var c StreamCursor
	if err := json.Unmarshal(raw, &c); err != nil {
		return StreamCursor{}, fmt.Errorf("invalid cursor: %w", err)
	}
	return c, nil
}

// Validate reports whether c may be used for a request over stream in order ord.
// A version mismatch means the format moved on; a stream mismatch means the token
// was issued for a different stream (it must never cross streams); an order
// mismatch means the caller asked for a different direction than it was issued.
func (c StreamCursor) Validate(stream string, ord Order) error {
	if c.V != CursorVersion {
		return fmt.Errorf("cursor expired (version %d, want %d); restart from the beginning", c.V, CursorVersion)
	}
	if c.Stream != stream {
		return fmt.Errorf("cursor was issued for a different stream; restart from the beginning")
	}
	if c.Ord != ord {
		return fmt.Errorf("cursor was issued for order %q but request asks for %q", c.Ord, ord)
	}
	return nil
}

// SubtreeCursor is the session-tree counterpart of StreamCursor: a ULID keyset
// position for paging the events of one session subtree (a root session plus its
// descendants) merged across their logs. It carries the root it was issued for —
// not a stream id — because the canonical scope of the new judge-gate semantics
// is the session tree, with stream id kept only as a compat read model. Like
// StreamCursor it needs no generation: ordering is by id, immune to any one
// log's byte layout.
type SubtreeCursor struct {
	V     int    `json:"v"`   // format version; must equal CursorVersion
	Root  string `json:"r"`   // root session this cursor was issued for
	After string `json:"a"`   // last event id (ULID) returned; the next page starts after it
	Ord   Order  `json:"ord"` // order this cursor was issued for
}

// Encode renders the subtree cursor as an opaque base64url(JSON) token.
func (c SubtreeCursor) Encode() string {
	b, _ := json.Marshal(c)
	return base64.RawURLEncoding.EncodeToString(b)
}

// DecodeSubtreeCursor parses a token produced by SubtreeCursor.Encode.
func DecodeSubtreeCursor(token string) (SubtreeCursor, error) {
	raw, err := base64.RawURLEncoding.DecodeString(token)
	if err != nil {
		return SubtreeCursor{}, fmt.Errorf("invalid cursor: %w", err)
	}
	var c SubtreeCursor
	if err := json.Unmarshal(raw, &c); err != nil {
		return SubtreeCursor{}, fmt.Errorf("invalid cursor: %w", err)
	}
	return c, nil
}

// Validate reports whether c may be used for a request over the subtree rooted
// at root in order ord. A root mismatch means the token was issued for a
// different subtree and must never cross roots.
func (c SubtreeCursor) Validate(root string, ord Order) error {
	if c.V != CursorVersion {
		return fmt.Errorf("cursor expired (version %d, want %d); restart from the beginning", c.V, CursorVersion)
	}
	if c.Root != root {
		return fmt.Errorf("cursor was issued for a different subtree root; restart from the beginning")
	}
	if c.Ord != ord {
		return fmt.Errorf("cursor was issued for order %q but request asks for %q", c.Ord, ord)
	}
	return nil
}
