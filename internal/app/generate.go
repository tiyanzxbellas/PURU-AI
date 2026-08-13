package app

import (
	"context"
	"fmt"
	"log"
	"strings"
	"time"

	"github.com/purujawa06-bot/PURU-AI/internal/ai"
	"github.com/purujawa06-bot/PURU-AI/internal/firebase"
	"github.com/purujawa06-bot/PURU-AI/internal/history"
	"github.com/purujawa06-bot/PURU-AI/internal/messages"
	"github.com/purujawa06-bot/PURU-AI/internal/telegram"
)

// processMessage handles a user message end-to-end.
func (a *App) processMessage(ctx context.Context, msg *telegram.Message, userMessage string) error {
	userID := msg.From.ID
	chatID := msg.Chat.ID

	stored, err := a.hist.GetHistory(ctx, userID)
	if err != nil {
		return err
	}
	hs := messages.CapUserTurns(messages.PruneMessages(stored))

	thID, err := a.sendThinking(ctx, msg, "🤔 PURU-AI sedang berpikir...")
	if err != nil {
		return err
	}

	res := a.agent.ProcessMessage(ctx, userMessage, hs, &ai.ProcessOptions{
		ChatID: userID,
		SendFile: func(content, filename, caption string) error {
			opts := map[string]any{}
			c := caption
			if c == "" {
				c = filename
			}
			opts["caption"] = c
			_, err := a.tg.SendFile(ctx, chatID, filename, []byte(content), "sendDocument", opts)
			return err
		},
		SendBuffer: func(data []byte, filename, caption string) error {
			return a.sendBuffer(ctx, msg, data, filename, caption)
		},
	})

	saved := make([]*messages.Message, 0, len(hs)+1+len(res.ResponseMessages))
	saved = append(saved, hs...)
	user := &messages.Message{Role: "user"}
	messages.SetContentString(user, userMessage)
	saved = append(saved, user)
	saved = append(saved, messages.SanitizeHistoryMessages(res.ResponseMessages)...)

	_ = a.hist.SetTokens(ctx, userID, &history.Tokens{
		Total:  res.LastStepUsage.TotalTokens,
		Input:  res.LastStepUsage.InputTokens,
		Output: res.LastStepUsage.OutputTokens,
	})
	_ = a.hist.SetHistory(ctx, userID, saved)

	if err := a.safeSend(ctx, msg, res.Text); err != nil {
		log.Printf("[app] send final reply failed: %v", err)
	}
	if err := a.tg.DeleteMessage(ctx, chatID, thID); err != nil {
		log.Printf("[app] delete thinking message failed: %v", err)
	}
	a.maybeUpdateMemory(userID, saved)
	return nil
}

// ---------------------------------------------------------------------------
// Document handler
// ---------------------------------------------------------------------------

