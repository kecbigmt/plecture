package adapter

import (
	"fmt"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/slack-go/slack"
	"github.com/slack-go/slack/slackevents"
)

var slackTimestampShape = regexp.MustCompile(`^\d{10}\.\d{6}$`)

func isSlackTimestampShape(ts string) bool {
	return slackTimestampShape.MatchString(ts)
}

type threadFetcher interface {
	fetchThreadReplies(channelID, threadTS string) ([]slack.Message, error)
	userDisplayName(userID string) string
}

// cachedNames memoizes display names for one transcript only: a long thread
// otherwise costs one Slack API call per message while the Socket Mode event
// loop waits, and a cache that outlived the event would serve renamed users'
// stale names.
type cachedNames struct {
	threadFetcher
	names map[string]string
}

func withCachedNames(fetcher threadFetcher) *cachedNames {
	return &cachedNames{threadFetcher: fetcher, names: make(map[string]string)}
}

func (c *cachedNames) userDisplayName(userID string) string {
	if name, ok := c.names[userID]; ok {
		return name
	}
	name := c.threadFetcher.userDisplayName(userID)
	c.names[userID] = name
	return name
}

// maxTranscriptBytes bounds one event body sent over the service protocol.
const maxTranscriptBytes = 64 << 10

// fitTranscript drops the oldest replies rather than the root or the
// mention, because those carry the thread's subject and the request itself.
// When those alone still exceed the budget, they share it evenly instead of
// the first one cut losing its text entirely.
func fitTranscript(lines []string, keepHead, keepTail int) (string, int) {
	size := transcriptSize(lines)
	if size <= maxTranscriptBytes {
		return strings.Join(lines, "\n"), 0
	}
	middle := len(lines) - keepHead - keepTail
	omitted := 0
	for omitted < middle && size+len(omissionMarker(omitted))+1 > maxTranscriptBytes {
		size -= len(lines[keepHead+omitted]) + 1
		omitted++
	}
	kept := make([]string, 0, len(lines)-omitted+1)
	kept = append(kept, lines[:keepHead]...)
	if omitted > 0 {
		kept = append(kept, omissionMarker(omitted))
	}
	kept = append(kept, lines[keepHead+omitted:]...)
	if transcriptSize(kept) > maxTranscriptBytes {
		shareBudget(kept, keepHead, keepTail)
	}
	return strings.Join(kept, "\n"), omitted
}

// shareBudget truncates the first keepHead and last keepTail lines in place,
// shortest first, so a line under its share leaves the rest to longer ones.
func shareBudget(kept []string, keepHead, keepTail int) {
	retained := make([]int, 0, keepHead+keepTail)
	for i := 0; i < keepHead; i++ {
		retained = append(retained, i)
	}
	for i := len(kept) - keepTail; i < len(kept); i++ {
		retained = append(retained, i)
	}
	sort.SliceStable(retained, func(x, y int) bool { return len(kept[retained[x]]) < len(kept[retained[y]]) })
	remaining := maxTranscriptBytes - transcriptSize(kept)
	for _, i := range retained {
		remaining += len(kept[i])
	}
	for n, i := range retained {
		kept[i] = truncateLine(kept[i], remaining/(len(retained)-n))
		remaining -= len(kept[i])
	}
}

func transcriptSize(lines []string) int {
	size := max(len(lines)-1, 0)
	for _, line := range lines {
		size += len(line)
	}
	return size
}

const truncationMarker = " [truncated]"

func truncateLine(line string, maxBytes int) string {
	if len(line) <= maxBytes {
		return line
	}
	cut := max(maxBytes-len(truncationMarker), 0)
	for cut > 0 && !utf8.RuneStart(line[cut]) {
		cut--
	}
	return line[:cut] + truncationMarker
}

func omissionMarker(omitted int) string {
	return fmt.Sprintf("[%d earlier messages omitted]", omitted)
}

