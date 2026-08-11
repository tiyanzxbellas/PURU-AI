// Package openai implements a minimal OpenAI-compatible chat client that
// implements llms.Model. The bot talks to any OpenAI-compatible endpoint
// (OpenAI, HF Spaces gateways, Gemini-family proxies, ...) through the shared
// http.Client with the browser-like User-Agent.
//
// The client always streams when a StreamingFunc is provided: streaming keeps
// long generations alive past gateway buffering timeouts (a non-streaming
// request that produces a long answer/tool loop gets cut by the proxy with
// HTTP 503). The critical part is the tool-call assembly below.
//
// langchaingo v0.1.14's own OpenAI client (llms/openai) is NOT used here: its
// SSE merge logic (updateToolCalls) only appends tool-call argument fragments
// when the delta has an empty `type`. Real OpenAI-format streams send fragments
// whose first chunk carries `type:"function"` and the rest repeat it — so the
// library turned every argument chunk into a separate, empty-named tool call
// (observed on the HF Spaces gateway serving the `puru` model): tools were
// invoked with empty args and the loop exploded into fake tool-calls until the
// step limit. This package fixes assembly by tracking tool-call fragments via
// their index and merging argument fragments into the last open tool call.
package openai

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"strings"

	"github.com/tmc/langchaingo/llms"
)

// Client is a minimal OpenAI-compatible chat client implementing llms.Model.
type Client struct {
	baseURL string
	apiKey  string
	model   string
	hc      *http.Client
}

// New builds a Client for the given endpoint/key/model using the shared HTTP
// client.
func New(baseURL, apiKey, model string, hc *http.Client) (*Client, error) {
	if hc == nil {
		hc = http.DefaultClient
	}
	return &Client{
		baseURL: strings.TrimSuffix(baseURL, "/"),
		apiKey:  apiKey,
		model:   model,
		hc:      hc,
	}, nil
}

// Call is the llms.Model convenience wrapper.
func (c *Client) Call(ctx context.Context, prompt string, options ...llms.CallOption) (string, error) {
	return llms.GenerateFromSinglePrompt(ctx, c, prompt, options...)
}

// wireMessage is one message in the chat.completions request body.
type wireMessage struct {
	Role         string         `json:"role"`
	Content      string         `json:"content,omitempty"`
	Name         string         `json:"name,omitempty"`
	ToolCalls    []wireToolCall `json:"tool_calls,omitempty"`
	ToolCallID   string         `json:"tool_call_id,omitempty"`
	FunctionCall *wireFuncCall  `json:"function_call,omitempty"`
}

type wireToolCall struct {
	ID       string       `json:"id,omitempty"`
	Type     string       `json:"type,omitempty"`
	Index    *int         `json:"index,omitempty"`
	Function wireFuncCall `json:"function,omitempty"`
}

// FragmentIndex returns the fragment's tool-call index, or -1 when the provider
// did not include one (common on the first fragment of a single tool call).
func (t wireToolCall) FragmentIndex() int {
	if t.Index == nil {
		return -1
	}
	return *t.Index
}

type wireFuncCall struct {
	Name      string `json:"name"`
	Arguments string `json:"arguments"`
}

// wireTool is a tool definition in the request.
type wireTool struct {
	Type     string                  `json:"type"`
	Function llms.FunctionDefinition `json:"function,omitempty"`
}

// wireRequest is the chat.completions request body.
type wireRequest struct {
	Model         string         `json:"model"`
	Messages      []wireMessage  `json:"messages"`
	Temperature   *float64       `json:"temperature,omitempty"`
	MaxTokens     int            `json:"max_tokens,omitempty"`
	Stream        bool           `json:"stream,omitempty"`
	StreamOptions *streamOptions `json:"stream_options,omitempty"`
	Tools         []wireTool     `json:"tools,omitempty"`
}

type streamOptions struct {
	IncludeUsage bool `json:"include_usage,omitempty"`
}

