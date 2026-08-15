// Session helpers: the conversation for one request is assembled by langchain
// primitives. Persisted ModelMessages are converted to langchain chat messages
// (chat_history placeholder) and back; the system message is rendered through a
// non-templating formatter so literal braces in the prompt (/skills/{{name}})
// survive. Tool arguments are normalized so strict OpenAI-compatible providers
// always see valid JSON objects.
package ai

import (
	"encoding/json"
	"fmt"
	"strings"

	"github.com/tmc/langchaingo/llms"
	"github.com/tmc/langchaingo/prompts"
	"github.com/tmc/langchaingo/schema"

	"github.com/purujawa06-bot/PURU-AI/internal/messages"
)

// chatMessageFormatter is a prompts.MessageFormatter that emits fixed chat
// messages without any templating (avoids {{name}} being parsed as a Go action).
type chatMessageFormatter struct {
	msg llms.ChatMessage
}

func (f chatMessageFormatter) FormatMessages(map[string]any) ([]llms.ChatMessage, error) {
	return []llms.ChatMessage{f.msg}, nil
}

func (f chatMessageFormatter) GetInputVariables() []string { return nil }

var _ prompts.MessageFormatter = chatMessageFormatter{}

// systemMessageFormatter returns a MessageFormatter that always emits the given
// system message.
func systemMessageFormatter(content string) prompts.MessageFormatter {
	return chatMessageFormatter{msg: llms.SystemChatMessage{Content: content}}
}

// toChatHistory converts persisted ModelMessages into langchain chat messages
// for the chat_history placeholder. Tool-call ids are normalised: when a part
// carries no id (older history written by providers that omit it), a
// deterministic one is minted and reused by the matching tool result so
// strict providers (Gemini) always see function calls paired 1:1 with their
// responses.
func toChatHistory(msgs []*messages.Message) []llms.ChatMessage {
	out := make([]llms.ChatMessage, 0, len(msgs))
	pending := pendingToolIDs{}
	for _, m := range msgs {
		if m == nil {
			continue
		}
		switch m.Role {
		case "system":
			out = append(out, llms.SystemChatMessage{Content: m.Text()})
		case "user":
			pending.reset()
			out = append(out, llms.HumanChatMessage{Content: m.Text()})
		case "assistant":
			chat := llms.AIChatMessage{Content: m.Text()}
			hasCalls := false
			if messages.IsParts(m) {
				for _, p := range messages.ContentParts(m) {
					switch p.Type() {
					case "reasoning", "reasoning-file":
						if chat.ReasoningContent == "" {
							chat.ReasoningContent = p.Str("text")
						}
					case "tool-call":
						id := pending.rememberCall(p.Str("toolCallId"))
						hasCalls = true
						chat.ToolCalls = append(chat.ToolCalls, llms.ToolCall{
							ID:   id,
							Type: "function",
							FunctionCall: &llms.FunctionCall{
								Name:      p.Str("toolName"),
								Arguments: argsFromJSON(p["input"]),
							},
						})
					}
				}
			}
			if raw := m.Extra("toolCalls"); len(raw) > 0 {
				var calls []struct {
					ID       string `json:"id"`
					Function struct {
						Name      string `json:"name"`
						Arguments any    `json:"arguments"`
					} `json:"function"`
				}
				if json.Unmarshal(raw, &calls) == nil {
					for _, c := range calls {
						if strings.TrimSpace(c.Function.Name) == "" {
							continue // never send a name-less tool call
						}
						chat.ToolCalls = append(chat.ToolCalls, llms.ToolCall{
							ID:   pending.rememberCall(c.ID),
							Type: "function",
							FunctionCall: &llms.FunctionCall{
								Name:      c.Function.Name,
								Arguments: toolArgsJSON(argValue(c.Function.Arguments)),
							},
						})
						hasCalls = true
					}
				}
			}
			if !hasCalls {
				pending.reset()
			}
			out = append(out, chat)
		case "tool":
			type toolResp struct {
				id      string
				content string
			}
			var responses []toolResp
			if messages.IsParts(m) {
				for _, p := range messages.ContentParts(m) {
					if p.Type() != "tool-result" {
						continue
					}
					responses = append(responses, toolResp{
						id:      pending.takeResult(p.Str("toolCallId")),
						content: toolResultText(&p),
					})
				}
			} else {
				responses = append(responses, toolResp{
					id:      pending.takeResult(strings.Trim(string(m.Extra("toolCallId")), `"`)),
					content: m.Text(),
				})
			}
			for _, r := range responses {
				if r.id != "" {
					out = append(out, llms.ToolChatMessage{ID: r.id, Content: r.content})
				}
			}
		}
	}
	return out
}

// pendingToolIDs reuses tool-call ids between a replayed assistant message and
// the tool results that immediately follow it. Providers that omit the id in
// their tool calls (Gemini-family OpenAI-compatible gateways) leave persisted
// parts without an id; the id is then minted once at the call and handed to the
// positional result so strict APIs never see a pair mismatch.
type pendingToolIDs struct {
	queue []string
	seq   int
}

