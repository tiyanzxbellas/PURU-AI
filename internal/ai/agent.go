// Package agent implements the tool-loop agent (a port of ToolLoopAgent) and
// processMessage (retry + memory injection + scold correction guard).
package ai

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"strings"
	"time"

	"github.com/purujawa06-bot/PURU-AI/internal/config"
	"github.com/purujawa06-bot/PURU-AI/internal/e2b"
	"github.com/purujawa06-bot/PURU-AI/internal/messages"
	"github.com/purujawa06-bot/PURU-AI/internal/prompt"
	"github.com/purujawa06-bot/PURU-AI/internal/skills"
	"github.com/purujawa06-bot/PURU-AI/internal/vfs"
)

const (
	stepTimeout    = 120 * time.Second
	toolTimeout    = 120 * time.Second
	totalAgentTime = 300 * time.Second
	maxCorrection  = 1
	maxRetries     = 4
)

const scoldPrompt = "[system] Lanjutkan pekerjaan sampai selesai. " +
	"Jika masih ada langkah yang perlu diambil, panggil tool yang sesuai sekarang. " +
	"Jika sudah selesai, panggil tool yang sesuai sekarang. [/system]"

const stepLimitHint = "⚠️ Percakapan mencapai batas maksimum langkah. Ketik `lanjut` atau `/ai lanjut` untuk melanjutkan percakapan dengan AI, atau masukkan prompt baru."

// Tool is a registered tool implementation for a single request.
type Tool struct {
	Name        string
	Description string
	Parameters  map[string]any
	Run         func(ctx context.Context, args map[string]any) (any, error)
}

type Agent struct {
	Client     *Client
	Config     *config.Config
	VFS        *vfs.VFS
	E2B        *e2b.Manager
	Catalog    *skills.Catalog
	Registry   *skills.Registry
	HTTP       *http.Client
	ToolsBuild func(opts *ProcessOptions) (map[string]*Tool, error)
	// ClientFor resolves a model client for a chat, allowing users to use
	// their own API settings. Nil means every chat uses Client. Each request
	// builds its own Client (from settings) so parallel users never share a
	// mutating struct.
	ClientFor func(ctx context.Context, chatID int64) *Client
}

// clientFor picks the client for the request's chat: the per-chat resolver
// when configured, otherwise the shared default client.
func (a *Agent) clientFor(ctx context.Context, chatID int64) *Client {
	if a.ClientFor != nil {
		if c := a.ClientFor(ctx, chatID); c != nil {
			return c
		}
	}
	return a.Client
}

func (a *Agent) clientForOpts(ctx context.Context, opts *ProcessOptions) *Client {
	if opts == nil {
		return a.clientFor(ctx, 0)
	}
	return a.clientFor(ctx, opts.ChatID)
}

type ProcessOptions struct {
	ChatID     int64
	SendFile   func(content string, filename string, caption string) error
	SendBuffer func(data []byte, filename string, caption string) error
}

type ProcessResult struct {
	Text             string
	ResponseMessages []*messages.Message
	TotalTokens      int
	LastStepUsage    Usage
}

type runResult struct {
	text                 string
	finalText            string
	responseMessages     []*messages.Message
	totalTokens          int
	lastStepUsage        Usage
	lastStepHasToolCalls bool
	finishMessage        string
	hitStepLimit         bool
}

// ---------------------------------------------------------------------------
// Message conversion (stored ModelMessage -> wire)
// ---------------------------------------------------------------------------

