package ai

import (
	"context"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/purujawa06-bot/PURU-AI/internal/config"
	"github.com/purujawa06-bot/PURU-AI/internal/firebase"
	"github.com/purujawa06-bot/PURU-AI/internal/messages"
	"github.com/purujawa06-bot/PURU-AI/internal/skills"
	"github.com/purujawa06-bot/PURU-AI/internal/vfs"
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

// TestProcessMessageDropsStubRound verifies that a text-only round which
// triggers the scold correction is NOT persisted into ProcessResult's
// ResponseMessages (the empty "\n" stub used to leak into stored history and
// degrade later turns).
func TestProcessMessageDropsStubRound(t *testing.T) {
	const stubChunk = `{"choices":[{"delta":{"role":"assistant","content":"\n"},"finish_reason":"stop"}]}`
	const finishChunk = `{"choices":[{"delta":{"role":"assistant","content":"","tool_calls":[{"index":0,"id":"finish_1","type":"function","function":{"name":"finish","arguments":"{\"message\":\"selesai\"}"}}]},"finish_reason":"tool_calls"}]}`

	var calls int
	chatSrv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/event-stream")
		calls++
		chunk := stubChunk
		if calls > 1 {
			chunk = finishChunk
		}
		io.WriteString(w, "data: "+chunk+"\n\n")
		io.WriteString(w, "data: [DONE]\n\n")
	}))
	defer chatSrv.Close()

	fbSrv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusNotFound)
	}))
	defer fbSrv.Close()

	fb := firebase.New(fbSrv.URL, fbSrv.Client())
	agent := &Agent{
		Client:  &Client{BaseURL: chatSrv.URL, APIKey: "test", Model: "test", HTTP: chatSrv.Client()},
		Config:  &config.Config{MaxLoop: 5, MemoryMaxChars: 8000},
		VFS:     vfs.New(fb),
		Catalog: skills.NewCatalog(vfs.New(fb)),
		ToolsBuild: func(opts *ProcessOptions) (map[string]*Tool, error) {
			return map[string]*Tool{
				"finish": {
					Name: "finish",
					Run: func(ctx context.Context, args map[string]any) (any, error) {
						return map[string]any{"done": true, "message": args["message"]}, nil
					},
				},
			}, nil
		},
	}

	res := agent.ProcessMessage(context.Background(), "halo", nil, &ProcessOptions{ChatID: 1})
	if res.Text != "selesai" {
		t.Fatalf("unexpected final text: %q", res.Text)
	}
	if calls != 2 {
		t.Fatalf("expected 2 chat calls (stub round + scold round), got %d", calls)
	}
	for _, m := range res.ResponseMessages {
		if m.Text() == "\n" || (strings.TrimSpace(m.Text()) == "" && !messages.IsParts(m)) {
			t.Fatalf("protocol-violation stub persisted into response: %+v", m)
		}
	}
}
