package adapter

import (
	"encoding/json"
	"errors"
	"io/fs"
	"log/slog"
	"os"
	"path/filepath"
	"sync"
	"time"

	"github.com/kecbigmt/plecture/contracts/atomicfile"
	"github.com/slack-go/slack"
)

// maxPendingStreamChunks bounds out-of-order buffering per stream: past this,
// a stalled or dropped chunk would otherwise buffer forever.
const maxPendingStreamChunks = 32

// maxRecordedThreads bounds per-thread recipient bookkeeping; evicting an
// idle thread costs one logged fallback post at worst.
const maxRecordedThreads = 4096

// maxPendingRecipients bounds the unanswered senders one thread remembers.
const maxPendingRecipients = 16

type Streamer interface {
	StartStream(channelID, threadTS, teamID, recipientUserID, text string) (ts string, err error)
	AppendStream(channelID, ts, text string) error
	StopStream(channelID, ts, text, sessionStatus string) error
}

// StartStream seeds a streaming message; chat.startStream's undocumented
// markdown_text seeds the placeholder it creates (verified empirically).
func (a *Adapter) StartStream(channelID, threadTS, teamID, recipientUserID, text string) (string, error) {
	opts := []slack.MsgOption{slack.MsgOptionTS(threadTS)}
	if teamID != "" {
		opts = append(opts, slack.MsgOptionRecipientTeamID(teamID))
	}
	if recipientUserID != "" {
		opts = append(opts, slack.MsgOptionRecipientUserID(recipientUserID))
	}
	if text != "" {
		opts = append(opts, slack.MsgOptionMarkdownText(text))
	}
	_, ts, err := a.api.StartStream(channelID, opts...)
	return ts, err
}

func (a *Adapter) AppendStream(channelID, ts, text string) error {
	_, _, err := a.api.AppendStream(channelID, ts, slack.MsgOptionMarkdownText(text))
	return err
}

// StopStream keeps the session processing because a message boundary is not
// a turn boundary. Its markdown_text appends, so text is only the remainder.
func (a *Adapter) StopStream(channelID, ts, text, sessionStatus string) error {
	return a.sessionAPI.StopStream(channelID, ts, text, sessionStatus)
}

type streamChunk struct {
	text  string
	final bool
}

// streamState is one stream's progress, mutated only once the Slack
// (or fallback) call for a chunk has actually succeeded — see Deliver.
// failed is set only on a StartStream failure; a later Append/StopStream
// error is returned to the caller instead, since a native message exists
// by then and a fallback post alongside it would break "exactly one Slack
// thread message".
type streamState struct {
	mu        sync.Mutex
	started   bool
	failed    bool
	recipient streamRecipient
	ts        string
	nextIndex int64
	pending   map[int64]streamChunk
	text      string
	attempt   uint64
}

// streamRecipient is the Slack user a stream is addressed to, and that
// user's workspace: chat.startStream needs both to stream into a channel.
type streamRecipient struct {
	UserID string `json:"user_id"`
	TeamID string `json:"team_id"`
}

// threadRecipients: pending holds senders no stream has answered yet, oldest
// first; last keeps the previous addressee so a reply with nobody pending
// (an unprompted turn) still has one.
type threadRecipients struct {
	pending  []streamRecipient
	last     streamRecipient
	lastTurn string
	touched  uint64
}

type threadKey struct {
	channelID string
	threadTS  string
}

type streamIdentity struct {
	channelID string
	threadTS  string
	streamKey string
}

const streamSnapshotVersion = 1

type persistedStreamChunk struct {
	Text  string `json:"text"`
	Final bool   `json:"final"`
}

type persistedStream struct {
	ChannelID string                         `json:"channel_id"`
	ThreadTS  string                         `json:"thread_ts"`
	StreamKey string                         `json:"stream_key"`
	Started   bool                           `json:"started"`
	Failed    bool                           `json:"failed"`
	Recipient streamRecipient                `json:"recipient"`
	TS        string                         `json:"ts"`
	NextIndex int64                          `json:"next_index"`
	Pending   map[int64]persistedStreamChunk `json:"pending,omitempty"`
	Text      string                         `json:"text"`
}

