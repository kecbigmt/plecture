package server

// MessageSender abstracts sending replies and permission prompts back to the message source.
type MessageSender interface {
	SendReply(text string) error
	SendPermissionPrompt(text string) error
}
