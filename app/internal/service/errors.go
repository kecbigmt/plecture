package service

// Error codes for machine-readable error identification.
const (
	ErrInvalidURL         = "invalid_url"
	ErrRepoNotAllowed     = "repo_not_allowed"
	ErrSessionNotFound    = "session_not_found"
	ErrInvalidTag         = "invalid_tag"
	ErrInvalidInput       = "invalid_input"
	ErrExecutionFailed    = "execution_failed"
	ErrNotAttachable      = "not_attachable"
	ErrNotProduced        = "not_produced"
	ErrNotCapturable      = "not_capturable"
	ErrHasChildren        = "has_children"
	ErrRelationNotAllowed = "relation_not_allowed"
	ErrChildCapExceeded   = "child_cap_exceeded"
)

// Error is a structured error with a machine-readable code.
type Error struct {
	Code    string `json:"code"`
	Message string `json:"message"`
}

func (e *Error) Error() string {
	return e.Message
}
