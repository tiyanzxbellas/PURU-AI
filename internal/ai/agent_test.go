package ai

import (
	"context"
	"io"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/purujawa06-bot/PURU-AI/internal/config"
	"github.com/purujawa06-bot/PURU-AI/internal/messages"
)

// TestRunOnceToolContext runs runOnce against a fake chat endpoint and
// verifies the tool executes with a live (non-canceled) context. Regressions:
// tools used to run with an already-canceled stepCtx, failing every HTTP
// request with "context canceled".
func TestRunOnceToolContext(t *testing.T) {
	const toolCallChunk = `{"choices":[{"delta":{"role":"assistant","content":"","tool_calls":[{"index":0,"id":"call_1","type":"function","function":{"name":"test_tool","arguments":"{}"}}]},"finish_reason":null}]}`
	const plainChunk = `{"choices":[{"delta":{"role":"assistant","content":"ok"},"finish_reason":"stop"}]}`

	var calls int
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/chat/completions" {
			t.Errorf("unexpected path: %s", r.URL.Path)
		}
		w.Header().Set("Content-Type", "text/event-stream")
		calls++
		chunk := toolCallChunk
		if calls > 1 {
			chunk = plainChunk
		}
		io.WriteString(w, "data: "+chunk+"\n\n")
		io.WriteString(w, "data: [DONE]\n\n")
	}))
	defer srv.Close()

	agent := &Agent{
		Client: &Client{BaseURL: srv.URL, APIKey: "test", Model: "test", HTTP: srv.Client()},
		Config: &config.Config{MaxLoop: 5},
	}

	toolRan := false
	var toolCtxErr error
	tools := map[string]*Tool{
		"test_tool": {
			Name: "test_tool",
			Run: func(ctx context.Context, args map[string]any) (any, error) {
				toolRan = true
				toolCtxErr = ctx.Err()
				return map[string]any{"ok": true}, nil
			},
		},
	}

	res, err := agent.runOnce(context.Background(), []*messages.Message{textMsg("user", "halo")}, nil, tools)
	if err != nil {
		t.Fatalf("runOnce: %v", err)
	}
	if !toolRan {
		t.Fatal("test_tool was not executed")
	}
	if toolCtxErr != nil {
		t.Fatalf("tool ran with canceled context: %v", toolCtxErr)
	}
	if res.text != "ok" {
		t.Fatalf("unexpected final text: %q", res.text)
	}
}