type persistedFinalizedStream struct {
	ChannelID   string    `json:"channel_id"`
	ThreadTS    string    `json:"thread_ts"`
	StreamKey   string    `json:"stream_key"`
	FinalizedAt time.Time `json:"finalized_at"`
}

type persistedStreamManagerState struct {
	Version   int                        `json:"version"`
	Streams   []persistedStream          `json:"streams,omitempty"`
	Finalized []persistedFinalizedStream `json:"finalized,omitempty"`
}

// finalizedRetention bounds stateOrFinalized's map to a duplicate's
// realistic lag behind its original, not a session's whole lifetime.
const finalizedRetention = 10 * time.Minute

type StreamManager struct {
	streamer Streamer
	poster   ThreadPoster
	logger   *slog.Logger

	mu        sync.Mutex
	state     map[streamIdentity]*streamState
	finalized map[streamIdentity]time.Time
	threads   map[threadKey]*threadRecipients
	threadSeq uint64
	statePath string
	persistMu sync.Mutex
}

func NewStreamManager(streamer Streamer, poster ThreadPoster, logger *slog.Logger) *StreamManager {
	return NewStreamManagerWithStatePath(streamer, poster, logger, "")
}

func NewStreamManagerWithStatePath(streamer Streamer, poster ThreadPoster, logger *slog.Logger, statePath string) *StreamManager {
	m := &StreamManager{
		streamer:  streamer,
		poster:    poster,
		logger:    logger,
		state:     make(map[streamIdentity]*streamState),
		finalized: make(map[streamIdentity]time.Time),
		threads:   make(map[threadKey]*threadRecipients),
		statePath: statePath,
	}
	if statePath != "" {
		m.load()
	}
	return m
}

// RecordRecipient queues the sender of an accepted inbound message as the
// addressee of one of the thread's next streams. The recipient is a property
// of the conversation, not of the access-control list: several people may be
// allowed to talk to a session, and only the one being answered can be named.
// A burst from one sender is one entry, since an agent may answer it in one turn.
func (m *StreamManager) RecordRecipient(channelID, threadTS, userID, teamID string) {
	if channelID == "" || threadTS == "" {
		return
	}
	r := streamRecipient{UserID: userID, TeamID: teamID}
	m.mu.Lock()
	defer m.mu.Unlock()
	t := m.threadLocked(threadKey{channelID, threadTS})
	if n := len(t.pending); n > 0 && t.pending[n-1] == r {
		return
	}
	t.pending = append(t.pending, r)
	if len(t.pending) > maxPendingRecipients {
		t.pending = t.pending[1:]
	}
}

func (m *StreamManager) threadLocked(k threadKey) *threadRecipients {
	m.threadSeq++
	t, ok := m.threads[k]
	if !ok {
		t = &threadRecipients{}
		m.threads[k] = t
	}
	t.touched = m.threadSeq
	if len(m.threads) > maxRecordedThreads {
		var oldest threadKey
		var oldestSeq uint64
		for key, other := range m.threads {
			if key != k && (oldestSeq == 0 || other.touched < oldestSeq) {
				oldest, oldestSeq = key, other.touched
			}
		}
		delete(m.threads, oldest)
	}
	return t
}

// claimRecipientLocked binds a new stream to the oldest unanswered sender, so
// a message arriving before this reply's first chunk cannot take it over.
// Streams of one turn share that sender; turns are told apart by the
// harness's turn id when it supplies one, else by stream key.
func (m *StreamManager) claimRecipientLocked(id streamIdentity, turnID string) streamRecipient {
	t := m.threadLocked(threadKey{id.channelID, id.threadTS})
	turn := turnID
	if turn == "" {
		turn = "stream:" + id.streamKey
	}
	if turn == t.lastTurn {
		return t.last
	}
	t.lastTurn = turn
	if len(t.pending) > 0 {
		t.last = t.pending[0]
		t.pending = t.pending[1:]
	}
	return t.last
}

