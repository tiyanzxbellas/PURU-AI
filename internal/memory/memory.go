// Package memory auto-updates /memory/MEMORY.md via an internal model call
// (streamText > chat completions with SSE tolerance). It follows the old
// src/memory.ts behaviour: max 10 numbered points, old data freely replaceable.
package memory

import (
	"context"
	"strings"

	"github.com/purujawa06-bot/PURU-AI/internal/ai"
	"github.com/purujawa06-bot/PURU-AI/internal/messages"
	"github.com/purujawa06-bot/PURU-AI/internal/vfs"
)

const memoryMaxOutput = 2000

const memoryPrompt = `You are the memory manager for AI assistant "PURU-AI".

Read the old MEMORY.md and the recent conversation, then write a new MEMORY.md:
- Keep only important facts: user info, decisions, ongoing tasks, preferences, unfinished threads.
- Max 10 numbered points (1-10). Short and clear. Keep 0 to 9 points, the rest can be replaced with new data or removed.
- Output ONLY the MEMORY.md content (markdown, max 2000 chars), no title or preamble.`

type Manager struct {
	client *ai.Client
	vfs    *vfs.VFS
}

func New(client *ai.Client, v *vfs.VFS) *Manager {
	return &Manager{client: client, vfs: v}
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

	text, err := m.client.ChatSystem(ctx, memoryPrompt,
		"memory/MEMORY.md lama:\n"+current+"\n\nPercakapan terakhir:\n"+historyText,
		memoryMaxOutput)
	if err != nil {
		return "", err
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
