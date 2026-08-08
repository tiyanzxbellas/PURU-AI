// Package ai is a minimal OpenAI-compatible chat/completions client.
//
// It always requests streaming and parses both SSE and plain JSON responses,
// because some proxies reply with SSE framing even for non-streaming requests
// (the same reason the old memory module used streamText).
package ai

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"sort"
	"strings"
)

type Usage struct {
	InputTokens  int
	OutputTokens int
	TotalTokens  int
}

type ToolCall struct {
	ID        string
	Name      string
	Arguments string
}

// Message is one entry of the chat request (wire format).
type Message struct {
	Role       string
	Content    string
	ToolCalls  []ToolCall // assistant messages
	ToolCallID string     // tool messages
}

type ToolSpec struct {
	Name        string
	Description string
	Parameters  map[string]any
}

type ChatResult struct {
	Content      string
	ToolCalls    []ToolCall
	FinishReason string
	Usage        *Usage
}

type Client struct {
	BaseURL string
	APIKey  string
	Model   string
	HTTP    *http.Client
}

type APIError struct {
	Status  int
	Message string
}

func (e *APIError) Error() string {
	return fmt.Sprintf("api error: status %d: %s", e.Status, e.Message)
}

func (c *Client) stop() string {
	return strings.TrimSuffix(c.BaseURL, "/") + "/chat/completions"
}

func wireTools(tools []ToolSpec) []map[string]any {
	out := make([]map[string]any, 0, len(tools))
	for _, t := range tools {
		fn := map[string]any{"name": t.Name}
		if t.Description != "" {
			fn["description"] = t.Description
		}
		if t.Parameters != nil {
			fn["parameters"] = t.Parameters
		}
		out = append(out, map[string]any{"type": "function", "function": fn})
	}
	return out
}

