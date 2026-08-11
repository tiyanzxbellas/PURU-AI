package ai

import (
	"context"
	"encoding/json"
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

// fakeChatServer answers /chat/completions with non-streamed JSON ChatCompletion
// responses — the bot's OpenAI client no longer requests SSE (see model.go), so
// the fixtures mirror the real wire format. The response for call i is
// chunks[i] (the last one is replayed for excess calls). Every HTTP request body
// is handed to checkBody (when non-nil) so a test can validate the wire format.
func fakeChatServer(t *testing.T, chunks []string, checkBody func(t *testing.T, body map[string]any)) *httptest.Server {
	t.Helper()
	var calls int
	return httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/chat/completions" {
			t.Errorf("unexpected path: %s", r.URL.Path)
		}
		var body map[string]any
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
			t.Errorf("decode body: %v", err)
		}
		if checkBody != nil {
			checkBody(t, body)
		}
		calls++
		idx := calls - 1
		if idx >= len(chunks) {
			idx = len(chunks) - 1
		}
		w.Header().Set("Content-Type", "application/json")
		w.Write([]byte(chunks[idx]))
	}))
}

// toolCallChunk builds a non-streamed response whose assistant message performs
// one tool call with a provider-supplied id.
func toolCallChunk(name, args string) string {
	return `{"object":"chat.completion","choices":[{"index":0,"message":{"role":"assistant","content":"","tool_calls":[{"index":0,"id":"t1","type":"function","function":{"name":"` + name + `","arguments":"` + args + `"}}]},"finish_reason":"tool_calls"}]}`
}

// toolCallChunkNoID builds a tool-call response that omits the tool-call id, as
// some OpenAI-compatible gateways (Gemini-family) do. The agent must mint one so
// the tool-result pairs up.
func toolCallChunkNoID(name, args string) string {
	return `{"object":"chat.completion","choices":[{"index":0,"message":{"role":"assistant","content":"","tool_calls":[{"index":0,"type":"function","function":{"name":"` + name + `","arguments":"` + args + `"}}]},"finish_reason":"tool_calls"}]}`
}

// textChunk builds a non-streamed plain-text response that stops the loop.
func textChunk(text string) string {
	return `{"object":"chat.completion","choices":[{"index":0,"message":{"role":"assistant","content":"` + text + `"},"finish_reason":"stop"}]}`
}