// contentToString renders a message's parts into the wire content string, and
// collects any tool calls / tool responses into the message.
func contentToString(parts []llms.ContentPart, msg *wireMessage) {
	var sb strings.Builder
	for _, p := range parts {
		switch t := p.(type) {
		case llms.TextContent:
			sb.WriteString(t.Text)
		case llms.ToolCall:
			msg.ToolCalls = append(msg.ToolCalls, wireToolCall{
				ID:   t.ID,
				Type: t.Type,
				Function: wireFuncCall{
					Name:      callName(t),
					Arguments: t.FunctionCall.Arguments,
				},
			})
		case llms.ToolCallResponse:
			msg.ToolCallID = t.ToolCallID
			sb.WriteString(t.Content)
		default:
			// Unknown parts are ignored; they are not representable on the wire.
		}
	}
	msg.Content = sb.String()
}

func callName(t llms.ToolCall) string {
	if t.FunctionCall != nil {
		return t.FunctionCall.Name
	}
	return ""
}

// buildMessages converts []llms.MessageContent into wire messages, mirroring
// langchaingo's openai.GenerateContent.
func buildMessages(messages []llms.MessageContent) []wireMessage {
	out := make([]wireMessage, 0, len(messages))
	for _, mc := range messages {
		m := wireMessage{Role: roleName(mc.Role)}
		contentToString(mc.Parts, &m)
		out = append(out, m)
	}
	return out
}

func roleName(r llms.ChatMessageType) string {
	switch r {
	case llms.ChatMessageTypeSystem:
		return "system"
	case llms.ChatMessageTypeAI:
		return "assistant"
	case llms.ChatMessageTypeGeneric:
		return "user"
	case llms.ChatMessageTypeFunction:
		return "function"
	case llms.ChatMessageTypeTool:
		return "tool"
	default: // human and anything else
		return "user"
	}
}

// GenerateContent implements llms.Model. When a StreamingFunc is set the
// request streams (stream:true) and the response is assembled from SSE chunks;
// otherwise it falls back to a plain JSON response (some proxies answer with
// non-SSE JSON even for stream:true).
func (c *Client) GenerateContent(ctx context.Context, messages []llms.MessageContent, options ...llms.CallOption) (*llms.ContentResponse, error) {
	opts := llms.CallOptions{}
	for _, opt := range options {
		opt(&opts)
	}

	model := opts.Model
	if model == "" {
		model = c.model
	}

	streaming := opts.StreamingFunc != nil
	req := wireRequest{
		Model:     model,
		Messages:  buildMessages(messages),
		MaxTokens: opts.MaxTokens,
		Stream:    streaming,
	}
	if streaming {
		req.StreamOptions = &streamOptions{IncludeUsage: true}
	}
	if opts.Temperature != 0 {
		req.Temperature = &opts.Temperature
	}
	for _, fn := range opts.Functions {
		req.Tools = append(req.Tools, wireTool{Type: "function", Function: fn})
	}
	for _, t := range opts.Tools {
		if t.Function != nil {
			req.Tools = append(req.Tools, wireTool{Type: t.Type, Function: *t.Function})
		}
	}

	resp, err := c.roundTrip(ctx, req, opts)
	if err != nil {
		return nil, err
	}
	if len(resp.Choices) == 0 {
		return nil, fmt.Errorf("empty response from model")
	}

	choices := make([]*llms.ContentChoice, len(resp.Choices))
	for i, ch := range resp.Choices {
		cc := &llms.ContentChoice{
			Content:    ch.Message.Content,
			StopReason: ch.FinishReason,
			GenerationInfo: map[string]any{
				"CompletionTokens": resp.Usage.CompletionTokens,
				"PromptTokens":     resp.Usage.PromptTokens,
				"TotalTokens":      resp.Usage.TotalTokens,
			},
		}
		for _, tc := range ch.Message.ToolCalls {
			cc.ToolCalls = append(cc.ToolCalls, llms.ToolCall{
				ID:   tc.ID,
				Type: tc.Type,
				FunctionCall: &llms.FunctionCall{
					Name:      tc.Function.Name,
					Arguments: tc.Function.Arguments,
				},
			})
		}
		if len(cc.ToolCalls) > 0 {
			cc.FuncCall = cc.ToolCalls[0].FunctionCall
		}
		choices[i] = cc
	}
	return &llms.ContentResponse{Choices: choices}, nil
}

