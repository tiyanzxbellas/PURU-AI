package ai

import (
	"testing"

	"github.com/tmc/langchaingo/llms"

	"github.com/purujawa06-bot/PURU-AI/internal/messages"
)

func toolParts(n int, ids []string) []messages.Part {
	parts := make([]messages.Part, 0, n)
	for i := 0; i < n; i++ {
		id := ""
		if i < len(ids) {
			id = ids[i]
		}
		parts = append(parts, messages.Part{
			"type":       mustJSON("tool-call"),
			"toolCallId": mustJSON(id),
			"toolName":   mustJSON("test_tool"),
			"input":      mustJSON(map[string]any{}),
		})
	}
	return parts
}

func toolResultParts(n int, ids []string) []messages.Part {
	parts := make([]messages.Part, 0, n)
	for i := 0; i < n; i++ {
		id := ""
		if i < len(ids) {
			id = ids[i]
		}
		parts = append(parts, messages.Part{
			"type":       mustJSON("tool-result"),
			"toolCallId": mustJSON(id),
			"toolName":   mustJSON("test_tool"),
			"output":     mustJSON(map[string]any{"type": "json", "value": map[string]any{"ok": 1}}),
		})
	}
	return parts
}

// TestToChatHistoryToolIDPairing verifies that persisted tool-call and
// tool-result parts without ids (as produced by providers that omit the id,
// e.g. Gemini-family OpenAI-compatible gateways) are replayed with a matching
// non-empty id per position, keeping the call/response counts paired.
func TestToChatHistoryToolIDPairing(t *testing.T) {
	assistant := &messages.Message{Role: "assistant"}
	messages.SetContentParts(assistant, toolParts(2, nil))

	tool := &messages.Message{Role: "tool"}
	messages.SetContentParts(tool, toolResultParts(2, nil))

	conv := toChatHistory([]*messages.Message{assistant, tool})

	toolIDs := make([]string, 0, 2)
	resultIDs := make([]string, 0, 2)
	for _, m := range conv {
		switch mm := m.(type) {
		case llms.AIChatMessage:
			for _, tc := range mm.ToolCalls {
				if tc.ID == "" {
					t.Fatalf("replayed tool call id is empty")
				}
				toolIDs = append(toolIDs, tc.ID)
			}
		case llms.ToolChatMessage:
			if mm.ID == "" {
				t.Fatalf("replayed tool result id is empty")
			}
			resultIDs = append(resultIDs, mm.ID)
		}
	}
	if len(toolIDs) != 2 || len(resultIDs) != 2 {
		t.Fatalf("expected 2 calls + 2 results, got %d + %d", len(toolIDs), len(resultIDs))
	}
	for i := range toolIDs {
		if toolIDs[i] != resultIDs[i] {
			t.Fatalf("call/result id mismatch at %d: %q vs %q", i, toolIDs[i], resultIDs[i])
		}
	}
}

// TestToChatHistoryKeepsStoredIDs verifies that stored ids are preserved
// unchanged during replay.
func TestToChatHistoryKeepsStoredIDs(t *testing.T) {
	assistant := &messages.Message{Role: "assistant"}
	messages.SetContentParts(assistant, toolParts(1, []string{"a1"}))

	tool := &messages.Message{Role: "tool"}
	messages.SetContentParts(tool, toolResultParts(1, []string{"a1"}))

	conv := toChatHistory([]*messages.Message{assistant, tool})

	var got []string
	for _, m := range conv {
		switch mm := m.(type) {
		case llms.AIChatMessage:
			for _, tc := range mm.ToolCalls {
				got = append(got, tc.ID)
			}
		case llms.ToolChatMessage:
			got = append(got, mm.ID)
		}
	}
	if len(got) != 2 || got[0] != "a1" || got[1] != "a1" {
		t.Fatalf("stored ids not preserved: %v", got)
	}
}

// TestToChatHistoryMultipleResultsPerMessage verifies a single stored tool
// message holding several tool-result parts is expanded into one replay message
// per part (keeps Gemini's per-part counting happy).
func TestToChatHistoryMultipleResultsPerMessage(t *testing.T) {
	assistant := &messages.Message{Role: "assistant"}
	messages.SetContentParts(assistant, toolParts(2, nil))

	tool := &messages.Message{Role: "tool"}
	messages.SetContentParts(tool, toolResultParts(2, nil))

	conv := toChatHistory([]*messages.Message{assistant, tool})
	toolMsgs := 0
	for _, m := range conv {
		if _, ok := m.(llms.ToolChatMessage); ok {
			toolMsgs++
		}
	}
	if toolMsgs != 2 {
		t.Fatalf("expected 2 tool messages, got %d", toolMsgs)
	}
}