func (a *App) handleDocument(ctx context.Context, msg *telegram.Message) error {
	userID := msg.From.ID
	chatID := msg.Chat.ID
	if a.isGroup(msg) {
		return nil
	}
	doc := msg.Document
	if doc == nil {
		return nil
	}
	if doc.FileSize > maxUploadSize {
		return a.safeReply(ctx, msg, "⚠️ File terlalu besar untuk diproses (maks 10MB).", true)
	}

	caption := strings.TrimSpace(msg.Caption)
	var vfsPath, prompt string
	if idx := strings.Index(caption, " "); idx > 0 && strings.HasPrefix(caption, "/") {
		vfsPath = strings.TrimPrefix(caption[:idx], "/")
		prompt = strings.TrimSpace(caption[idx+1:])
	} else if strings.HasPrefix(caption, "/") {
		vfsPath = strings.TrimPrefix(caption, "/")
	} else {
		vfsPath = doc.FileName
		if vfsPath == "" {
			vfsPath = "uploaded_file"
		}
		prompt = caption
	}
	vfsPath = firebase.NormalizePath(vfsPath)

	file, err := a.tg.GetFile(ctx, doc.FileID)
	if err != nil || file.FilePath == "" {
		return a.safeReply(ctx, msg, "Gagal mengunduh file.", true)
	}
	data, err := a.tg.DownloadFile(ctx, file.FilePath)
	if err != nil {
		return a.safeReply(ctx, msg, "Gagal mengunduh file.", true)
	}
	fileContent := string(data)

	if err := a.vfs.WriteFile(ctx, userID, vfsPath, fileContent); err != nil {
		log.Printf("[app] vfs write failed: %v", err)
	}
	saveText := fmt.Sprintf("📁 Tersimpan di `/%s`\n\n🤔 PURU-AI sedang memproses...", vfsPath)
	saveRaw, err := a.tg.SendMessage(ctx, chatID, saveText,
		map[string]any{"parse_mode": "Markdown", "reply_to_message_id": msg.MessageID})
	if err != nil {
		return err
	}
	saveID, _ := extractMessageID(saveRaw)

	stored, err := a.hist.GetHistory(ctx, userID)
	if err != nil {
		return err
	}
	hs := messages.CapUserTurns(messages.PruneMessages(stored))

	filePreview := truncateStrIn(fileContent, 4000)
	injected := fmt.Sprintf("[Uploaded file: /%s]\n```\n%s\n```", vfsPath, filePreview)
	if prompt != "" {
		injected += "\n\n" + prompt
	}

	res := a.agent.ProcessMessage(ctx, injected, hs, &ai.ProcessOptions{
		ChatID: userID,
		SendFile: func(content, filename, caption string) error {
			opts := map[string]any{}
			c := caption
			if c == "" {
				c = filename
			}
			opts["caption"] = c
			_, err := a.tg.SendFile(ctx, chatID, filename, []byte(content), "sendDocument", opts)
			return err
		},
		SendBuffer: func(data []byte, filename, caption string) error {
			return a.sendBuffer(ctx, msg, data, filename, caption)
		},
	})

	saved := make([]*messages.Message, 0, len(hs)+1+len(res.ResponseMessages))
	saved = append(saved, hs...)
	userMsg := &messages.Message{Role: "user"}
	messages.SetContentString(userMsg, injected)
	saved = append(saved, userMsg)
	saved = append(saved, messages.SanitizeHistoryMessages(res.ResponseMessages)...)

	_ = a.hist.SetHistory(ctx, userID, saved)
	_ = a.hist.SetTokens(ctx, userID, &history.Tokens{
		Total:  res.LastStepUsage.TotalTokens,
		Input:  res.LastStepUsage.InputTokens,
		Output: res.LastStepUsage.OutputTokens,
	})

	if err := a.safeSend(ctx, msg, res.Text); err != nil {
		log.Printf("[app] send file-process reply failed: %v", err)
	}
	if err := a.tg.DeleteMessage(ctx, chatID, saveID); err != nil {
		log.Printf("[app] delete save message failed: %v", err)
	}
	a.maybeUpdateMemory(userID, saved)
	return nil
}

// ---------------------------------------------------------------------------
// Memory auto-update trigger (fire-and-forget)
// ---------------------------------------------------------------------------

// memoryUpdateTimeout bounds one background MEMORY.md rewrite so a slow memory
// model can never pile up goroutines for the same user.
const memoryUpdateTimeout = 60 * time.Second

// maybeUpdateMemory kicks off a background MEMORY.md refresh. It runs after the
// reply is sent and never holds the per-user busy guard, so a slow memory model
// call does not block the user's next message. The caller no longer waits on it.
func (a *App) maybeUpdateMemory(userID int64, msgs []*messages.Message) {
	if a.mem == nil || a.cfg.MemoryUpdateEvery <= 0 {
		return
	}
	go a.updateMemoryAsync(context.Background(), userID, msgs)
}

// updateMemoryAsync performs the actual rewrite. A per-user mutex serializes
// overlapping refreshes so the turn counter and MEMORY.md stay consistent even
// when the user keeps chatting while a previous update is still running.
// Errors are non-fatal (log only), matching the old behaviour.
func (a *App) updateMemoryAsync(ctx context.Context, userID int64, msgs []*messages.Message) {
	mu := a.memMuFor(userID)
	mu.Lock()
	defer mu.Unlock()

	meta := a.hist.GetMeta(ctx, userID)
	turns := meta.UserTurns + 1
	_ = a.hist.SetMeta(ctx, userID, history.Meta{UserTurns: turns})

	if turns%a.cfg.MemoryUpdateEvery != 0 {
		return
	}
	mctx, cancel := context.WithTimeout(ctx, memoryUpdateTimeout)
	defer cancel()
	if updated, err := a.mem.UpdateMemory(mctx, userID, msgs); err != nil {
		log.Printf("[memory] update failed: %v", err)
	} else if updated != "" {
		log.Printf("Memory updated for user %d (turn %d)", userID, turns)
	}
}

func truncateStrIn(s string, n int) string {
	if len(s) > n {
		return s[:n]
	}
	return s
}