// Chat posts a streaming request and returns the fully assembled result.
func (c *Client) Chat(ctx context.Context, messages []*Message, tools []ToolSpec, temperature float64, maxTokens int) (*ChatResult, error) {
	body := map[string]any{
		"model":       c.Model,
		"messages":    wireMessages(messages),
		"stream":      true,
		"temperature": temperature,
	}
	if len(tools) > 0 {
		body["tools"] = wireTools(tools)
		body["tool_choice"] = "auto"
	}
	if maxTokens > 0 {
		body["max_tokens"] = maxTokens
	}
	raw, err := json.Marshal(body)
	if err != nil {
		return nil, err
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, c.stop(), bytes.NewReader(raw))
	if err != nil {
		return nil, err
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Authorization", "Bearer "+c.APIKey)
	req.Header.Set("Accept", "text/event-stream, application/json")

	resp, err := c.HTTP.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	data, err := io.ReadAll(io.LimitReader(resp.Body, 64<<20))
	if err != nil {
		return nil, err
	}
	if resp.StatusCode >= 400 {
		return nil, &APIError{Status: resp.StatusCode, Message: extractError(data)}
	}
	return parseChatResponse(data)
}

// WithSystem construsts a one-shot [system, user] request and returns text.
// Used by the memory module.
func (c *Client) ChatSystem(ctx context.Context, system, prompt string, maxTokens int) (string, error) {
	res, err := c.Chat(ctx, []*Message{
		{Role: "system", Content: system},
		{Role: "user", Content: prompt},
	}, nil, 0, maxTokens)
	if err != nil {
		return "", err
	}
	return res.Content, nil
}

func extractError(data []byte) string {
	var e struct {
		Error struct {
			Message string `json:"message"`
		} `json:"error"`
	}
	if json.Unmarshal(data, &e) == nil && e.Error.Message != "" {
		return e.Error.Message
	}
	return truncate(string(data), 300)
}

// ---------------------------------------------------------------------------
// Wire formatting
// ---------------------------------------------------------------------------

func wireMessages(msgs []*Message) []map[string]any {
	out := make([]map[string]any, 0, len(msgs))
	for _, m := range msgs {
		if m == nil {
			continue
		}
		w := map[string]any{"role": m.Role, "content": m.Content}
		switch m.Role {
		case "assistant":
			if len(m.ToolCalls) > 0 {
				var tcs []map[string]any
				for _, tc := range m.ToolCalls {
					tcs = append(tcs, map[string]any{
						"id":   tc.ID,
						"type": "function",
						"function": map[string]any{
							"name":      tc.Name,
							"arguments": tc.Arguments,
						},
					})
				}
				w["tool_calls"] = tcs
			}
		case "tool":
			if m.ToolCallID != "" {
				w["tool_call_id"] = m.ToolCallID
			}
		}
		out = append(out, w)
	}
	return out
}

func truncate(s string, n int) string {
	if len(s) > n {
		return s[:n] + "..."
	}
	return s
}

// ---------------------------------------------------------------------------
// Response parsing (plain JSON or SSE)
// ---------------------------------------------------------------------------

type wireChunk struct {
	Delta struct {
		Content   string `json:"content"`
		ToolCalls []struct {
			Index    int    `json:"index"`
			ID       string `json:"id"`
			Function struct {
				Name      string `json:"name"`
				Arguments string `json:"arguments"`
			} `json:"function"`
		} `json:"tool_calls"`
	} `json:"delta"`
	Message struct {
		Role      string `json:"role"`
		Content   string `json:"content"`
		ToolCalls []struct {
			ID       string `json:"id"`
			Type     string `json:"type"`
			Function struct {
				Name      string `json:"name"`
				Arguments string `json:"arguments"`
			} `json:"function"`
		} `json:"tool_calls"`
	} `json:"message"`
	FinishReason string `json:"finish_reason"`
	Error        *struct {
		Message string `json:"message"`
	} `json:"error"`
}

type wireUsage struct {
	PromptTokens     int `json:"prompt_tokens"`
	CompletionTokens int `json:"completion_tokens"`
	TotalTokens      int `json:"total_tokens"`
}

type wireResp struct {
	Choices []wireChunk `json:"choices"`
	Usage   *wireUsage  `json:"usage"`
	Error   *struct {
		Message string `json:"message"`
	} `json:"error"`
}

func parseChatResponse(data []byte) (*ChatResult, error) {
	trimmed := bytes.TrimSpace(data)
	if len(trimmed) > 0 && trimmed[0] == '{' {
		return parseJSONResponse(trimmed)
	}
	return parseSSEResponse(data)
}

func parseJSONResponse(data []byte) (*ChatResult, error) {
	var r wireResp
	if err := json.Unmarshal(data, &r); err != nil {
		return nil, err
	}
	if r.Error != nil {
		return nil, &APIError{Status: 400, Message: r.Error.Message}
	}
	res := &ChatResult{}
	if len(r.Choices) > 0 {
		ch := r.Choices[0]
		res.Content = ch.Message.Content
		res.FinishReason = ch.FinishReason
		for _, tc := range ch.Message.ToolCalls {
			res.ToolCalls = append(res.ToolCalls, ToolCall{ID: tc.ID, Name: tc.Function.Name, Arguments: tc.Function.Arguments})
		}
	}
	if r.Usage != nil {
		res.Usage = &Usage{
			InputTokens:  r.Usage.PromptTokens,
			OutputTokens: r.Usage.CompletionTokens,
			TotalTokens:  r.Usage.TotalTokens,
		}
	}
	return res, nil
}

func parseSSEResponse(data []byte) (*ChatResult, error) {
	res := &ChatResult{}
	toolCalls := map[int]*ToolCall{}

	scanner := bufio.NewScanner(bytes.NewReader(data))
	scanner.Buffer(make([]byte, 64*1024), 16<<20)
	var payloads []string
	current := ""
	for scanner.Scan() {
		l := strings.TrimSpace(scanner.Text())
		if l == "" {
			if current != "" {
				payloads = append(payloads, current)
				current = ""
			}
			continue
		}
		if strings.HasPrefix(l, "data:") {
			if current != "" {
				payloads = append(payloads, current)
			}
			current = strings.TrimSpace(l[len("data:"):])
		}
	}
	if current != "" {
		payloads = append(payloads, current)
	}
	if err := scanner.Err(); err != nil {
		return nil, err
	}

	for _, p := range payloads {
		if err := handleSSEChunk(p, res, toolCalls); err != nil {
			return nil, err
		}
	}

	idx := make([]int, 0, len(toolCalls))
	for i := range toolCalls {
		idx = append(idx, i)
	}
	sort.Ints(idx)
	for _, i := range idx {
		res.ToolCalls = append(res.ToolCalls, *toolCalls[i])
	}
	return res, nil
}

func handleSSEChunk(payload string, res *ChatResult, toolCalls map[int]*ToolCall) error {
	if payload == "" || payload == "[DONE]" {
		return nil
	}
	var r wireResp
	if err := json.Unmarshal([]byte(payload), &r); err != nil {
		return nil // ignore invalid / keepalive lines
	}
	if r.Error != nil {
		return &APIError{Message: r.Error.Message}
	}
	if r.Usage != nil {
		res.Usage = &Usage{
			InputTokens:  r.Usage.PromptTokens,
			OutputTokens: r.Usage.CompletionTokens,
			TotalTokens:  r.Usage.TotalTokens,
		}
	}
	if len(r.Choices) == 0 {
		return nil
	}
	ch := r.Choices[0]
	if ch.Delta.Content != "" {
		res.Content += ch.Delta.Content
	}
	for _, tc := range ch.Delta.ToolCalls {
		tt := toolCalls[tc.Index]
		if tt == nil {
			tt = &ToolCall{}
			toolCalls[tc.Index] = tt
		}
		if tc.ID != "" {
			tt.ID = tc.ID
		}
		if tc.Function.Name != "" {
			tt.Name = tc.Function.Name
		}
		if tc.Function.Arguments != "" {
			tt.Arguments += tc.Function.Arguments
		}
	}
	if ch.FinishReason != "" {
		res.FinishReason = ch.FinishReason
	}
	return nil
}
