package main

import (
	"context"
	"encoding/json"
	"io"
	"strings"
	"testing"
	"time"

	"github.com/kecbigmt/plecture/plugins/github/src/internal/watcher"
)

func TestServiceProtocolConfigSnapshotDeltaAndPublishReply(t *testing.T) {
	inputReader, inputWriter := io.Pipe()
	outputReader, outputWriter := io.Pipe()
	defer inputReader.Close()
	defer inputWriter.Close()
	defer outputReader.Close()
	defer outputWriter.Close()
	store := watcher.NewProtocolStore(t.TempDir(), t.TempDir())
	protocol := newServiceProtocol(inputReader, outputWriter, store)
	done := make(chan error, 1)
	go func() { done <- protocol.Run() }()
	encode := json.NewEncoder(inputWriter)
	if err := encode.Encode(map[string]any{"op": "config", "config": map[string]any{}}); err != nil {
		t.Fatal(err)
	}
	row := watcher.MembershipRow{Session: "s1", Source: "runtime", Resource: watcher.MembershipResource{ID: "r1", Type: "github.issue", Locator: "https://github.com/org/repo/issues/1"}}
	if err := encode.Encode(map[string]any{"op": "subscriptions", "add": []watcher.MembershipRow{row}, "remove": []watcher.MembershipRow{}}); err != nil {
		t.Fatal(err)
	}
	select {
	case <-protocol.ready:
	case <-time.After(time.Second):
		t.Fatal("snapshot not ready")
	}
	all, err := store.All()
	if err != nil || len(all) != 1 {
		t.Fatalf("snapshot = %v, %v", all, err)
	}
	result := make(chan error, 1)
	go func() {
		ctx, cancel := context.WithTimeout(context.Background(), time.Second)
		defer cancel()
		result <- protocol.Publish(ctx, row.Resource.Locator, "github.new_commits", "push", nil)
	}()
	var request map[string]any
	if err := json.NewDecoder(outputReader).Decode(&request); err != nil {
		t.Fatal(err)
	}
	if request["op"] != "publish" || request["resource"] != row.Resource.Locator || request["id"] == "" {
		t.Fatalf("request = %v", request)
	}
	if err := encode.Encode(map[string]any{"op": "published", "id": request["id"], "delivered": 1}); err != nil {
		t.Fatal(err)
	}
	if err := <-result; err != nil {
		t.Fatal(err)
	}
	if err := encode.Encode(map[string]any{"op": "subscriptions", "add": []watcher.MembershipRow{}, "remove": []watcher.MembershipRow{row}}); err != nil {
		t.Fatal(err)
	}
	deadline := time.Now().Add(time.Second)
	for time.Now().Before(deadline) {
		all, _ = store.All()
		if len(all) == 0 {
			break
		}
		time.Sleep(time.Millisecond)
	}
	if len(all) != 0 {
		t.Fatalf("delta left membership: %v", all)
	}
	inputWriter.Close()
	if err := <-done; err != io.EOF {
		t.Fatalf("run error = %v", err)
	}
}

func TestServiceProtocolRejectsErrorReply(t *testing.T) {
	inputReader, inputWriter := io.Pipe()
	outputReader, outputWriter := io.Pipe()
	defer inputReader.Close()
	defer inputWriter.Close()
	defer outputReader.Close()
	defer outputWriter.Close()
	protocol := newServiceProtocol(inputReader, outputWriter, watcher.NewProtocolStore(t.TempDir(), t.TempDir()))
	go protocol.Run()
	encode := json.NewEncoder(inputWriter)
	encode.Encode(map[string]any{"op": "config", "config": map[string]any{}})
	encode.Encode(map[string]any{"op": "subscriptions", "add": []any{}})
	<-protocol.ready
	result := make(chan error, 1)
	go func() {
		ctx, cancel := context.WithTimeout(context.Background(), time.Second)
		defer cancel()
		result <- protocol.Publish(ctx, "r", "github.error", "summary", nil)
	}()
	var request map[string]any
	if err := json.NewDecoder(outputReader).Decode(&request); err != nil {
		t.Fatal(err)
	}
	encode.Encode(map[string]any{"op": "error", "id": request["id"], "code": "denied", "message": "no subscribers"})
	if err := <-result; err == nil || !strings.Contains(err.Error(), "denied") {
		t.Fatalf("publish error = %v", err)
	}
}