// Deliver processes one chunk for streamKey, ordered by index. Draining
// stops at the first failure, leaving that chunk (and anything after it)
// pending instead of skipped, so a caller's retry of the same index
// re-attempts it rather than the stream silently completing short.
func (m *StreamManager) Deliver(channelID, threadTS, streamKey, turnID string, index int64, text string, final bool) error {
	return m.deliver(channelID, threadTS, streamKey, turnID, index, text, final, false, "processing")
}

func (m *StreamManager) DeliverLate(channelID, threadTS, streamKey, turnID string, index int64, text string, final bool, currentStatus string) error {
	return m.deliver(channelID, threadTS, streamKey, turnID, index, text, final, true, currentStatus)
}

func (m *StreamManager) deliver(channelID, threadTS, streamKey, turnID string, index int64, text string, final, late bool, currentStatus string) error {
	id := streamIdentity{channelID: channelID, threadTS: threadTS, streamKey: streamKey}
	st, alreadyFinalized := m.stateOrFinalized(id, turnID)
	if alreadyFinalized {
		m.logger.Info("stream delivery dropped: stream already finalized",
			"component", "slack-adapter", "event", "stream_deliver_duplicate",
			"channel_id", channelID, "thread_ts", threadTS, "stream_key", streamKey)
		return nil
	}

	st.mu.Lock()
	if late && !st.started {
		st.failed = true
	}
	// A lower index is a duplicate of an already-applied chunk, not a retry.
	if index >= st.nextIndex {
		st.pending[index] = streamChunk{text: text, final: final}
	}

	for {
		idx, c, ok := nextChunk(st)
		if !ok {
			st.mu.Unlock()
			m.persist()
			return nil
		}
		st.attempt++
		if err := m.apply(id, st, c, currentStatus); err != nil {
			m.logger.Warn("stream delivery failed, will retry on redelivery",
				"component", "slack-adapter", "event", "stream_deliver_error",
				"channel_id", channelID, "thread_ts", threadTS, "turn_id", turnID,
				"stream_key", streamKey, "index", idx, "attempt", st.attempt,
				"operation", streamOperation(st, c), "error", err)
			st.mu.Unlock()
			m.persist()
			return err
		}
		delete(st.pending, idx)
		st.nextIndex = idx + 1
		if c.final {
			m.logger.Info("stream message delivered",
				"component", "slack-adapter", "event", "stream_deliver_final",
				"channel_id", channelID, "thread_ts", threadTS, "turn_id", turnID,
				"stream_key", streamKey, "index", idx, "attempt", st.attempt)
			m.forget(id)
			st.mu.Unlock()
			m.persist()
			return nil
		}
	}
}

func streamOperation(st *streamState, c streamChunk) string {
	if st.failed {
		return "chat.postMessage"
	}
	if !st.started {
		return "chat.startStream"
	}
	if c.final {
		return "chat.stopStream"
	}
	return "chat.appendStream"
}

// stateOrFinalized combines both checks under one lock: split into two, a
// concurrent forget could land between them and let a duplicate through.
func (m *StreamManager) stateOrFinalized(id streamIdentity, turnID string) (st *streamState, alreadyFinalized bool) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if t, ok := m.finalized[id]; ok {
		if time.Since(t) <= finalizedRetention {
			return nil, true
		}
		delete(m.finalized, id)
	}
	st, ok := m.state[id]
	if !ok {
		// Claimed now rather than at start: a message that arrives while
		// this stream's first chunks are still buffered must not redirect it.
		st = &streamState{
			pending:   make(map[int64]streamChunk),
			recipient: m.claimRecipientLocked(id, turnID),
		}
		m.state[id] = st
	}
	return st, false
}