// wireResponse mirrors the non-stream chat.completions response.
type wireResponse struct {
	Choices []wireChoice
	Usage   struct {
		PromptTokens     int `json:"prompt_tokens"`
		CompletionTokens int `json:"completion_tokens"`
		TotalTokens      int `json:"total_tokens"`
	} `json:"usage"`
}

type wireChoice struct {
	Message      wireMessage `json:"message"`
	FinishReason string      `json:"finish_reason"`
}

// roundTrip performs the HTTP request and returns a fully assembled
// wireResponse (from a single JSON body or from SSE chunks).
func (c *Client) roundTrip(ctx context.Context, req wireRequest, opts llms.CallOptions) (*wireResponse, error) {
	payload, err := json.Marshal(req)
	if err != nil {
		return nil, err
	}

	httpReq, err := http.NewRequestWithContext(ctx, http.MethodPost, c.baseURL+"/chat/completions", bytes.NewReader(payload))
	if err != nil {
		return nil, err
	}
	httpReq.Header.Set("Content-Type", "application/json")
	if c.apiKey != "" {
		httpReq.Header.Set("Authorization", "Bearer "+c.apiKey)
	}

	resp, err := c.hc.Do(httpReq)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		var e struct {
			Error struct {
				Message string `json:"message"`
			} `json:"error"`
		}
		if json.NewDecoder(resp.Body).Decode(&e) == nil && e.Error.Message != "" {
			return nil, fmt.Errorf("API returned unexpected status code: %d: %s", resp.StatusCode, e.Error.Message)
		}
		return nil, fmt.Errorf("API returned unexpected status code: %d", resp.StatusCode)
	}

	ct := resp.Header.Get("Content-Type")
	if !req.Stream || (ct != "" && !strings.Contains(strings.ToLower(ct), "text/event-stream")) {
		// Non-streaming response, or the proxy answered with plain JSON despite
		// stream:true — either way, read the single JSON body.
		var wr wireResponse
		if err := json.NewDecoder(resp.Body).Decode(&wr); err != nil {
			return nil, err
		}
		return &wr, nil
	}

	return parseSSE(ctx, resp, opts)
}

// parseSSE reads a text/event-stream body and assembles the wireResponse.
// Tool calls are assembled correctly (see package doc).
func parseSSE(ctx context.Context, resp *http.Response, opts llms.CallOptions) (*wireResponse, error) {
	scanner := bufio.NewScanner(resp.Body)
	scanner.Buffer(make([]byte, 0, 64*1024), 4*1024*1024)

	out := &wireResponse{
		Choices: []wireChoice{{}},
	}

	var toolCalls []assembledToolCall

	for scanner.Scan() {
		line := scanner.Text()
		if line == "" {
			continue
		}
		if !strings.HasPrefix(line, "data:") {
			// SSE comments (:...), event:, id:, retry: etc.
			continue
		}
		data := strings.TrimSpace(strings.TrimPrefix(line, "data:"))
		if data == "[DONE]" {
			break
		}

		var chunk streamChunk
		if err := json.Unmarshal([]byte(data), &chunk); err != nil {
			// Skip non-JSON data lines some providers send.
			continue
		}
		if chunk.Usage != nil {
			out.Usage.PromptTokens = chunk.Usage.PromptTokens
			out.Usage.CompletionTokens = chunk.Usage.CompletionTokens
			out.Usage.TotalTokens = chunk.Usage.TotalTokens
		}
		if len(chunk.Choices) == 0 {
			continue
		}
		ch := chunk.Choices[0]
		out.Choices[0].FinishReason = ch.FinishReason

		d := ch.Delta
		out.Choices[0].Message.Content += d.Content

		if len(d.ToolCalls) > 0 {
			toolCalls = mergeToolCallDeltas(toolCalls, d.ToolCalls)
		}
		if opts.StreamingFunc != nil {
			if err := opts.StreamingFunc(ctx, []byte(d.Content)); err != nil {
				return nil, fmt.Errorf("streaming func returned an error: %w", err)
			}
		}
	}
	if err := scanner.Err(); err != nil {
		return nil, err
	}

	for _, tc := range toolCalls {
		out.Choices[0].Message.ToolCalls = append(out.Choices[0].Message.ToolCalls, wireToolCall{
			ID:   tc.ID,
			Type: tc.Type,
			Function: wireFuncCall{
				Name:      tc.Name,
				Arguments: tc.Arguments,
			},
		})
	}
	return out, nil
}

