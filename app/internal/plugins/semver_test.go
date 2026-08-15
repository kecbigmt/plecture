package plugins

import "testing"

func TestAtLeast(t *testing.T) {
	tests := []struct {
		name       string
		running    string
		minVersion string
		want       bool
	}{
		{"equal versions satisfy", "0.8.0", "0.8.0", true},
		{"newer patch satisfies", "0.8.1", "0.8.0", true},
		{"newer minor satisfies", "0.9.0", "0.8.0", true},
		{"newer major satisfies", "1.0.0", "0.8.0", true},
		{"older patch fails", "0.8.0", "0.8.1", false},
		{"older minor fails", "0.8.0", "0.9.0", false},
		{"older major fails", "0.8.0", "1.0.0", false},
		{"dev suffix stripped for comparison", "0.0.0-dev", "0.0.0", true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := AtLeast(tt.running, tt.minVersion)
			if err != nil {
				t.Fatalf("AtLeast(%q, %q): unexpected error: %v", tt.running, tt.minVersion, err)
			}
			if got != tt.want {
				t.Errorf("AtLeast(%q, %q) = %v, want %v", tt.running, tt.minVersion, got, tt.want)
			}
		})
	}
}

func TestAtLeast_RejectsMalformedVersions(t *testing.T) {
	tests := []struct {
		name       string
		running    string
		minVersion string
	}{
		{"running has too few components", "0.8", "0.8.0"},
		{"minVersion has too few components", "0.8.0", "0.8"},
		{"running has non-numeric component", "0.x.0", "0.8.0"},
		{"minVersion is empty", "0.8.0", ""},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if _, err := AtLeast(tt.running, tt.minVersion); err == nil {
				t.Fatalf("AtLeast(%q, %q): want error, got nil", tt.running, tt.minVersion)
			}
		})
	}
}
