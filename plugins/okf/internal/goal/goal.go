// Package goal parses and finalizes a local-okf goal Concept file: YAML
// frontmatter plus a "## Done When" GFM checklist. It reads the file's
// content as data — nothing here ever evaluates the file's bytes as code
// (the goal file is authored by whoever owns the knowledge bundle, not by
// this plugin, so it is untrusted input).
package goal

import (
	"bufio"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"os"
	"regexp"
	"strings"
	"time"

	"gopkg.in/yaml.v3"
)

// Status values a goal's frontmatter may declare.
const (
	StatusOpen      = "open"
	StatusBlocked   = "blocked"
	StatusCompleted = "completed"
	StatusArchived  = "archived"
)

// Checklist outcomes for the "## Done When" section.
const (
	ChecklistSuccess = "SUCCESS"
	ChecklistPending = "PENDING"
)

// Goal is one successfully parsed goal Concept file.
type Goal struct {
	// Status is the frontmatter's validated status.
	Status string
	// ChecklistStatus is SUCCESS when every "## Done When" item is
	// checked, PENDING when at least one is open.
	ChecklistStatus string
	// OpenItems lists the text of every unchecked item, semicolon-joined,
	// in file order.
	OpenItems string
	// Revision is a content hash of the file's bytes: "sha256:<hex>". It
	// changes exactly when judged content changes, and survives copies or
	// checkouts that reset mtimes.
	Revision string
}

// ParseError reports why a goal file could not be parsed. Status carries
// the frontmatter's own status value once it has been validated — so a
// checklist-stage failure still reports the goal's real status — and stays
// empty for a failure discovered before status validation.
type ParseError struct {
	Status string
	// Revision is set whenever the file could be read at all: content
	// hashing happens before any structural validation, so even a
	// malformed-frontmatter failure still reports the revision the
	// malformed content hashes to.
	Revision string
	Reason   string
}

func (e *ParseError) Error() string { return e.Reason }

var doneWhenHeading = regexp.MustCompile(`^##[ \t]+Done When[ \t]*$`)
var anyHeading = regexp.MustCompile(`^#{1,6}[ \t]`)

// Parse reads and validates a goal Concept file: YAML frontmatter declaring
// `type: Goal` and a `status`, followed by a body containing a "## Done
// When" GFM checklist with at least one item.
func Parse(path string) (*Goal, *ParseError) {
	content, err := os.ReadFile(path)
	if err != nil {
		return nil, &ParseError{Reason: err.Error()}
	}
	revision := Revision(content)
	lines := strings.Split(string(content), "\n")

	if len(lines) == 0 || lines[0] != "---" {
		return nil, &ParseError{Revision: revision, Reason: "missing YAML frontmatter (file must start with ---)"}
	}

	closeIdx := -1
	for i := 1; i < len(lines); i++ {
		if lines[i] == "---" {
			closeIdx = i
			break
		}
	}
	if closeIdx == -1 {
		return nil, &ParseError{Revision: revision, Reason: "unterminated YAML frontmatter (no closing ---)"}
	}

	fm := parseFrontmatterBlock(lines[1:closeIdx])
	body := lines[closeIdx+1:]

	fmType, _ := fm["type"].(string)
	if fmType != "Goal" {
		return nil, &ParseError{Revision: revision, Reason: fmt.Sprintf("frontmatter type is %q, want \"Goal\"", fmType)}
	}

	status, _ := fm["status"].(string)
	switch status {
	case StatusOpen, StatusBlocked, StatusCompleted, StatusArchived:
	default:
		return nil, &ParseError{Revision: revision, Reason: fmt.Sprintf("frontmatter status is %q, want open|blocked|completed|archived", status)}
	}

	headingIdx := -1
	for i, line := range body {
		if doneWhenHeading.MatchString(line) {
			headingIdx = i
			break
		}
	}
	if headingIdx == -1 {
		return nil, &ParseError{Status: status, Revision: revision, Reason: `missing "## Done When" section`}
	}

	var section []string
	for _, line := range body[headingIdx+1:] {
		if anyHeading.MatchString(line) {
			break
		}
		section = append(section, line)
	}

	itemCount := 0
	var openItems []string
	var malformed string
	for _, line := range section {
		trimmed := strings.TrimSpace(line)
		if trimmed == "" {
			continue
		}
		switch {
		case strings.HasPrefix(trimmed, "- [ ]"), strings.HasPrefix(trimmed, "- [x]"), strings.HasPrefix(trimmed, "- [X]"):
			itemCount++
			mark := trimmed[3]
			// A bare item like "- [ ]" with no trailing text is shorter
			// than the fixed "- [ ] " prefix a text slice would start
			// after — cut -c7- on such a line returns empty in the shell
			// this was ported from, so an out-of-range slice here must
			// mean "no text" too, not a panic.
			var text string
			if len(trimmed) > 6 {
				text = strings.TrimLeft(trimmed[6:], " \t")
			}
			if mark == ' ' {
				openItems = append(openItems, text)
			}
		case strings.HasPrefix(trimmed, "- ["):
			malformed = trimmed
		}
	}

	if malformed != "" {
		return nil, &ParseError{Status: status, Revision: revision, Reason: fmt.Sprintf("malformed checklist item: %q", malformed)}
	}
	if itemCount == 0 {
		return nil, &ParseError{Status: status, Revision: revision, Reason: `"## Done When" section has no checklist items`}
	}

	checklistStatus := ChecklistSuccess
	if len(openItems) > 0 {
		checklistStatus = ChecklistPending
	}

	return &Goal{
		Status:          status,
		ChecklistStatus: checklistStatus,
		OpenItems:       strings.Join(openItems, "; "),
		Revision:        revision,
	}, nil
}

