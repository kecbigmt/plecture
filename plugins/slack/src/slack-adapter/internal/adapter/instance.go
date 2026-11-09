package adapter

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
)

func ResolveStateDir(flagValue string) string {
	if flagValue != "" {
		return flagValue
	}
	if dir := os.Getenv("SLACK_ADAPTER_STATE_DIR"); dir != "" {
		return dir
	}
	return defaultStateDir()
}

func defaultStateDir() string {
	if stateHome := os.Getenv("XDG_STATE_HOME"); stateHome != "" {
		return filepath.Join(stateHome, "slack-adapter")
	}
	home, err := os.UserHomeDir()
	if err != nil {
		return ""
	}
	return filepath.Join(home, ".local", "state", "slack-adapter")
}

// InstanceRegistryDir is deliberately independent of --state-dir and
// SLACK_ADAPTER_STATE_DIR: a resource hook runs with plect's environment, not
// any one instance's, so every instance must publish its address where a hook
// can find it without knowing which instance it wants.
func InstanceRegistryDir() string {
	dir := defaultStateDir()
	if dir == "" {
		return ""
	}
	return filepath.Join(dir, "instances")
}

type instanceRecord struct {
	TeamID     string `json:"team_id"`
	BotUserID  string `json:"bot_user_id"`
	ListenAddr string `json:"listen_addr"`
}

func instanceRecordPath(registryDir, teamID, botUserID string) (string, error) {
	if registryDir == "" {
		return "", errors.New("instance registry directory is unknown")
	}
	if !mentionResourcePattern.MatchString(teamID) || !mentionResourcePattern.MatchString(botUserID) {
		return "", fmt.Errorf("invalid Slack identity team %q bot %q", teamID, botUserID)
	}
	return filepath.Join(registryDir, teamID+"-"+botUserID+".json"), nil
}

// RegisterInstance is never undone on shutdown: a hook that reaches a stopped
// instance fails and is retried, while the adapter answering a reused address
// rejects a mention subscription for a bot that is not its own.
func (a *Adapter) RegisterInstance(registryDir, listenAddr string) error {
	path, err := instanceRecordPath(registryDir, a.teamID, a.botUserID)
	if err != nil {
		return err
	}
	raw, err := json.Marshal(instanceRecord{TeamID: a.teamID, BotUserID: a.botUserID, ListenAddr: listenAddr})
	if err != nil {
		return err
	}
	if err := os.MkdirAll(registryDir, 0o700); err != nil {
		return err
	}
	tmp, err := os.CreateTemp(registryDir, ".instance-*")
	if err != nil {
		return err
	}
	defer os.Remove(tmp.Name())
	if _, err := tmp.Write(raw); err != nil {
		tmp.Close()
		return err
	}
	if err := tmp.Close(); err != nil {
		return err
	}
	return os.Rename(tmp.Name(), path)
}

// MentionBaseURL falls back to the caller's configured address, which keeps a
// single-instance host working before its adapter has ever registered.
func MentionBaseURL(registryDir, teamID, botUserID, fallbackAddr string) string {
	if path, err := instanceRecordPath(registryDir, teamID, botUserID); err == nil {
		if raw, err := os.ReadFile(path); err == nil {
			var record instanceRecord
			if json.Unmarshal(raw, &record) == nil && record.ListenAddr != "" {
				return "http://" + record.ListenAddr
			}
		}
	}
	return "http://" + fallbackAddr
}
