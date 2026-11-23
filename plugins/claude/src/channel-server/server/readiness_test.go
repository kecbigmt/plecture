package server

import (
	"bufio"
	"context"
	"encoding/json"
	"errors"
	"io"
	"testing"
	"time"

	"github.com/mark3labs/mcp-go/server"

	"github.com/kecbigmt/plecture/contracts/channel-protocol"
)

// stdioClient drives a ChannelServer over the same stdio transport Claude Code
// uses, so the test observes exactly what reaches the agent.
type stdioClient struct {
	t      *testing.T
	in     *io.PipeWriter
	frames chan map[string]any
}

func startStdioClient(t *testing.T, cs *ChannelServer) *stdioClient {
	t.Helper()
	ctx, cancel := context.WithCancel(context.Background())
	clientToServer, serverIn := io.Pipe()
	serverOut, serverToClient := io.Pipe()
	done := make(chan error, 1)
	go func() {
		done <- server.NewStdioServer(cs.MCPServer()).Listen(ctx, clientToServer, serverToClient)
	}()
	c := &stdioClient{t: t, in: serverIn, frames: make(chan map[string]any, 32)}
	go func() {
		scanner := bufio.NewScanner(serverOut)
		scanner.Buffer(make([]byte, 0, 64*1024), 1<<20)
		for scanner.Scan() {
			var frame map[string]any
			if json.Unmarshal(scanner.Bytes(), &frame) == nil {
				c.frames <- frame
			}
		}
	}()
	t.Cleanup(func() {
		cancel()
		if err := serverIn.Close(); err != nil {
			t.Error(err)
		}
		if err := <-done; err != nil && !errors.Is(err, context.Canceled) && !errors.Is(err, io.EOF) {
			t.Errorf("stdio server: %v", err)
		}
		if err := serverToClient.Close(); err != nil {
			t.Error(err)
		}
	})
	return c
}

func (c *stdioClient) send(frame map[string]any) {
	c.t.Helper()
	frame["jsonrpc"] = "2.0"
	data, err := json.Marshal(frame)
	if err != nil {
		c.t.Fatal(err)
	}
	if _, err := c.in.Write(append(data, '\n')); err != nil {
		c.t.Fatal(err)
	}
}

// handshake performs the MCP initialize exchange in the order Claude Code
// does: the initialize request, its response, then the initialized
// notification.
func (c *stdioClient) handshake() {
	c.t.Helper()
	c.send(map[string]any{"id": 1, "method": "initialize", "params": map[string]any{
		"protocolVersion": "2025-06-18",
		"capabilities":    map[string]any{},
		"clientInfo":      map[string]any{"name": "test", "version": "0"},
	}})
	for {
		frame := c.next()
		if frame["id"] != nil {
			break
		}
	}
	c.send(map[string]any{"method": "notifications/initialized"})
}

func (c *stdioClient) next() map[string]any {
	c.t.Helper()
	select {
	case frame := <-c.frames:
		return frame
	case <-time.After(2 * time.Second):
		c.t.Fatal("no frame from the channel server")
		return nil
	}
}

// channelTexts collects channel notification contents until want arrive, then
// confirms nothing further follows.
func (c *stdioClient) channelTexts(want int) []string {
	c.t.Helper()
	var got []string
	for len(got) < want {
		frame := c.next()
		if frame["method"] != "notifications/claude/channel" {
			continue
		}
		params, _ := frame["params"].(map[string]any)
		text, _ := params["content"].(string)
		got = append(got, text)
	}
	select {
	case frame := <-c.frames:
		c.t.Fatalf("unexpected frame after %d channel messages: %v", want, frame)
	case <-time.After(200 * time.Millisecond):
	}
	return got
}

func TestChannelServer_MessageBeforeHandshakeReachesAgentOnceInitialized(t *testing.T) {
	cs := NewChannelServer(&mockSender{})
	client := startStdioClient(t, cs)

	cs.OnMessage(protocol.MessagePayload{Text: "waking event"})
	cs.OnMessage(protocol.MessagePayload{Text: "queued while asleep"})
	client.handshake()

	got := client.channelTexts(2)
	if got[0] != "waking event" || got[1] != "queued while asleep" {
		t.Fatalf("channel messages = %q, want the two early messages in arrival order", got)
	}
}

func TestChannelServer_MessageBeforeAnyClientIsHeldUntilHandshake(t *testing.T) {
	cs := NewChannelServer(&mockSender{})
	cs.OnMessage(protocol.MessagePayload{Text: "before the agent started"})

	client := startStdioClient(t, cs)
	client.handshake()

	if got := client.channelTexts(1); got[0] != "before the agent started" {
		t.Fatalf("channel message = %q", got[0])
	}
}

func TestChannelServer_MessageAfterHandshakeIsForwardedOnceInOrder(t *testing.T) {
	cs := NewChannelServer(&mockSender{})
	client := startStdioClient(t, cs)
	cs.OnMessage(protocol.MessagePayload{Text: "early"})
	client.handshake()
	if got := client.channelTexts(1); got[0] != "early" {
		t.Fatalf("early message = %q", got[0])
	}

	cs.OnMessage(protocol.MessagePayload{Text: "late"})
	if got := client.channelTexts(1); got[0] != "late" {
		t.Fatalf("late message = %q", got[0])
	}
}

func TestChannelServer_RepeatedInitializedNotificationDoesNotRedeliver(t *testing.T) {
	cs := NewChannelServer(&mockSender{})
	client := startStdioClient(t, cs)
	cs.OnMessage(protocol.MessagePayload{Text: "once"})
	client.handshake()
	client.channelTexts(1)

	client.send(map[string]any{"method": "notifications/initialized"})
	client.channelTexts(0)
}