func buildDeliberationTranscript(fetcher threadFetcher, replies []slack.Message, mention slackevents.AppMentionEvent, threadTS string) (string, string, int) {
	messages := selectDeliberationMessages(replies, threadTS, mention.TimeStamp)
	lines := make([]string, 0, len(messages)+1)
	deliveredThrough := mention.TimeStamp
	for _, msg := range messages {
		if msg.Timestamp != "" && compareSlackTS(msg.Timestamp, deliveredThrough) > 0 {
			deliveredThrough = msg.Timestamp
		}
		lines = append(lines, formatTranscriptLine(msg.Timestamp, displayNameForMessage(fetcher, msg), msg.Text))
	}
	mentionUser := fetcher.userDisplayName(mention.User)
	if mentionUser == "" {
		mentionUser = mention.User
	}
	lines = append(lines, formatTranscriptLine(mention.TimeStamp, mentionUser, mention.Text))
	body, omitted := fitTranscript(lines, rootLines(messages, threadTS), 1)
	return body, deliveredThrough, omitted
}

func rootLines(messages []slack.Message, threadTS string) int {
	if len(messages) > 0 && messages[0].Timestamp == threadTS {
		return 1
	}
	return 0
}

func sortedReplies(replies []slack.Message) []slack.Message {
	sorted := append([]slack.Message(nil), replies...)
	sort.SliceStable(sorted, func(i, j int) bool {
		return compareSlackTS(sorted[i].Timestamp, sorted[j].Timestamp) < 0
	})
	return sorted
}

func selectDeliberationMessages(replies []slack.Message, threadTS, mentionTS string) []slack.Message {
	sorted := sortedReplies(replies)
	out := make([]slack.Message, 0, len(sorted))
	for _, msg := range sorted {
		if msg.Timestamp == "" || !renderableMessage(msg) {
			continue
		}
		isRoot := msg.Timestamp == threadTS
		switch {
		case isRoot && msg.Timestamp == mentionTS:
			continue
		case isRoot:
			out = append(out, msg)
		case mentionTS != "" && compareSlackTS(msg.Timestamp, mentionTS) >= 0:
			continue
		default:
			out = append(out, msg)
		}
	}
	return out
}

func renderableMessage(msg slack.Message) bool {
	if msg.Hidden || msg.DeletedTimestamp != "" {
		return false
	}
	switch msg.SubType {
	case "", slack.MsgSubTypeBotMessage:
		return true
	default:
		return false
	}
}

func displayNameForMessage(fetcher threadFetcher, msg slack.Message) string {
	if msg.User != "" {
		if name := fetcher.userDisplayName(msg.User); name != "" {
			return name
		}
		return msg.User
	}
	if msg.BotProfile != nil && msg.BotProfile.Name != "" {
		return msg.BotProfile.Name
	}
	if msg.Username != "" {
		return msg.Username
	}
	if msg.BotID != "" {
		return msg.BotID
	}
	return "unknown"
}

func formatTranscriptLine(ts, author, text string) string {
	return fmt.Sprintf("[%s] %s: %s", formatSlackTimestamp(ts), author, text)
}

func formatSlackTimestamp(ts string) string {
	sec, nsec, ok := parseSlackTimestamp(ts)
	if !ok {
		return ts
	}
	return time.Unix(sec, nsec).UTC().Format(time.RFC3339Nano)
}

func compareSlackTS(a, b string) int {
	asec, afrac := splitSlackTimestamp(a)
	bsec, bfrac := splitSlackTimestamp(b)
	if len(asec) != len(bsec) {
		if len(asec) < len(bsec) {
			return -1
		}
		return 1
	}
	if asec != bsec {
		if asec < bsec {
			return -1
		}
		return 1
	}
	maxFracLen := max(len(afrac), len(bfrac))
	afrac = padRight(afrac, maxFracLen)
	bfrac = padRight(bfrac, maxFracLen)
	if afrac == bfrac {
		return 0
	}
	if afrac < bfrac {
		return -1
	}
	return 1
}

func parseSlackTimestamp(ts string) (int64, int64, bool) {
	secPart, fracPart := splitSlackTimestamp(ts)
	sec, err := strconv.ParseInt(secPart, 10, 64)
	if err != nil {
		return 0, 0, false
	}
	fracPart = padRight(fracPart, 9)
	if len(fracPart) > 9 {
		fracPart = fracPart[:9]
	}
	nsec, err := strconv.ParseInt(fracPart, 10, 64)
	if err != nil {
		return 0, 0, false
	}
	return sec, nsec, true
}

func splitSlackTimestamp(ts string) (string, string) {
	sec, frac, ok := strings.Cut(ts, ".")
	if !ok {
		return ts, ""
	}
	return sec, frac
}

func padRight(s string, length int) string {
	if len(s) >= length {
		return s
	}
	return s + strings.Repeat("0", length-len(s))
}
