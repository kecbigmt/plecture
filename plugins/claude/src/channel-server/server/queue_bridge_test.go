package server

import (
	"encoding/json"
	"net"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/kecbigmt/plecture/contracts/channel-protocol"
)

type queuedTurn struct {
	Type string `json:"type"`
	Text string `json:"text"`
}

func startQueueBridge(t *testing.T) (net.Conn, string) {
	t.Helper()
	queueDir := filepath.Join(t.TempDir(), "queue")
	// A test-named TempDir can push a socket path past the platform's
	// sun_path limit, so the socket gets its own short directory.
	sockDir, err := os.MkdirTemp("", "qb")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { os.RemoveAll(sockDir) })
	sockPath := filepath.Join(sockDir, "b.sock")
	listener, err := NewQueueBridge(sockPath, queueDir, testLogger())
	if err != nil {
		t.Fatalf("NewQueueBridge() error: %v", err)
	}
	t.Cleanup(func() { listener.Close() })
	go listener.Serve()

	conn, err := net.Dial("unix", sockPath)
	if err != nil {
		t.Fatalf("Dial() error: %v", err)
	}
	t.Cleanup(func() { conn.Close() })
	return conn, queueDir
}

func send(t *testing.T, conn net.Conn, typ protocol.MessageType, payload any) {
	t.Helper()
	data, err := protocol.NewEnvelope(typ, payload)
	if err != nil {
		t.Fatalf("NewEnvelope() error: %v", err)
	}
	if err := writeMessage(conn, data); err != nil {
		t.Fatalf("writeMessage() error: %v", err)
	}
}

// queuedTurns waits until want turn files exist, so a test that expects none
// still gives the bridge time to (wrongly) write one.
func queuedTurns(t *testing.T, queueDir string, want int) []queuedTurn {
	t.Helper()
	var files []string
	deadline := time.Now().Add(2 * time.Second)
	for {
		files, _ = filepath.Glob(filepath.Join(queueDir, "*.json"))
		if len(files) >= want && want > 0 || time.Now().After(deadline) {
			break
		}
		time.Sleep(20 * time.Millisecond)
	}
	var turns []queuedTurn
	for _, f := range files {
		raw, err := os.ReadFile(f)
		if err != nil {
			t.Fatalf("ReadFile(%s) error: %v", f, err)
		}
		var turn queuedTurn
		if err := json.Unmarshal(raw, &turn); err != nil {
			t.Fatalf("queued file %s is not JSON: %v", f, err)
		}
		turns = append(turns, turn)
	}
	return turns
}

func TestQueueBridge_MessageBecomesQueuedTurnCarryingItsSender(t *testing.T) {
	conn, queueDir := startQueueBridge(t)
	send(t, conn, protocol.MsgRegister, protocol.RegisterPayload{ThreadTS: "1.1", ChannelID: "C1"})
	send(t, conn, protocol.MsgMessage, protocol.MessagePayload{
		User: `ali"ce`, UserID: "U1", Text: "please rebase\non main", ThreadTS: "1.1", Source: "chat",
	})

	turns := queuedTurns(t, queueDir, 1)
	if len(turns) != 1 {
		t.Fatalf("queued %d turns, want 1", len(turns))
	}
	if turns[0].Type != "channel.message" {
		t.Errorf("Type = %q, want channel.message", turns[0].Type)
	}
	want := `<channel source="channel-server" user="ali&#34;ce" user_id="U1" thread_ts="1.1">` + "\nplease rebase\non main\n</channel>"
	if turns[0].Text != want {
		t.Errorf("Text = %q, want %q", turns[0].Text, want)
	}
}

func TestQueueBridge_PermissionVerdictIsOrdinaryText(t *testing.T) {
	conn, queueDir := startQueueBridge(t)
	send(t, conn, protocol.MsgMessage, protocol.MessagePayload{User: "alice", Text: "y abcde", Source: "chat"})

	turns := queuedTurns(t, queueDir, 1)
	if len(turns) != 1 || !strings.Contains(turns[0].Text, "y abcde") {
		t.Fatalf("turns = %#v, want the verdict text queued as a turn", turns)
	}
}

func TestQueueBridge_NothingButANonEmptyMessageIsQueued(t *testing.T) {
	conn, queueDir := startQueueBridge(t)
	send(t, conn, protocol.MsgRegister, protocol.RegisterPayload{ThreadTS: "1.1"})
	send(t, conn, protocol.MsgMessage, protocol.MessagePayload{User: "alice", Text: ""})

	if turns := queuedTurns(t, queueDir, 0); len(turns) != 0 {
		t.Fatalf("queued %#v, want nothing", turns)
	}
}

func TestQueueBridge_TurnsQueueInArrivalOrder(t *testing.T) {
	conn, queueDir := startQueueBridge(t)
	for _, text := range []string{"first", "second", "third"} {
		send(t, conn, protocol.MsgMessage, protocol.MessagePayload{Text: text})
	}

	turns := queuedTurns(t, queueDir, 3)
	if len(turns) != 3 {
		t.Fatalf("queued %d turns, want 3", len(turns))
	}
	for i, want := range []string{"first", "second", "third"} {
		if !strings.Contains(turns[i].Text, "\n"+want+"\n") {
			t.Errorf("turn %d = %q, want it to carry %q", i, turns[i].Text, want)
		}
	}
}
