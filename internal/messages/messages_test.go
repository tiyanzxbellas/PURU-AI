package messages

import (
	"encoding/json"
	"strings"
	"testing"
)

func makeMsg(role, text string) *Message {
	m := &Message{Role: role}
	SetContentString(m, text)
	return m
}

func TestRoundTripPreservesExtras(t *testing.T) {
	raw := []byte(`[{"role":"assistant","content":[],"toolCalls":[{"id":"1","function":{"name":"search","arguments":"{\"q\":\"x\"}"}}]},{"role":"tool","content":[{"type":"tool-result","toolCallId":"1","toolName":"search","output":{"type":"json","value":{"ok":1}}}],"providerOptions":{}}]`)
	var msgs []*Message
	if err := json.Unmarshal(raw, &msgs); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	out, err := json.Marshal(msgs)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	if !strings.Contains(string(out), `"toolCalls"`) {
		t.Fatalf("round trip lost toolCalls: %s", string(out))
	}
	if !strings.Contains(string(out), `"providerOptions"`) {
		t.Fatalf("round trip lost providerOptions: %s", string(out))
	}
}

func TestCapUserTurns(t *testing.T) {
	var history []*Message
	for _, pair := range [][2]string{
		{"user", "u1"}, {"assistant", "a1"},
		{"user", "u2"}, {"assistant", "a2"},
		{"user", "u3"}, {"assistant", "a3"},
		{"user", "u4"}, {"assistant", "a4"},
		{"user", "u5"}, {"assistant", "a5"},
	} {
		history = append(history, makeMsg(pair[0], pair[1]))
	}
	got := CapUserTurns(history)
	users := 0
	for _, m := range got {
		if m.Role == "user" {
			users++
		}
	}
	if users >= MaxUserMessages {
		t.Fatalf("CapUserTurns kept %d user turns", users)
	}
	if r := firstNonSystemRole(got); r != "user" {
		t.Fatalf("expected to start with user, got %q", r)
	}
}

func TestEnsureStartsWithUser(t *testing.T) {
	in := []*Message{
		{Role: "system"},
		makeMsg("assistant", "x"),
		makeMsg("user", "y"),
	}
	out := EnsureStartsWithUser(in)
	if out[0].Role != "system" || len(out) != 2 {
		t.Fatalf("unexpected ensure result: %+v", out)
	}
	out = EnsureStartsWithUser(out)
	if firstNonSystemRole(out) != "user" {
		t.Fatalf("expect user first, got %s", firstNonSystemRole(out))
	}
}

func TestSanitizeTruncatesText(t *testing.T) {
	big := strings.Repeat("x", 10000)
	truncated := SanitizeMessage(makeMsg("user", big))
	s, ok := ContentString(truncated)
	if !ok || len(s) > MaxStoredContent+len("\n...[truncated]") {
		t.Fatalf("sanitize did not truncate: length=%d", len(s))
	}
}

func firstNonSystemRole(msgs []*Message) string {
	for _, m := range msgs {
		if m.Role != "system" {
			return m.Role
		}
	}
	return ""
}