func toWireMessages(msgs []*messages.Message) []*Message {
	var out []*Message
	for _, m := range msgs {
		if m == nil {
			continue
		}
		switch m.Role {
		case "system":
			out = append(out, &Message{Role: "system", Content: m.Text()})
		case "user":
			out = append(out, &Message{Role: "user", Content: m.Text()})
		case "assistant":
			w := &Message{Role: "assistant", Content: m.Text()}
			if messages.IsParts(m) {
				for _, p := range messages.ContentParts(m) {
					if p.Type() != "tool-call" {
						continue
					}
					w.ToolCalls = append(w.ToolCalls, ToolCall{
						ID:        p.Str("toolCallId"),
						Name:      p.Str("toolName"),
						Arguments: argsFromPart(p["input"]),
					})
				}
			}
			if tc := m.Extra("toolCalls"); len(tc) > 0 {
				var calls []struct {
					ID       string `json:"id"`
					Function struct {
						Name      string `json:"name"`
						Arguments any    `json:"arguments"`
					} `json:"function"`
				}
				if err := json.Unmarshal(tc, &calls); err == nil {
					for _, c := range calls {
						args := ""
						switch t := c.Function.Arguments.(type) {
						case string:
							args = t
						default:
							if b, err := json.Marshal(t); err == nil {
								args = string(b)
							}
						}
						w.ToolCalls = append(w.ToolCalls, ToolCall{ID: c.ID, Name: c.Function.Name, Arguments: args})
					}
				}
			}
			out = append(out, w)
		case "tool":
			appended := false
			if messages.IsParts(m) {
				for _, p := range messages.ContentParts(m) {
					if p.Type() != "tool-result" {
						continue
					}
					out = append(out, &Message{
						Role:       "tool",
						ToolCallID: p.Str("toolCallId"),
						Content:    toolResultText(&p),
					})
					appended = true
				}
			}
			if !appended {
				id := strings.Trim(string(m.Extra("toolCallId")), "\"")
				if id == "" {
					continue
				}
				out = append(out, &Message{Role: "tool", ToolCallID: id, Content: m.Text()})
			}
		}
	}
	return out
}

func argsFromPart(raw []byte) string {
	if len(raw) == 0 {
		return "{}"
	}
	var v any
	if err := json.Unmarshal(raw, &v); err != nil {
		return strings.TrimSpace(string(raw))
	}
	b, err := json.Marshal(v)
	if err != nil {
		return "{}"
	}
	return string(b)
}

func toolResultText(p *messages.Part) string {
	raw, ok := (*p)["output"]
	if !ok {
		return ""
	}
	var out struct {
		Type  string          `json:"type"`
		Value json.RawMessage `json:"value"`
	}
	if err := json.Unmarshal(raw, &out); err != nil {
		return ""
	}
	switch out.Type {
	case "text", "error-text":
		var s string
		if json.Unmarshal(out.Value, &s) != nil {
			return strings.TrimSpace(string(out.Value))
		}
		return s
	case "json", "error-json":
		var v any
		if err := json.Unmarshal(out.Value, &v); err == nil {
			if b, err := json.Marshal(v); err == nil {
				return string(b)
			}
		}
		return strings.TrimSpace(string(out.Value))
	default:
		var s string
		if json.Unmarshal(out.Value, &s) == nil {
			return s
		}
		return strings.TrimSpace(string(out.Value))
	}
}

func toolSpecs(tools map[string]*Tool) []ToolSpec {
	out := make([]ToolSpec, 0, len(tools))
	for _, t := range tools {
		out = append(out, ToolSpec{Name: t.Name, Description: t.Description, Parameters: t.Parameters})
	}
	return out
}

// ---------------------------------------------------------------------------
// runOnce: one full tool-calling loop (max loop steps)
// ---------------------------------------------------------------------------