// forget also records the stream identity as finalized, guarding against a duplicate.
func (m *StreamManager) forget(id streamIdentity) {
	now := time.Now()
	m.mu.Lock()
	delete(m.state, id)
	m.finalized[id] = now
	m.pruneFinalizedLocked(now)
	m.mu.Unlock()
}

func (m *StreamManager) load() {
	data, err := os.ReadFile(m.statePath)
	if errors.Is(err, fs.ErrNotExist) {
		m.logger.Info("stream state missing, starting empty", "path", m.statePath)
		return
	}
	if err != nil {
		m.logger.Warn("stream state unreadable, starting empty", "path", m.statePath, "error", err)
		return
	}
	var persisted persistedStreamManagerState
	if err := json.Unmarshal(data, &persisted); err != nil {
		m.logger.Warn("stream state invalid, starting empty", "path", m.statePath, "error", err)
		return
	}
	if persisted.Version != streamSnapshotVersion {
		m.logger.Warn("stream state version unsupported, starting empty", "path", m.statePath, "version", persisted.Version)
		return
	}
	for _, saved := range persisted.Streams {
		id, ok := streamIdentityFromPersisted(saved.ChannelID, saved.ThreadTS, saved.StreamKey)
		if !ok {
			continue
		}
		pending := make(map[int64]streamChunk, len(saved.Pending))
		for index, chunk := range saved.Pending {
			pending[index] = streamChunk{text: chunk.Text, final: chunk.Final}
		}
		m.state[id] = &streamState{
			started:   saved.Started,
			failed:    saved.Failed,
			recipient: saved.Recipient,
			ts:        saved.TS,
			nextIndex: saved.NextIndex,
			pending:   pending,
			text:      saved.Text,
		}
	}
	for _, saved := range persisted.Finalized {
		id, ok := streamIdentityFromPersisted(saved.ChannelID, saved.ThreadTS, saved.StreamKey)
		if !ok || saved.FinalizedAt.IsZero() {
			continue
		}
		m.finalized[id] = saved.FinalizedAt
		delete(m.state, id)
	}
	m.pruneFinalizedLocked(time.Now())
}

func streamIdentityFromPersisted(channelID, threadTS, streamKey string) (streamIdentity, bool) {
	if channelID == "" || threadTS == "" || streamKey == "" {
		return streamIdentity{}, false
	}
	return streamIdentity{channelID: channelID, threadTS: threadTS, streamKey: streamKey}, true
}

func (m *StreamManager) persist() {
	if m.statePath == "" {
		return
	}
	m.persistMu.Lock()
	defer m.persistMu.Unlock()

	m.mu.Lock()
	m.pruneFinalizedLocked(time.Now())
	states := make(map[streamIdentity]*streamState, len(m.state))
	for id, st := range m.state {
		states[id] = st
	}
	finalized := make(map[streamIdentity]time.Time, len(m.finalized))
	for id, at := range m.finalized {
		finalized[id] = at
	}
	m.mu.Unlock()

	persisted := persistedStreamManagerState{
		Version:   streamSnapshotVersion,
		Streams:   make([]persistedStream, 0, len(states)),
		Finalized: make([]persistedFinalizedStream, 0, len(finalized)),
	}
	for id, st := range states {
		st.mu.Lock()
		pending := make(map[int64]persistedStreamChunk, len(st.pending))
		for index, chunk := range st.pending {
			pending[index] = persistedStreamChunk{Text: chunk.text, Final: chunk.final}
		}
		persisted.Streams = append(persisted.Streams, persistedStream{
			ChannelID: id.channelID, ThreadTS: id.threadTS, StreamKey: id.streamKey,
			Started: st.started, Failed: st.failed, Recipient: st.recipient, TS: st.ts, NextIndex: st.nextIndex,
			Pending: pending, Text: st.text,
		})
		st.mu.Unlock()
	}
	for id, at := range finalized {
		persisted.Finalized = append(persisted.Finalized, persistedFinalizedStream{
			ChannelID: id.channelID, ThreadTS: id.threadTS, StreamKey: id.streamKey, FinalizedAt: at,
		})
	}

	dir := filepath.Dir(m.statePath)
	if err := os.MkdirAll(dir, 0o700); err != nil {
		m.logger.Warn("failed to create stream state directory", "path", dir, "error", err)
		return
	}
	data, err := json.MarshalIndent(persisted, "", "  ")
	if err != nil {
		m.logger.Warn("failed to encode stream state", "error", err)
		return
	}
	if err := atomicfile.Write(m.statePath, append(data, '\n')); err != nil {
		m.logger.Warn("failed to persist stream state", "path", m.statePath, "error", err)
	}
}

