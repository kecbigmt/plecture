package adapter

import (
	"context"
	"encoding/json"
	"io"
	"strings"
	"testing"
	"time"
)

func TestServiceProtocolPublishesWithDeliveredCountAndErrors(t *testing.T) {
	inputReader, inputWriter := io.Pipe()
	outputReader, outputWriter := io.Pipe()
	defer inputReader.Close()
	defer inputWriter.Close()
	defer outputReader.Close()
	defer outputWriter.Close()
	p := NewServiceProtocol(inputReader, outputWriter)
	input := json.NewEncoder(inputWriter)
	output := json.NewDecoder(outputReader)
	go input.Encode(map[string]any{"op": "config", "config": map[string]any{"bots": []any{}}})
	if _, err := p.ReadConfig(); err != nil {
		t.Fatal(err)
	}
	go input.Encode(map[string]any{"op": "subscriptions", "resource_type": "", "add": []any{}})
	if err := p.ReadSnapshot(); err != nil {
		t.Fatal(err)
	}
	done := make(chan error, 1)
	go func() { done <- p.ReadReplies() }()
	for _, tc := range []struct {
		reply     map[string]any
		wantCount int
		wantError string
	}{
		{map[string]any{"op": "published", "delivered": 1}, 1, ""},
		{map[string]any{"op": "published", "delivered": 0}, 0, ""},
		{map[string]any{"op": "error", "code": "storage", "message": "unavailable"}, 0, "unavailable"},
	} {
		result := make(chan struct {
			count int
			err   error
		}, 1)
		go func() {
			count, err := p.Publish(context.Background(), "slack://T/U", "user.emit", "mention", "body", nil)
			result <- struct {
				count int
				err   error
			}{count, err}
		}()
		var request map[string]any
		if err := output.Decode(&request); err != nil {
			t.Fatal(err)
		}
		if request["resource"] != "slack://T/U" || request["id"] == "" {
			t.Fatalf("request = %+v", request)
		}
		tc.reply["id"] = request["id"]
		if err := input.Encode(tc.reply); err != nil {
			t.Fatal(err)
		}
		select {
		case got := <-result:
			if got.count != tc.wantCount || (tc.wantError != "" && (got.err == nil || !strings.Contains(got.err.Error(), tc.wantError))) || (tc.wantError == "" && got.err != nil) {
				t.Fatalf("count = %d, err = %v", got.count, got.err)
			}
		case <-time.After(time.Second):
			t.Fatal("publish did not receive reply")
		}
	}
	inputWriter.Close()
	if err := <-done; err != io.EOF {
		t.Fatalf("read replies = %v", err)
	}
}
