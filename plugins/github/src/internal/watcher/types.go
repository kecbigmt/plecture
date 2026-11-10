package watcher

type Subscription struct {
	SessionName string            `json:"session_name"`
	Resource    string            `json:"resource"`
	Branch      string            `json:"branch,omitempty"`
	Last        map[string]string `json:"last,omitempty"`
	FailStreak  int               `json:"fail_streak,omitempty"`
}

func subKey(session, resource string) string {
	return session + "\x00" + resource
}

type cacheEntry struct {
	ETag string `json:"etag"`
	Body string `json:"body"`
}
