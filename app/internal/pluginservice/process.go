package pluginservice

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"sort"
	"strings"
	"sync"
	"syscall"
	"time"

	"github.com/kecbigmt/plecture/app/internal/cachehome"
	"github.com/kecbigmt/plecture/app/internal/datahome"
	"github.com/kecbigmt/plecture/app/internal/persistence"
)

type PublishRequest struct {
	Op       string            `json:"op"`
	ID       string            `json:"id"`
	Resource string            `json:"resource"`
	Type     string            `json:"type"`
	Summary  string            `json:"summary"`
	Body     string            `json:"body"`
	Metadata map[string]string `json:"metadata"`
}

type Protocol struct {
	Publish       func(context.Context, PublishRequest) (int, error)
	Subscriptions func(context.Context) ([]persistence.Subscription, error)
}

// runProcess starts decl's executable and blocks until it exits or ctx is
// cancelled. It records the running pid via onStart before waiting, so a
// caller observes "running" status for the whole process lifetime, not just
// after the fact. On ctx cancellation, the child's whole process group gets
// SIGTERM (a service may spawn helpers of its own); WaitDelay bounds how
// long Wait keeps waiting for the group to actually exit before the
// os/exec package escalates to SIGKILL, so a service that ignores SIGTERM
// cannot hang resident shutdown indefinitely.
//
// Child stdout/stderr are forwarded to logger, one log record per line,
// each tagged with the service id and stream — see
// docs/design/plugin-packaging.md: "Service logs are attached to the
// resident process log with the service id." They never reach the durable
// per-session event log; that log is for plect's own events, not a plugin
// daemon's arbitrary output.
func runProcess(ctx context.Context, decl Declaration, logger *slog.Logger, waitDelay time.Duration, onStart func(pid int), protocols ...*Protocol) error {
	var protocol *Protocol
	if len(protocols) > 0 {
		protocol = protocols[0]
	}
	cmd := exec.CommandContext(ctx, decl.ExecPath, decl.Args...)
	dataDir := filepath.Join(datahome.Resolve(), "plugins", decl.PluginID)
	cacheRoot, err := cachehome.Resolve()
	if err != nil {
		return err
	}
	cacheDir := filepath.Join(cacheRoot, "plugins", decl.PluginID)
	for _, dir := range []string{dataDir, cacheDir} {
		if err := os.MkdirAll(dir, 0o700); err != nil {
			return fmt.Errorf("create service directory %s: %w", dir, err)
		}
		if err := os.Chmod(dir, 0o700); err != nil {
			return err
		}
	}
	cmd.Env = buildEnv(decl.Env, dataDir, cacheDir)
	stderr := &serviceLogWriter{logger: logger, id: decl.ID, stream: "stderr"}
	cmd.Stderr = stderr
	stdin, err := cmd.StdinPipe()
	if err != nil {
		return err
	}
	stdout, err := cmd.StdoutPipe()
	if err != nil {
		return err
	}
	cmd.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
	cmd.Cancel = func() error {
		return syscall.Kill(-cmd.Process.Pid, syscall.SIGTERM)
	}
	cmd.WaitDelay = waitDelay

	if err := cmd.Start(); err != nil {
		return err
	}
	onStart(cmd.Process.Pid)
	encoder := json.NewEncoder(stdin)
	var writeMu sync.Mutex
	write := func(value any) error { writeMu.Lock(); defer writeMu.Unlock(); return encoder.Encode(value) }
	config := decl.Config
	if config == nil {
		config = map[string]any{}
	}
	if err := write(map[string]any{"op": "config", "config": config}); err != nil {
		_ = cmd.Process.Kill()
		_ = cmd.Wait()
		return err
	}
	var initial []persistence.Subscription
	if protocol != nil && protocol.Subscriptions != nil {
		rows, err := protocol.Subscriptions(ctx)
		if err != nil {
			_ = cmd.Process.Kill()
			_ = cmd.Wait()
			return err
		}
		initial = rows
		if rows == nil {
			rows = []persistence.Subscription{}
		}
		if err := write(map[string]any{"op": "subscriptions", "resource_type": "", "add": rows, "remove": []persistence.Subscription{}}); err != nil {
			_ = cmd.Process.Kill()
			_ = cmd.Wait()
			return err
		}
	}
	stopPoll := make(chan struct{})
	if protocol != nil && protocol.Subscriptions != nil {
		go pollSubscriptions(ctx, stopPoll, initial, protocol.Subscriptions, write, logger, decl.ID)
	}
	scanner := bufio.NewScanner(stdout)
	scanner.Buffer(make([]byte, 4096), 1024*1024)
	for scanner.Scan() {
		var request PublishRequest
		if err := json.Unmarshal(scanner.Bytes(), &request); err != nil {
			logger.Error("invalid service request", "service", decl.ID, "error", err)
			continue
		}
		if request.ID == "" {
			logger.Error("service request missing id", "service", decl.ID)
			continue
		}
		if request.Op != "publish" || protocol == nil || protocol.Publish == nil {
			_ = write(map[string]any{"op": "error", "id": request.ID, "code": "invalid_request", "message": "unsupported operation"})
			continue
		}
		count, err := protocol.Publish(ctx, request)
		if err != nil {
			_ = write(map[string]any{"op": "error", "id": request.ID, "code": "publish_failed", "message": err.Error()})
		} else {
			_ = write(map[string]any{"op": "published", "id": request.ID, "delivered": count})
		}
	}
	close(stopPoll)
	_ = stdin.Close()
	err = cmd.Wait()
	// A child that exits without a trailing newline on its last write would
	// otherwise lose that line: Write only logs on '\n' boundaries.
	stderr.flush()
	if scanErr := scanner.Err(); scanErr != nil && err == nil {
		return scanErr
	}
	return err
}