func (a *Agent) runOnce(ctx context.Context, base []*messages.Message, opts *ProcessOptions, tools map[string]*Tool) (*runResult, error) {
	ctx, cancel := context.WithTimeout(ctx, totalAgentTime)
	defer cancel()

	maxSteps := a.Config.MaxLoop
	if maxSteps <= 0 {
		maxSteps = 20
	}
	work := make([]*messages.Message, 0, len(base)+4)
	work = append(work, base...)

	res := &runResult{}
	for step := 0; step < maxSteps; step++ {
		client := a.clientForOpts(ctx, opts)
		stepCtx, cancel := context.WithTimeout(ctx, stepTimeout)
		chatResp, err := client.Chat(stepCtx, toWireMessages(work), toolSpecs(tools), a.Config.Temperature, 0)
		if err != nil {
			cancel()
			return nil, err
		}

		if chatResp.Usage != nil {
			res.lastStepUsage = *chatResp.Usage
			res.totalTokens += chatResp.Usage.TotalTokens
		}

		res.lastStepHasToolCalls = len(chatResp.ToolCalls) > 0
		res.text += chatResp.Content

		assistantParts := make([]messages.Part, 0, 1+len(chatResp.ToolCalls))
		if chatResp.Content != "" {
			assistantParts = append(assistantParts, messages.Part{
				"type": []byte(`"text"`),
				"text": mustJSON(chatResp.Content),
			})
		}
		for _, tc := range chatResp.ToolCalls {
			var input any
			if err := json.Unmarshal([]byte(tc.Arguments), &input); err != nil {
				input = map[string]any{}
			}
			assistantParts = append(assistantParts, messages.Part{
				"type":       []byte(`"tool-call"`),
				"toolCallId": mustJSON(tc.ID),
				"toolName":   mustJSON(tc.Name),
				"input":      mustJSON(input),
			})
		}
		assistant := &messages.Message{Role: "assistant"}
		if len(assistantParts) == 0 {
			assistant.Content = nil // null content
		} else {
			messages.SetContentParts(assistant, assistantParts)
		}
		work = append(work, assistant)
		res.responseMessages = append(res.responseMessages, assistant)

		if len(chatResp.ToolCalls) == 0 {
			cancel()
			break
		}

		for _, tc := range chatResp.ToolCalls {
			args := map[string]any{}
			if err := json.Unmarshal([]byte(tc.Arguments), &args); err != nil {
				args = map[string]any{}
			}
			var result any
			t, ok := tools[tc.Name]
			if !ok {
				result = map[string]any{"error": "unknown tool: " + tc.Name}
			} else {
				rctx, rcancel := context.WithTimeout(stepCtx, toolTimeout)
				val, rerr := t.Run(rctx, args)
				rcancel()
				if rerr != nil {
					result = map[string]any{"error": rerr.Error()}
				} else {
					result = val
				}
			}
			if tc.Name == "finish" {
				res.finishMessage = finishMessageFrom(tc, args, result)
			}
			out := messages.Part{
				"type":       []byte(`"tool-result"`),
				"toolCallId": mustJSON(tc.ID),
				"toolName":   mustJSON(tc.Name),
				"output":     mustJSON(map[string]any{"type": "json", "value": result}),
			}
			toolMsg := &messages.Message{Role: "tool"}
			messages.SetContentParts(toolMsg, []messages.Part{out})
			work = append(work, toolMsg)
			res.responseMessages = append(res.responseMessages, toolMsg)
		}
		cancel()

		if res.finishMessage != "" {
			break
		}
		if step+1 >= maxSteps {
			res.hitStepLimit = true
			break
		}
	}

	res.finalText = res.finishMessage
	if res.finalText == "" {
		res.finalText = strings.TrimSpace(res.text)
	}
	return res, nil
}

func finishMessageFrom(tc ToolCall, args map[string]any, result any) string {
	if s, ok := args["message"].(string); ok && strings.TrimSpace(s) != "" {
		return s
	}
	if m, ok := result.(map[string]any); ok {
		if s, ok := m["message"].(string); ok {
			return s
		}
	}
	return ""
}

func mustJSON(v any) []byte {
	b, _ := json.Marshal(v)
	return b
}

// ---------------------------------------------------------------------------
// ProcessMessage: retry + correction runner
// ---------------------------------------------------------------------------

func textMsg(role, text string) *messages.Message {
	m := &messages.Message{Role: role}
	if text != "" {
		messages.SetContentString(m, text)
	}
	return m
}

