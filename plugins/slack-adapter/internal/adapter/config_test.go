package adapter

import "testing"

func TestMentionPrefix(t *testing.T) {
	tests := []struct {
		name           string
		notifyUserIDs  []string
		expectedPrefix string
	}{
		{
			name:           "no users configured",
			notifyUserIDs:  nil,
			expectedPrefix: "",
		},
		{
			name:           "single user",
			notifyUserIDs:  []string{"U12345"},
			expectedPrefix: "<@U12345> ",
		},
		{
			name:           "multiple users",
			notifyUserIDs:  []string{"U12345", "U67890"},
			expectedPrefix: "<@U12345> <@U67890> ",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			cfg := &Config{NotifyUserIDs: tt.notifyUserIDs}
			got := cfg.MentionPrefix()
			if got != tt.expectedPrefix {
				t.Errorf("MentionPrefix() = %q, want %q", got, tt.expectedPrefix)
			}
		})
	}
}