// Revision computes the content-hash revision Parse and Finalize agree on.
func Revision(content []byte) string {
	sum := sha256.Sum256(content)
	return "sha256:" + hex.EncodeToString(sum[:])
}

// parseFrontmatterBlock parses a frontmatter block permissively: content
// that fails to parse as YAML yields an empty map rather than an error, so
// callers see the same "missing/invalid field" failures a strict parse
// would report field-by-field, instead of one opaque YAML error.
func parseFrontmatterBlock(lines []string) map[string]any {
	var fm map[string]any
	if err := yaml.Unmarshal([]byte(strings.Join(lines, "\n")), &fm); err != nil {
		return map[string]any{}
	}
	if fm == nil {
		return map[string]any{}
	}
	return fm
}

// ReadFrontmatter reads just a file's frontmatter block into a generic map,
// for callers (goal_bootstrap scanning) that need a field or two without
// the full "## Done When" validation Parse performs. It reports ok=false,
// with no error, for a file that isn't frontmatter-shaped at all — such
// files (index pages, for instance) are not goal files and are meant to be
// skipped, not treated as parse failures.
func ReadFrontmatter(path string) (fm map[string]any, ok bool, err error) {
	f, err := os.Open(path)
	if err != nil {
		return nil, false, err
	}
	defer f.Close()

	scanner := bufio.NewScanner(f)
	if !scanner.Scan() || scanner.Text() != "---" {
		return nil, false, nil
	}
	var block []string
	closed := false
	for scanner.Scan() {
		line := scanner.Text()
		if line == "---" {
			closed = true
			break
		}
		block = append(block, line)
	}
	if err := scanner.Err(); err != nil {
		return nil, false, err
	}
	if !closed {
		return nil, false, nil
	}
	return parseFrontmatterBlock(block), true, nil
}

// Judge is one recorded done_when judge action, carried into the
// completion log entry Finalize appends.
type Judge struct {
	ID     string
	Reason string
}

// FormatJudges renders judge evidence for the completion log entry, one
// "id: reason" per judge, semicolon-joined.
func FormatJudges(judges []Judge) string {
	if len(judges) == 0 {
		return "(no judge evidence)"
	}
	parts := make([]string, len(judges))
	for i, j := range judges {
		parts[i] = j.ID + ": " + j.Reason
	}
	return strings.Join(parts, "; ")
}

