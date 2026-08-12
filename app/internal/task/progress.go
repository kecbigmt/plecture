package task

import (
	"bufio"
	"bytes"
	"fmt"
	"io"
	"os"
	"sync"
	"time"
)

// StreamReporter renders task lifecycle events as live progress lines,
// grouped by scope under a `session:` / `run:` header.
//
// In TTY mode it animates a Braille spinner while a task runs, then
// rewrites the line with the final status. In non-TTY mode it stays quiet
// during the run and writes a single line per task when it finishes —
// safe for `plect up ... | cat` and CI logs.
//
// Task script stderr does not stream live: the runner captures it and
// hands it to OnSuccess/OnFailure, and this reporter dumps it (indented)
// after the status line. That keeps the spinner from being shredded by
// direnv/shell-hook noise mid-run, while still surfacing the same content
// once the task finishes.
type StreamReporter struct {
	w     io.Writer
	isTTY bool

	idWidth int

	mu        sync.Mutex
	tick      *time.Ticker
	done      chan struct{}
	active    activeLine
	lastScope string
}

type activeLine struct {
	scope string
	id    string
	start time.Time
}

const spinnerFrames = `⠋⠙⠹⠸⠼⠴⠦⠧⠇⠏`
const indent = "  "

// NewStreamReporter constructs an observer writing to w. If w is *os.File
// and refers to a terminal, animated output is used; otherwise non-TTY mode.
// taskIDs (in any order) sizes the id column so output stays aligned.
func NewStreamReporter(w io.Writer, taskIDs []string) *StreamReporter {
	return &StreamReporter{
		w:       w,
		isTTY:   writerIsTerminal(w),
		idWidth: longest(taskIDs),
	}
}

func longest(xs []string) int {
	n := 0
	for _, x := range xs {
		if len(x) > n {
			n = len(x)
		}
	}
	return n
}

func writerIsTerminal(w io.Writer) bool {
	f, ok := w.(*os.File)
	if !ok {
		return false
	}
	fi, err := f.Stat()
	if err != nil {
		return false
	}
	return fi.Mode()&os.ModeCharDevice != 0
}

func (r *StreamReporter) prefix(id string) string {
	return fmt.Sprintf("%s%-*s ", indent, r.idWidth, id)
}

// Caller must hold r.mu.
func (r *StreamReporter) ensureScopeHeaderLocked(scope string) {
	if scope == r.lastScope {
		return
	}
	fmt.Fprintf(r.w, "%s:\n", scope)
	r.lastScope = scope
}

func (r *StreamReporter) OnStart(scope, id string) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.stopSpinnerLocked()
	r.ensureScopeHeaderLocked(scope)
	r.active = activeLine{scope: scope, id: id, start: time.Now()}
	if !r.isTTY {
		// A piped reader can't interpret \r line rewrites, so non-TTY
		// output waits for the terminal event and prints once.
		return
	}
	// Draw the first frame synchronously so users see motion before the
	// ticker's first 100ms tick.
	r.drawSpinnerLocked(0)
	r.tick = time.NewTicker(100 * time.Millisecond)
	r.done = make(chan struct{})
	go r.spin(r.tick, r.done)
}

func (r *StreamReporter) spin(tick *time.Ticker, done chan struct{}) {
	i := 1
	for {
		select {
		case <-done:
			return
		case <-tick.C:
			r.mu.Lock()
			if r.tick == tick {
				r.drawSpinnerLocked(i)
				i++
			}
			r.mu.Unlock()
		}
	}
}

func (r *StreamReporter) drawSpinnerLocked(i int) {
	frames := []rune(spinnerFrames)
	frame := frames[i%len(frames)]
	fmt.Fprintf(r.w, "\033[2K\r%s%c", r.prefix(r.active.id), frame)
}

func (r *StreamReporter) stopSpinnerLocked() {
	if r.tick == nil {
		return
	}
	r.tick.Stop()
	close(r.done)
	r.tick = nil
	r.done = nil
}

func (r *StreamReporter) writeFinalLocked(id, icon, tail string) {
	if r.isTTY {
		fmt.Fprintf(r.w, "\033[2K\r%s%s %s\n", r.prefix(id), icon, tail)
	} else {
		fmt.Fprintf(r.w, "%s%s %s\n", r.prefix(id), icon, tail)
	}
}

// stderrIndent is the prefix used when dumping a script's captured stderr
// below its status line. Wider than the id-column indent so the dump is
// visually distinct from the next task's line.
const stderrIndent = "      "

// writeStderrLocked dumps captured stderr indented under the status line.
// Each line is prefixed with stderrIndent; a trailing partial line (no
// newline) is still printed so nothing is silently dropped.
func (r *StreamReporter) writeStderrLocked(stderr []byte) {
	if len(bytes.TrimSpace(stderr)) == 0 {
		return
	}
	scanner := bufio.NewScanner(bytes.NewReader(stderr))
	scanner.Buffer(make([]byte, 0, 4096), 1024*1024)
	for scanner.Scan() {
		fmt.Fprintf(r.w, "%s%s\n", stderrIndent, scanner.Text())
	}
}

func (r *StreamReporter) OnSuccess(scope, id string, elapsed time.Duration, stderr []byte) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.stopSpinnerLocked()
	r.ensureScopeHeaderLocked(scope)
	r.writeFinalLocked(id, "✓", formatDuration(elapsed))
	r.writeStderrLocked(stderr)
}

// OnFailure renders the icon + duration. The error itself is left to the
// CLI/cobra layer (or DestroyResult.CleanupWarnings for --force) so the
// reason is shown exactly once. Captured stderr is dumped below the status
// line so users can see the diagnostic output that led to the failure.
func (r *StreamReporter) OnFailure(scope, id string, elapsed time.Duration, _ error, stderr []byte) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.stopSpinnerLocked()
	r.ensureScopeHeaderLocked(scope)
	r.writeFinalLocked(id, "✗", formatDuration(elapsed))
	r.writeStderrLocked(stderr)
}

func (r *StreamReporter) OnSkip(scope, id, reason string) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.stopSpinnerLocked()
	r.ensureScopeHeaderLocked(scope)
	tail := "skipped"
	if reason != "" {
		tail = fmt.Sprintf("skipped (%s)", reason)
	}
	r.writeFinalLocked(id, "⊘", tail)
}

func formatDuration(d time.Duration) string {
	switch {
	case d < time.Millisecond:
		return fmt.Sprintf("%dµs", d.Microseconds())
	case d < time.Second:
		return fmt.Sprintf("%dms", d.Milliseconds())
	case d < 10*time.Second:
		return fmt.Sprintf("%.2fs", d.Seconds())
	case d < time.Minute:
		return fmt.Sprintf("%.1fs", d.Seconds())
	default:
		return d.Round(time.Second).String()
	}
}
