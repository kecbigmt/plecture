package config

import (
	"errors"
	"testing"
)

func TestNewLive_ReturnsInitialConfigFromLoad(t *testing.T) {
	want := &Config{WorkdirsRoot: "/one"}
	lv, err := NewLive(func() (*Config, error) { return want, nil }, 0)
	if err != nil {
		t.Fatalf("NewLive: %v", err)
	}
	if got := lv.Get(); got != want {
		t.Fatalf("Get() = %v, want the Config returned by the initial load", got)
	}
}

func TestNewLive_PropagatesInitialLoadError(t *testing.T) {
	wantErr := errors.New("boom")
	_, err := NewLive(func() (*Config, error) { return nil, wantErr }, 0)
	if !errors.Is(err, wantErr) {
		t.Fatalf("NewLive() error = %v, want %v", err, wantErr)
	}
}

func TestLive_Refresh_SwapsInTheNewConfig(t *testing.T) {
	first := &Config{WorkdirsRoot: "/one"}
	second := &Config{WorkdirsRoot: "/two"}
	calls := 0
	lv, err := NewLive(func() (*Config, error) {
		calls++
		if calls == 1 {
			return first, nil
		}
		return second, nil
	}, 0)
	if err != nil {
		t.Fatalf("NewLive: %v", err)
	}
	if got := lv.Get(); got != first {
		t.Fatalf("Get() before refresh = %v, want %v", got, first)
	}
	lv.refresh()
	if got := lv.Get(); got != second {
		t.Fatalf("Get() after refresh = %v, want %v", got, second)
	}
}

// A daemon must stay on its last-known-good Config across a transient
// catalog/lock read error rather than losing plugin-mounted definitions it
// already had.
func TestLive_Refresh_KeepsPreviousConfigOnLoadError(t *testing.T) {
	first := &Config{WorkdirsRoot: "/one"}
	calls := 0
	lv, err := NewLive(func() (*Config, error) {
		calls++
		if calls == 1 {
			return first, nil
		}
		return nil, errors.New("transient")
	}, 0)
	if err != nil {
		t.Fatalf("NewLive: %v", err)
	}
	lv.refresh()
	if got := lv.Get(); got != first {
		t.Fatalf("Get() after failed refresh = %v, want unchanged %v", got, first)
	}
}