func pollSubscriptions(ctx context.Context, stop <-chan struct{}, previous []persistence.Subscription, list func(context.Context) ([]persistence.Subscription, error), write func(any) error, logger *slog.Logger, id string) {
	ticker := time.NewTicker(time.Second)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-stop:
			return
		case <-ticker.C:
		}
		current, err := list(ctx)
		if err != nil {
			logger.Error("load service subscriptions", "service", id, "error", err)
			continue
		}
		for kind, delta := range subscriptionDeltas(previous, current) {
			if err := write(map[string]any{"op": "subscriptions", "resource_type": kind, "add": delta.add, "remove": delta.remove}); err != nil {
				logger.Error("write subscription delta", "service", id, "error", err)
				return
			}
		}
		previous = current
	}
}

type subscriptionDelta struct {
	add    []persistence.Subscription
	remove []persistence.Subscription
}

func subscriptionDeltas(before, after []persistence.Subscription) map[string]subscriptionDelta {
	key := func(row persistence.Subscription) string {
		return row.SessionID + "\x00" + row.Resource.ID + "\x00" + row.Source
	}
	old := make(map[string]persistence.Subscription, len(before))
	newRows := make(map[string]persistence.Subscription, len(after))
	for _, row := range before {
		old[key(row)] = row
	}
	for _, row := range after {
		newRows[key(row)] = row
	}
	deltas := map[string]subscriptionDelta{}
	for id, row := range old {
		if next, ok := newRows[id]; !ok || !reflect.DeepEqual(row, next) {
			delta := deltas[row.Resource.Type]
			delta.remove = append(delta.remove, row)
			deltas[row.Resource.Type] = delta
		}
	}
	for id, row := range newRows {
		if previous, ok := old[id]; !ok || !reflect.DeepEqual(row, previous) {
			delta := deltas[row.Resource.Type]
			delta.add = append(delta.add, row)
			deltas[row.Resource.Type] = delta
		}
	}
	return deltas
}

// serviceLogWriter is an io.Writer that logs each complete line written to
// it as one log record tagged with the owning service's id and stream
// name, buffering an incomplete trailing line across Write calls (a child
// process's stdout/stderr writes are not guaranteed to land one line per
// Write). Not safe for concurrent use — runProcess gives stdout and stderr
// each their own instance, and os/exec never calls a Writer from more than
// one goroutine at a time for a given stream.
type serviceLogWriter struct {
	logger *slog.Logger
	id     string
	stream string
	buf    bytes.Buffer
}

func (w *serviceLogWriter) Write(p []byte) (int, error) {
	w.buf.Write(p)
	for {
		line, err := w.buf.ReadString('\n')
		if err != nil {
			// No full line yet: ReadString still drained the partial data
			// into line, leaving buf empty — put it back for the next
			// Write (or flush) to complete.
			w.buf.WriteString(line)
			break
		}
		w.logLine(strings.TrimSuffix(line, "\n"))
	}
	return len(p), nil
}

// flush logs a trailing line that never got a '\n', once the child has
// exited and no further Write is coming.
func (w *serviceLogWriter) flush() {
	if w.buf.Len() == 0 {
		return
	}
	w.logLine(w.buf.String())
	w.buf.Reset()
}

func (w *serviceLogWriter) logLine(line string) {
	if line == "" {
		return
	}
	w.logger.Info(line, "service", w.id, "stream", w.stream)
}

func buildEnv(overrides map[string]string, dataDir, cacheDir string) []string {
	merged := make(map[string]string, len(overrides))
	for k, v := range overrides {
		merged[k] = v
	}
	merged["PLECT_DATA_DIR"], merged["PLECT_CACHE_DIR"] = dataDir, cacheDir
	keys := make([]string, 0, len(merged))
	for k := range merged {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	out := make([]string, len(keys))
	for i, k := range keys {
		out[i] = k + "=" + merged[k]
	}
	return out
}