// rememberCall registers the canonical id of an assistant tool call.
func (p *pendingToolIDs) rememberCall(stored string) string {
	if s := strings.TrimSpace(stored); s != "" {
		p.queue = append(p.queue, s)
		return s
	}
	p.seq++
	id := fmt.Sprintf("call_%d", p.seq)
	p.queue = append(p.queue, id)
	return id
}

// takeResult returns the id of a tool result — its stored id, or else the
// configured id registered by the matching assistant tool call.
func (p *pendingToolIDs) takeResult(stored string) string {
	if s := strings.TrimSpace(stored); s != "" {
		return s
	}
	if len(p.queue) > 0 {
		id := p.queue[0]
		p.queue = p.queue[1:]
		return id
	}
	return ""
}

func (p *pendingToolIDs) reset() {
	p.queue = nil
	p.seq = 0
}

// stepsToChatMessages converts the executor's recorded steps into chat messages
// (grouped by their plan - same Log) so they fit the agent_scratchpad
// placeholder. reasoningByStep is aligned by absolute step index: each message
// group inherits the reasoning_content its Plan produced so thinking-mode
// providers (deepseek-reasoner) get the reasoning echoed back, avoiding a 400
// on the next Plan.
func stepsToChatMessages(steps []schema.AgentStep, reasoningByStep []string) []llms.ChatMessage {
	if len(steps) == 0 {
		return nil
	}
	out := make([]llms.ChatMessage, 0, 2*len(steps))
	for i := 0; i < len(steps); {
		j := i
		for j < len(steps) && steps[j].Action.Log == steps[i].Action.Log {
			j++
		}
		group := steps[i:j]
		msg := llms.AIChatMessage{Content: strings.TrimSpace(group[0].Action.Log)}
		if reasoning := stepsReasoning(reasoningByStep, i); reasoning != "" {
			msg.ReasoningContent = reasoning
		}
		for _, s := range group {
			msg.ToolCalls = append(msg.ToolCalls, llms.ToolCall{
				ID:   s.Action.ToolID,
				Type: "function",
				FunctionCall: &llms.FunctionCall{
					Name:      s.Action.Tool,
					Arguments: toolArgsJSON(s.Action.ToolInput),
				},
			})
		}
		out = append(out, msg)
		for _, s := range group {
			out = append(out, llms.ToolChatMessage{ID: s.Action.ToolID, Content: s.Observation})
		}
		i = j
	}
	return out
}

// stepsReasoning returns the reasoning recorded for the step at the given index
// (empty when absent).
func stepsReasoning(reasoningByStep []string, idx int) string {
	if idx < 0 || idx >= len(reasoningByStep) {
		return ""
	}
	return reasoningByStep[idx]
}

// chatMessageToContent converts a langchain chat message to the generic
// MessageContent shape expected by llms.Model.GenerateContent.
func chatMessageToContent(msg llms.ChatMessage) llms.MessageContent {
	mc := llms.MessageContent{Role: msg.GetType()}
	switch m := msg.(type) {
	case llms.ToolChatMessage:
		mc.Parts = []llms.ContentPart{llms.ToolCallResponse{
			ToolCallID: m.ID,
			Content:    m.Content,
		}}
	case llms.AIChatMessage:
		if m.Content != "" {
			mc.Parts = append(mc.Parts, llms.TextContent{Text: m.Content})
		}
		for _, tc := range m.ToolCalls {
			mc.Parts = append(mc.Parts, tc)
		}
		if len(mc.Parts) == 0 {
			mc.Parts = []llms.ContentPart{llms.TextContent{Text: m.Content}}
		}
	default:
		mc.Parts = []llms.ContentPart{llms.TextContent{Text: msg.GetContent()}}
	}
	return mc
}

// toolArgsJSON makes the tool call arguments a valid JSON object so strict
// OpenAI-compatible providers do not reject the replayed assistant message.
func toolArgsJSON(raw string) string {
	trimmed := strings.TrimSpace(raw)
	if trimmed == "" {
		return "{}"
	}
	var v any
	if err := json.Unmarshal([]byte(trimmed), &v); err == nil {
		if _, ok := v.(map[string]any); ok {
			return trimmed
		}
		b, _ := json.Marshal(map[string]any{"input": v})
		return string(b)
	}
	b, _ := json.Marshal(map[string]any{"input": trimmed})
	return string(b)
}

func argsFromJSON(raw []byte) string {
	if len(raw) == 0 {
		return "{}"
	}
	return toolArgsJSON(string(raw))
}

func argValue(v any) string {
	switch t := v.(type) {
	case string:
		return t
	case nil:
		return ""
	default:
		b, err := json.Marshal(t)
		if err != nil {
			return ""
		}
		return string(b)
	}
}