// Finalize records a goal's completion: it marks the frontmatter completed
// (if it isn't already) and appends a log entry, once. Idempotency is keyed
// on "resource @ revision" in the log rather than on frontmatter status,
// because the goal file may already read `status: completed` from a prior
// manual edit, or from a re-run after a crash between the frontmatter write
// and the log append — either way, re-running must converge without a
// duplicate log entry. The key includes the resource (not revision alone)
// because revision is a content hash: two distinct goal files with
// identical content share a revision, and finalizing one must not skip the
// other.
func Finalize(path, logPath, resource, revision string, now time.Time, judges []Judge) error {
	finalizeKey := fmt.Sprintf("<!-- local-okf.finalize: %s @ %s -->", resource, revision)

	logContent, err := os.ReadFile(logPath)
	if err != nil {
		if !os.IsNotExist(err) {
			return err
		}
		logContent = nil
	}
	if strings.Contains(string(logContent), finalizeKey) {
		return nil
	}

	fm, ok, err := ReadFrontmatter(path)
	if err != nil {
		return err
	}
	if !ok {
		return fmt.Errorf("goal file %s has no YAML frontmatter", path)
	}
	if status, _ := fm["status"].(string); status != StatusCompleted {
		nowText := now.UTC().Format("2006-01-02T15:04:05Z")
		if err := setFrontmatterFields(path, []fieldUpdate{
			{key: "status", value: StatusCompleted},
			{key: "completed_at", value: nowText},
		}); err != nil {
			return err
		}
	}

	entry := "\n## " + now.UTC().Format("2006-01-02T15:04:05Z") + " — " + resource + " completed\n" +
		"- revision: " + revision + "\n" +
		"- judge: " + FormatJudges(judges) + "\n" +
		finalizeKey + "\n"

	f, err := os.OpenFile(logPath, os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0o644)
	if err != nil {
		return err
	}
	defer f.Close()
	_, err = f.WriteString(entry)
	return err
}

type fieldUpdate struct {
	key, value string
}

// setFrontmatterFields rewrites a fixed set of flat, top-level scalar
// frontmatter keys in place, leaving every other line of the file —
// frontmatter and body alike — byte-for-byte untouched. A full YAML
// unmarshal/marshal round trip was rejected for this: it would reformat or
// reorder fields the goal file's owner authored by hand, an unrelated diff
// on every finalize. This only has to handle the two flat scalar fields
// finalize ever writes, so a targeted line rewrite is both sufficient and
// far lower risk than re-deriving a YAML library's own formatting choices.
func setFrontmatterFields(path string, updates []fieldUpdate) error {
	content, err := os.ReadFile(path)
	if err != nil {
		return err
	}
	trailingNewline := strings.HasSuffix(string(content), "\n")
	lines := strings.Split(string(content), "\n")
	if trailingNewline {
		lines = lines[:len(lines)-1]
	}

	closeIdx := -1
	for i := 1; i < len(lines); i++ {
		if lines[i] == "---" {
			closeIdx = i
			break
		}
	}
	if len(lines) == 0 || lines[0] != "---" || closeIdx == -1 {
		return fmt.Errorf("%s: frontmatter block not found", path)
	}

	fmLines := lines[1:closeIdx]
	for _, u := range updates {
		fmLines = setOrInsertField(fmLines, u.key, u.value)
	}

	rebuilt := make([]string, 0, len(lines)+len(updates))
	rebuilt = append(rebuilt, "---")
	rebuilt = append(rebuilt, fmLines...)
	rebuilt = append(rebuilt, lines[closeIdx:]...)
	out := strings.Join(rebuilt, "\n")
	if trailingNewline {
		out += "\n"
	}
	return os.WriteFile(path, []byte(out), 0o644)
}

// setOrInsertField replaces a top-level "key: ..." line's value, or
// appends a new "key: value" line when the key is absent. Only an
// unindented line qualifies as top-level, so a same-named key nested under
// another field is left alone.
func setOrInsertField(lines []string, key, value string) []string {
	prefix := key + ":"
	for i, line := range lines {
		if line == key || strings.HasPrefix(line, prefix) {
			lines[i] = key + ": " + value
			return lines
		}
	}
	return append(lines, key+": "+value)
}

// IsGoalShaped reports whether a file starts with a closed YAML frontmatter
// block, without validating its contents. It is a cheap pre-filter for
// bundle directory scans: a Markdown page with no frontmatter (an index
// page, say) is not a goal file and should be skipped rather than treated
// as a parse failure.
func IsGoalShaped(path string) (bool, error) {
	_, ok, err := ReadFrontmatter(path)
	return ok, err
}
