package ai

import (
	"strings"
	"testing"
)

// TestChatSessionID verifies the per-chat session id is stable, prefixed and
// distinct across different chats.
func TestChatSessionID(t *testing.T) {
	a := ChatSessionID(123)
	if !strings.HasPrefix(a, "ses_") || len(a) <= len("ses_") {
		t.Fatalf("session id shape wrong: %q", a)
	}
	if b := ChatSessionID(123); b != a {
		t.Fatalf("session id must be stable per chat: %q vs %q", a, b)
	}
	if c := ChatSessionID(124); c == a {
		t.Fatalf("different chats must have different session ids: %q", a)
	}
}
