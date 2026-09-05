package adapter

import "sync"

// unboundMentionItem deliberately carries only identity and appearance
// context, not live thread state: mixing the two would leave two paths
// able to disagree about what a thread's current state is.
type unboundMentionItem struct {
	Resource  string `json:"resource"`
	ChannelID string `json:"channel_id"`
	ThreadTS  string `json:"thread_ts"`
	MentionTS string `json:"mention_ts"`
}

// mentionStreamBuffer bounds how far a slow /unbound-mentions reader can lag
// before publish starts dropping items for it. A query.subscribe appearance
// is best-effort by contract — a dropped item is not proof the mention never
// happened — so this trades a slow reader's completeness for keeping the
// Slack event-handling goroutine from ever blocking on a reader.
const mentionStreamBuffer = 16

// mentionStream fans one unbound app mention out to every connected
// /unbound-mentions reader. It holds nothing on disk: a reader that was not
// connected when a mention occurred has no way to recover it, since there
// is no complete-membership snapshot for a mention to belong to — only the
// moment it happened.
type mentionStream struct {
	mu   sync.Mutex
	subs map[chan unboundMentionItem]struct{}
}

func newMentionStream() *mentionStream {
	return &mentionStream{subs: make(map[chan unboundMentionItem]struct{})}
}

// subscribe registers a new reader and returns its channel plus a cancel
// func the caller must run (typically via defer) to unregister it.
func (s *mentionStream) subscribe() (<-chan unboundMentionItem, func()) {
	ch := make(chan unboundMentionItem, mentionStreamBuffer)
	s.mu.Lock()
	s.subs[ch] = struct{}{}
	s.mu.Unlock()
	return ch, func() {
		s.mu.Lock()
		delete(s.subs, ch)
		s.mu.Unlock()
	}
}

// hasSubscribers lets dispatchUnboundMention skip permalink resolution (a
// Slack API call) when nothing would receive the result anyway.
func (s *mentionStream) hasSubscribers() bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	return len(s.subs) > 0
}

// publish drops the item for any reader whose buffer is full rather than
// blocking: see mentionStreamBuffer for why.
func (s *mentionStream) publish(item unboundMentionItem) {
	s.mu.Lock()
	defer s.mu.Unlock()
	for ch := range s.subs {
		select {
		case ch <- item:
		default:
		}
	}
}
