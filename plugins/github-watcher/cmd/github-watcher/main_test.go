package main

import (
	"strings"
	"testing"

	"github.com/kecbigmt/plect/plugins/github-watcher/internal/watcher"
)

func TestConfigureDeliveryRequiresBusUnlessLegacyOptIn(t *testing.T) {
	var poller watcher.Poller
	delivery, err := configureDelivery(&poller, "", "", "http://127.0.0.1:7890/notify", false)
	if err == nil {
		t.Fatal("expected missing PLECT_BUS_SOCKET to fail without legacy opt-in")
	}
	if delivery != "" {
		t.Errorf("delivery = %q, want empty", delivery)
	}
	if !strings.Contains(err.Error(), "--allow-legacy-notify") {
		t.Errorf("error = %q, want legacy opt-in hint", err)
	}
	if poller.Bus != nil || poller.NotifyURL != "" {
		t.Errorf("poller should not be configured on error: %+v", &poller)
	}
}

func TestConfigureDeliveryUsesBusWhenSocketIsSet(t *testing.T) {
	var poller watcher.Poller
	delivery, err := configureDelivery(&poller, "/tmp/plect-bus.sock", "secret", "http://127.0.0.1:7890/notify", true)
	if err != nil {
		t.Fatal(err)
	}
	if delivery != "bus" {
		t.Errorf("delivery = %q, want bus", delivery)
	}
	if poller.Bus == nil {
		t.Fatal("Bus was not configured")
	}
	if poller.NotifyURL != "" {
		t.Errorf("NotifyURL = %q, want empty when bus is set", poller.NotifyURL)
	}
}

func TestConfigureDeliveryAllowsExplicitLegacyNotify(t *testing.T) {
	var poller watcher.Poller
	const notifyURL = "http://127.0.0.1:7890/notify"
	delivery, err := configureDelivery(&poller, "", "", notifyURL, true)
	if err != nil {
		t.Fatal(err)
	}
	if delivery != "notify" {
		t.Errorf("delivery = %q, want notify", delivery)
	}
	if poller.NotifyURL != notifyURL {
		t.Errorf("NotifyURL = %q, want %q", poller.NotifyURL, notifyURL)
	}
	if poller.Bus != nil {
		t.Fatal("Bus should not be configured for legacy notify")
	}
}