func (m *StreamManager) pruneFinalizedLocked(now time.Time) {
	cutoff := now.Add(-finalizedRetention)
	for k, t := range m.finalized {
		if t.Before(cutoff) {
			delete(m.finalized, k)
		}
	}
}

// nextChunk selects the next candidate for apply: the chunk at
// st.nextIndex, or, once the gap ahead of it has stalled past
// maxPendingStreamChunks, the lowest buffered index instead. It never
// mutates st; Deliver does that only after a successful apply.
func nextChunk(st *streamState) (int64, streamChunk, bool) {
	if c, ok := st.pending[st.nextIndex]; ok {
		return st.nextIndex, c, true
	}
	if len(st.pending) < maxPendingStreamChunks {
		return 0, streamChunk{}, false
	}
	lowest := int64(-1)
	for idx := range st.pending {
		if lowest == -1 || idx < lowest {
			lowest = idx
		}
	}
	return lowest, st.pending[lowest], true
}

func (m *StreamManager) apply(id streamIdentity, st *streamState, c streamChunk, currentStatus string) error {
	channelID, threadTS := id.channelID, id.threadTS
	if st.failed {
		return m.applyFallback(channelID, threadTS, st, c)
	}

	if !st.started {
		if reason := missingRecipientReason(st.recipient); reason != "" {
			m.logger.Warn("stream start skipped, falling back to one post",
				"component", "slack-adapter", "event", "stream_start_skipped",
				"reason", reason,
				"channel_id", channelID, "thread_ts", threadTS, "stream_key", id.streamKey)
			st.failed = true
			return m.applyFallback(channelID, threadTS, st, c)
		}
		ts, err := m.streamer.StartStream(channelID, threadTS, st.recipient.TeamID, st.recipient.UserID, c.text)
		if err != nil {
			m.logger.Warn("stream start failed, falling back to one post",
				"component", "slack-adapter", "event", "stream_start_failed",
				"reason", "start_failed",
				"channel_id", channelID, "thread_ts", threadTS, "stream_key", id.streamKey,
				"error", err)
			st.failed = true
			return m.applyFallback(channelID, threadTS, st, c)
		}
		st.started = true
		st.ts = ts
		if !c.final {
			return nil
		}
		// The seed text above already carries this chunk (see StopStream).
		return m.streamer.StopStream(channelID, ts, "", currentStatus)
	}

	if c.final {
		return m.streamer.StopStream(channelID, st.ts, c.text, currentStatus)
	}
	return m.streamer.AppendStream(channelID, st.ts, c.text)
}

func missingRecipientReason(r streamRecipient) string {
	switch {
	case r.UserID == "":
		return "recipient_unknown"
	case r.TeamID == "":
		return "recipient_team_unknown"
	}
	return ""
}

// applyFallback buffers c into the fallback text and, on final, posts it
// once. The merge is a local value until PostToThread actually succeeds:
// a failed post leaves st.text unchanged, so retrying the same final chunk
// recomputes the identical merge instead of appending c.text a second time.
func (m *StreamManager) applyFallback(channelID, threadTS string, st *streamState, c streamChunk) error {
	merged := st.text + c.text
	if !c.final {
		st.text = merged
		return nil
	}
	if _, err := m.poster.PostToThread(channelID, threadTS, merged); err != nil {
		return err
	}
	st.text = merged
	return nil
}