// TestRunOnceToolContext runs runOnce (langchaingo executor) against a fake
// chat endpoint and verifies the tool executes with a live (non-canceled)
// context. Regression: in the old manual loop a prematurely canceled stepCtx
// made every tool HTTP request fail with "context canceled".
func TestRunOnceToolContext(t *testing.T) {
	srv := fakeChatServer(t, []string{
		toolCallChunk("test_tool", `{}`),
		textChunk("ok"),
	}, nil)
	defer srv.Close()

	model, err := NewModel(srv.URL, "test", "test", srv.Client())
	if err != nil {
		t.Fatalf("NewModel: %v", err)
	}
	agent := &Agent{
		Client: model,
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

	res, err := agent.runOnce(context.Background(), "", nil, "halo", nil, tools)
	if err != nil {
		t.Fatalf("runOnce: %v", err)
	}
	if !toolRan {
		t.Fatal("test_tool was not executed")
	}
	if toolCtxErr != nil {
		t.Fatalf("tool ran with canceled context: %v", toolCtxErr)
	}
	if res.finalText != "ok" {
		t.Fatalf("unexpected final text: %q", res.finalText)
	}
}

// TestProcessMessagePlainAnswer verifies the default langchaingo behavior: a
// natural text answer stops the loop and is returned as-is (no scold, no
// mandatory finish tool).
func TestProcessMessagePlainAnswer(t *testing.T) {
	srv := fakeChatServer(t, []string{textChunk("Hai!")}, nil)
	defer srv.Close()
	agent := newAgent(t, srv)

	res := agent.ProcessMessage(context.Background(), "halo", nil, &ProcessOptions{ChatID: 1})
	if res.Text != "Hai!" {
		t.Fatalf("unexpected final text: %q", res.Text)
	}
	if len(res.ResponseMessages) != 1 {
		t.Fatalf("expected a single final assistant message, got %d", len(res.ResponseMessages))
	}
	if res.ResponseMessages[0].Role != "assistant" || res.ResponseMessages[0].Text() != "Hai!" {
		t.Fatalf("final assistant message not persisted: %+v", res.ResponseMessages[0])
	}
}

// TestRunOnceOmitsProviderToolID verifies that a provider omitting the
// tool-call id in its streaming deltas still produces a wire request whose tool
// call and tool result share the same non-empty id — Gemini 400s otherwise.
func TestRunOnceOmitsProviderToolID(t *testing.T) {
	var secondBody map[string]any
	srv := fakeChatServer(t, []string{
		toolCallChunkNoID("test_tool", `{}`),
		textChunk("done"),
	}, func(t *testing.T, body map[string]any) {
		secondBody = body
	})
	defer srv.Close()

	model, err := NewModel(srv.URL, "test", "test", srv.Client())
	if err != nil {
		t.Fatalf("NewModel: %v", err)
	}
	agent := &Agent{
		Client: model,
		Config: &config.Config{MaxLoop: 5},
	}
	tools := map[string]*Tool{
		"test_tool": {
			Name: "test_tool",
			Run:  func(ctx context.Context, args map[string]any) (any, error) { return map[string]any{"ok": true}, nil },
		},
	}

	res, err := agent.runOnce(context.Background(), "", nil, "halo", nil, tools)
	if err != nil {
		t.Fatalf("runOnce: %v", err)
	}
	if res.finalText != "done" {
		t.Fatalf("unexpected final text: %q", res.finalText)
	}

	msgs, ok := secondBody["messages"].([]any)
	if !ok {
		t.Fatalf("no messages in second request body")
	}
	var callID, resultID string
	for _, raw := range msgs {
		m, ok := raw.(map[string]any)
		if !ok {
			continue
		}
		switch m["role"] {
		case "assistant":
			if tcs, ok := m["tool_calls"].([]any); ok {
				for _, tc := range tcs {
					if tcm, ok := tc.(map[string]any); ok {
						if id, _ := tcm["id"].(string); id != "" {
							callID = id
						}
					}
				}
			}
		case "tool":
			if id, ok := m["tool_call_id"].(string); ok && id != "" {
				resultID = id
			}
		}
	}
	if callID == "" {
		t.Fatal("replayed tool call has no id")
	}
	if resultID == "" {
		t.Fatal("replayed tool result has no id")
	}
	if callID != resultID {
		t.Fatalf("tool call/result id mismatch on wire: %q vs %q", callID, resultID)
	}
}

// TestProcessMessageEmptyOutput verifies that an empty text-only model response
// does not pollute history: ProcessMessage retries and falls back to the error
// text.
func TestProcessMessageEmptyOutput(t *testing.T) {
	srv := fakeChatServer(t, []string{textChunk("\n")}, nil)
	defer srv.Close()

	agent := newAgent(t, srv)
	res := agent.ProcessMessage(context.Background(), "halo", nil, &ProcessOptions{ChatID: 1})
	if res.Text != "Maaf, saya tidak bisa merespons saat ini." {
		t.Fatalf("unexpected fallback text: %q", res.Text)
	}
	for _, m := range res.ResponseMessages {
		if strings.TrimSpace(m.Text()) == "" && !messages.IsParts(m) {
			t.Fatalf("empty stub persisted into response: %+v", m)
		}
	}
}

// TestProviderStrictArguments verifies that tool calls whose arguments came
// back malformed from the model are normalized to valid JSON objects before the
// assistant tool-call message is replayed to the provider ([400] ... invalid
// 'arguments' guard).
func TestProviderStrictArguments(t *testing.T) {
	type wireMsg struct {
		Role      string `json:"role"`
		ToolCalls []struct {
			Function struct {
				Arguments json.RawMessage `json:"arguments"`
			} `json:"function"`
		} `json:"tool_calls"`
	}
	var wireErr error
	srv := fakeChatServer(t, []string{
		toolCallChunk("echo_tool", `not-valid-json`),
		textChunk("ok"),
	}, func(t *testing.T, body map[string]any) {
		if wireErr != nil {
			return
		}
		raw, err := json.Marshal(body["messages"])
		if err != nil {
			wireErr = err
			return
		}
		var msgs []wireMsg
		if err := json.Unmarshal(raw, &msgs); err != nil {
			wireErr = err
			return
		}
		for _, m := range msgs {
			for _, tc := range m.ToolCalls {
				if !json.Valid(tc.Function.Arguments) {
					wireErr = &invalidArgs{args: string(tc.Function.Arguments)}
					return
				}
			}
		}
	})
	defer srv.Close()

	toolRan := false
	agent := newAgent(t, srv)
	agent.ToolsBuild = func(opts *ProcessOptions) (map[string]*Tool, error) {
		return map[string]*Tool{
			"echo_tool": {
				Name: "echo_tool",
				Run: func(ctx context.Context, args map[string]any) (any, error) {
					toolRan = true
					return args, nil
				},
			},
		}, nil
	}
	res := agent.ProcessMessage(context.Background(), "halo", nil, &ProcessOptions{ChatID: 1})
	if wireErr != nil {
		t.Fatalf("invalid arguments sent to provider: %v", wireErr)
	}
	if !toolRan {
		t.Fatal("echo_tool was not executed")
	}
	if res.Text != "ok" {
		t.Fatalf("unexpected final text: %q", res.Text)
	}
}

type invalidArgs struct{ args string }

func (e *invalidArgs) Error() string { return "assistant tool call arguments are not valid JSON: " + e.args }

// newAgent builds an Agent wired to the fake server (VFS/Catalog so
// ProcessMessage can render the system prompt).
func newAgent(t *testing.T, srv *httptest.Server) *Agent {
	t.Helper()
	fbSrv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusNotFound)
	}))
	t.Cleanup(fbSrv.Close)

	fb := firebase.New(fbSrv.URL, fbSrv.Client())
	model, err := NewModel(srv.URL, "test", "test", srv.Client())
	if err != nil {
		t.Fatalf("NewModel: %v", err)
	}
	return &Agent{
		Client:  model,
		Config:  &config.Config{MaxLoop: 5, MemoryMaxChars: 8000},
		VFS:     vfs.New(fb),
		Catalog: skills.NewCatalog(vfs.New(fb)),
		ToolsBuild: func(opts *ProcessOptions) (map[string]*Tool, error) {
			// At least one tool so ProcessMessage does not short-circuit on an
			// empty tool set; the fake models never invoke it.
			return map[string]*Tool{
				"list_directory": {
					Name: "list_directory",
					Run: func(ctx context.Context, args map[string]any) (any, error) {
						return map[string]any{"entries": []any{}}, nil
					},
				},
			}, nil
		},
	}
}

// TestToolArgsJSON covers the normalization rules.
func TestToolArgsJSON(t *testing.T) {
	cases := map[string]string{
		"":                 "{}",
		"   ":              "{}",
		`{}`:               `{}`,
		`{"path":"a.js"}`:  `{"path":"a.js"}`,
		`not json`:         `{"input":"not json"}`,
		`"a string"`:       `{"input":"a string"}`,
	}
	for in, want := range cases {
		if got := toolArgsJSON(in); got != want {
			t.Errorf("toolArgsJSON(%q) = %s, want %s", in, got, want)
		}
	}
}