// streamChunk is one SSE data payload.
type streamChunk struct {
	Choices []struct {
		Delta struct {
			Content   string         `json:"content"`
			ToolCalls []wireToolCall `json:"tool_calls"`
		} `json:"delta"`
		FinishReason string `json:"finish_reason"`
	} `json:"choices"`
	Usage *struct {
		PromptTokens     int `json:"prompt_tokens"`
		CompletionTokens int `json:"completion_tokens"`
		TotalTokens      int `json:"total_tokens"`
	} `json:"usage"`
}

// assembledToolCall is a tool call being assembled from streamed fragments.
type assembledToolCall struct {
	Index     int
	ID        string
	Type      string
	Name      string
	Arguments string
}

// mergeToolCallDeltas merges tool-call fragments into the assembled calls.
//
// Real OpenAI streams fragment a single tool call across several deltas:
//
//	delta.tool_calls = [{index:0, id:"call_1", type:"function",
//	    function:{name:"write_file", arguments:""}}]
//	delta.tool_calls = [{index:0, function:{arguments:"{\"path\":\"a"}}]
//	delta.tool_calls = [{index:0, function:{arguments:"\"}}"}}]
//
// Fragments are identified by their `index`, not by position: the first chunk
// carries the name/id, later chunks only carry argument slices. HF Spaces
// gateways additionally repeat type:"function" on every fragment (including
// argument-only ones). The rules:
//
//   - a fragment whose id or function.name is non-empty targets the tool call
//     at that index (creating it when needed) and updates its metadata;
//   - a fragment that only carries function.arguments appends to the tool call
//     with the same index (falling back to the last open call when the index
//     is absent — some providers only send it on the first fragment);
//   - fully empty fragments (role marker deltas) are ignored.
//
// mergeTarget resolves the assembled tool call a delta fragment belongs to.
func mergeTarget(tools []assembledToolCall, idx int, name, id string) ([]assembledToolCall, *assembledToolCall) {
	if name != "" || id != "" {
		// Named fragment: find or create the tool call at idx.
		for i := range tools {
			if tools[i].Index == idx {
				return tools, &tools[i]
			}
		}
		tools = append(tools, assembledToolCall{Index: idx})
		return tools, &tools[len(tools)-1]
	}
	// Argument-only fragment: use the tool call at idx, else the last open one.
	if idx >= 0 {
		for i := range tools {
			if tools[i].Index == idx {
				return tools, &tools[i]
			}
		}
	}
	if len(tools) == 0 {
		tools = append(tools, assembledToolCall{Index: idx})
		return tools, &tools[0]
	}
	return tools, &tools[len(tools)-1]
}

func mergeToolCallDeltas(tools []assembledToolCall, deltas []wireToolCall) []assembledToolCall {
	for _, d := range deltas {
		idx := d.FragmentIndex()
		hasName := d.Function.Name != "" || d.ID != ""
		hasArgs := d.Function.Arguments != ""
		if !hasName && !hasArgs {
			// Empty marker fragment — ignore.
			continue
		}

		var target *assembledToolCall
		tools, target = mergeTarget(tools, idx, d.Function.Name, d.ID)
		if d.ID != "" {
			target.ID = d.ID
		}
		if d.Type != "" {
			target.Type = d.Type
		}
		if d.Function.Name != "" {
			target.Name = d.Function.Name
		}
		if hasArgs {
			target.Arguments += d.Function.Arguments
		}
	}
	return tools
}
