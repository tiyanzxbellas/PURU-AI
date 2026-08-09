// Package memory auto-updates /memory/MEMORY.md via an internal model call.
// The memory stays minimal: max 5 numbered important points plus a short
// "current topic" section; obsolete/irrelevant entries are dropped. The model
// is langchaingo's OpenAI-compatible client (streaming, SSE-tolerant).
package memory

import (
	"context"
	"strings"

	"github.com/tmc/langchaingo/llms"

	"github.com/purujawa06-bot/PURU-AI/internal/messages"
	"github.com/purujawa06-bot/PURU-AI/internal/vfs"
)

const memoryMaxOutput = 1500

const memoryPrompt = `You are the memory manager for AI assistant "PURU-AI".

Read the old MEMORY.md and the recent conversation, then write a new MEMORY.md:
- Keep ONLY the most important facts: user info, decisions, preferences, ongoing tasks, unfinished threads.
- Max 5 numbered points (1-5), short and clear. Remove everything outdated or irrelevant.
- End with a short "Topik sedang dibahas:" line describing what is currently being discussed (write "—" when nothing is).
- Output ONLY the MEMORY.md content (markdown, max 1500 chars), no title or preamble.`

func noopStream(context.Context, []byte) error { return nil }

type Manager struct {
	model llms.Model
	vfs   *vfs.VFS
	// ClientFor resolves the model per chat so users with their own API
	// settings get memory updates through their key too. Nil uses model.
	ClientFor func(ctx context.Context, chatID int64) llms.Model
}

func New(model llms.Model, v *vfs.VFS) *Manager {
	return &Manager{model: model, vfs: v}
}

func (m *Manager) modelFor(ctx context.Context, chatID int64) llms.Model {
	if m.ClientFor != nil {
		if mo := m.ClientFor(ctx, chatID); mo != nil {
			return mo
		}
	}
	return m.model
}

func messageToText(m *messages.Message) string {
	if messages.IsParts(m) {
		var sb strings.Builder
		for _, p := range messages.ContentParts(m) {
			if s := p.Text(); s != "" {
				sb.WriteString(s)
			}
		}
		return sb.String()
	}
	if s, ok := messages.ContentString(m); ok {
		return s
	}
	return ""
}

// UpdateMemory rewrites MEMORY.md for the chat from the recent conversation.
// Returns the new content, or an empty string when nothing was produced.
func (m *Manager) UpdateMemory(ctx context.Context, chatID int64, recent []*messages.Message) (string, error) {
	current := "(kosong)"
	if s, ok := m.vfs.ReadFile(ctx, chatID, "memory/MEMORY.md"); ok {
		current = s
	}

	var lines []string
	start := len(recent) - 12
	if start < 0 {
		start = 0
	}
	for _, msg := range recent[start:] {
		role := msg.Role
		if role == "assistant" {
			role = "AI"
		}
		text := messageToText(msg)
		if len(text) > 2000 {
			text = text[:2000]
		}
		if text != "" {
			lines = append(lines, role+": "+text)
		}
	}
	historyText := strings.Join(lines, "\n")
	if historyText == "" {
		historyText = "(tidak ada)"
	}

	res, err := m.modelFor(ctx, chatID).GenerateContent(ctx, []llms.MessageContent{
		{
			Role:  llms.ChatMessageTypeSystem,
			Parts: []llms.ContentPart{llms.TextContent{Text: memoryPrompt}},
		},
		{
			Role:  llms.ChatMessageTypeHuman,
			Parts: []llms.ContentPart{llms.TextContent{
				Text: "memory/MEMORY.md lama:\n" + current + "\n\nPercakapan terakhir:\n" + historyText,
			}},
		},
	}, llms.WithMaxTokens(memoryMaxOutput), llms.WithStreamingFunc(noopStream))
	if err != nil {
		return "", err
	}
	text := ""
	if res != nil && len(res.Choices) > 0 {
		text = res.Choices[0].Content
	}
	trimmed := strings.TrimSpace(text)
	if trimmed == "" {
		return "", nil
	}
	if err := m.vfs.WriteFile(ctx, chatID, "memory/MEMORY.md", trimmed); err != nil {
		return "", err
	}
	return trimmed, nil
}