func (a *Agent) ProcessMessage(ctx context.Context, userMessage string, history []*messages.Message, opts *ProcessOptions) *ProcessResult {
	// Strip leading non-user messages (mirrors agent.ts guard).
	i := 0
	for i < len(history) && history[i].Role == "system" {
		i++
	}
	for i < len(history) && history[i].Role != "user" {
		history = append(history[:i], history[i+1:]...)
	}

	memoryContent := ""
	if m, ok := a.VFS.ReadFile(ctx, opts.ChatID, "memory/MEMORY.md"); ok {
		memoryContent = m
	}
	if len(memoryContent) > a.Config.MemoryMaxChars {
		memoryContent = memoryContent[:a.Config.MemoryMaxChars] + "\n...[truncated]"
	}

	skillsSummary := a.Catalog.BuildSkillsSummary(ctx, opts.ChatID)
	systemPrompt, err := prompt.Get(memoryContent, skillsSummary)
	if err != nil {
		systemPrompt = ""
	}

	tools, terr := a.ToolsBuild(opts)
	if terr != nil || len(tools) == 0 {
		return errResult()
	}

	var lastErr error
	for attempt := 1; attempt <= maxRetries; attempt++ {
		base := make([]*messages.Message, 0, len(history)+3)
		base = append(base, textMsg("system", systemPrompt))
		base = append(base, history...)
		base = append(base, textMsg("user", userMessage))

		allResp := []*messages.Message{}
		totalTokens := 0
		lastUsage := Usage{}

		var runErr error
		for round := 0; round <= maxCorrection; round++ {
			run, rerr := a.runOnce(ctx, base, opts, tools)
			if rerr != nil {
				runErr = rerr
				break
			}
			totalTokens += run.totalTokens
			lastUsage = run.lastStepUsage

			// Only rounds that finish the turn are persisted to history.
			// Text-only stubs that trigger the scold correction are kept out
			// of allResp so empty/garbled intermediates do not pollute the
			// stored conversation.
			if strings.TrimSpace(run.finishMessage) != "" {
				allResp = append(allResp, run.responseMessages...)
				return makeResult(run.finishMessage, allResp, totalTokens, lastUsage)
			}
			if run.hitStepLimit {
				allResp = append(allResp, run.responseMessages...)
				return makeResult(stepLimitHint, allResp, totalTokens, lastUsage)
			}
			if run.lastStepHasToolCalls {
				allResp = append(allResp, run.responseMessages...)
				if strings.TrimSpace(run.finalText) == "" {
					run.finalText = run.text
				}
				return makeResult(run.finalText, allResp, totalTokens, lastUsage)
			}

			// Last step is text-only without finish -> protocol violation.
			if round < maxCorrection {
				snippet := strings.TrimSpace(run.text)
				if len(snippet) > 300 {
					snippet = snippet[:300]
				}
				base = make([]*messages.Message, 0, len(history)+4)
				base = append(base, textMsg("system", systemPrompt))
				base = append(base, history...)
				base = append(base, textMsg("user", userMessage))
				if snippet != "" {
					base = append(base, textMsg("assistant", snippet))
				}
				base = append(base, textMsg("user", scoldPrompt))
				continue
			}
			// Final corrected round: persist and return its answer.
			allResp = append(allResp, run.responseMessages...)
			if strings.TrimSpace(run.finalText) != "" {
				return makeResult(run.finalText, allResp, totalTokens, lastUsage)
			}
			lastErr = errors.New("Empty response from AI")
			break
		}

		if runErr != nil {
			lastErr = runErr
			if isNonRetryableError(runErr) {
				break
			}
		} else if lastErr == nil {
			lastErr = errors.New("Empty response from AI")
		}
		if attempt < maxRetries {
			backoff := time.Duration(1000<<uint(attempt-1)) * time.Millisecond
			if backoff > 30*time.Second {
				backoff = 30 * time.Second
			}
			sleep(ctx, backoff)
			continue
		}
		break
	}

	_ = lastErr
	return errResult()
}

func sleep(ctx context.Context, d time.Duration) {
	t := time.NewTimer(d)
	defer t.Stop()
	select {
	case <-ctx.Done():
	case <-t.C:
	}
}

func makeResult(text string, resp []*messages.Message, total int, usage Usage) *ProcessResult {
	if strings.TrimSpace(text) == "" {
		text = "Maaf, saya tidak bisa merespons saat ini."
	}
	return &ProcessResult{Text: text, ResponseMessages: resp, TotalTokens: total, LastStepUsage: usage}
}

func errResult() *ProcessResult {
	return &ProcessResult{Text: "Maaf, saya tidak bisa merespons saat ini."}
}

// isNonRetryableError mirrors the TS logic: 4xx except 408/429 are permanent.
func isNonRetryableError(err error) bool {
	var apiErr *APIError
	if errors.As(err, &apiErr) {
		s := apiErr.Status
		return s >= 400 && s < 500 && s != 408 && s != 429
	}
	return false
}
