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
	"github.com/purujawa06-bot/PURU-AI/internal/settings"
	"github.com/purujawa06-bot/PURU-AI/internal/telegram"
	"github.com/purujawa06-bot/PURU-AI/internal/usage"
)

// recordUsage persists the last-step token usage of a model reply to the usage
// store (per-user web dashboard). Provider label comes from the effective base
// URL so the dashboard groups by provider like 9router's usage page.
func (a *App) recordUsage(ctx context.Context, userID int64, res *ai.ProcessResult) {
	if a.Usage == nil || res == nil || res.LastStepUsage.TotalTokens <= 0 {
		return
	}
	modelName := a.cfg.AI.Model
	baseURL := a.cfg.AI.BaseURL
	if a.Settings != nil {
		if u := a.Settings.Get(ctx, userID); u != nil {
			eff := settings.Effective(a.cfg.AI, u)
			modelName = eff.Model
			baseURL = eff.BaseURL
		}
	}
	_ = a.Usage.Add(ctx, userID, usage.ProviderLabel(baseURL), modelName, res.LastStepUsage.InputTokens, res.LastStepUsage.OutputTokens)
}

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
	a.recordUsage(ctx, userID, res)
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
// Document / photo handler
// ---------------------------------------------------------------------------

// handleDocument processes a user-uploaded document. Images are never stored
// to VFS: they are summarised by the vision model and injected into the agent
// via processImage. Non-image files are stored to VFS (path-only injection).
func (a *App) handleDocument(ctx context.Context, msg *telegram.Message) error {
	userID := msg.From.ID
	chatID := msg.Chat.ID
	caption := strings.TrimSpace(msg.Caption)
	// Di grup, file hanya diproses bila caption diawali "/ai" (mis. caption
	// "/ai analisis file ini"). File lain di grup diabaikan agar file tanpa
	// izin tidak masuk pipeline AI. Prefix "/ai" di-strip supaya parsing
	// vfsPath/prompt di bawah bekerja normal.
	if a.isGroup(msg) {
		if !strings.HasPrefix(caption, "/ai") {
			return nil
		}
		caption = strings.TrimSpace(strings.TrimPrefix(caption, "/ai"))
	}
	doc := msg.Document
	if doc == nil {
		return nil
	}
	if doc.FileSize > maxUploadSize {
		return a.safeReply(ctx, msg, "⚠️ File terlalu besar untuk diproses (maks 10MB).", true)
	}

	file, err := a.tg.GetFile(ctx, doc.FileID)
	if err != nil || file.FilePath == "" {
		return a.safeReply(ctx, msg, "Gagal mengunduh file.", true)
	}
	data, err := a.tg.DownloadFile(ctx, file.FilePath)
	if err != nil {
		return a.safeReply(ctx, msg, "Gagal mengunduh file.", true)
	}

	// Gambar tidak pernah ditulis ke VFS: minta ringkasan ke model visi lalu
	// inject hasilnya bersama prompt user dalam marker [context].
	if ai.IsImageContent(data) {
		return a.processImage(ctx, msg, data, caption)
	}

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

	injected := injectUploadedFile(vfsPath, prompt)
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
	a.recordUsage(ctx, userID, res)

	if err := a.safeSend(ctx, msg, res.Text); err != nil {
		log.Printf("[app] send file-process reply failed: %v", err)
	}
	if err := a.tg.DeleteMessage(ctx, chatID, saveID); err != nil {
		log.Printf("[app] delete save message failed: %v", err)
	}
	a.maybeUpdateMemory(userID, saved)
	return nil
}

// handlePhoto processes a native Telegram photo message (not sent as a
// document). The largest resolution is downloaded and processed exactly like
// an image document: summarised by the vision model, never stored to VFS.
// In groups a photo is only processed when its caption starts with "/ai".
func (a *App) handlePhoto(ctx context.Context, msg *telegram.Message) error {
	caption := strings.TrimSpace(msg.Caption)
	if a.isGroup(msg) {
		if !strings.HasPrefix(caption, "/ai") {
			return nil
		}
		caption = strings.TrimSpace(strings.TrimPrefix(caption, "/ai"))
	}
	photo := largestPhoto(msg.Photo)
	if photo == nil {
		return nil
	}
	if photo.FileSize > maxUploadSize {
		return a.safeReply(ctx, msg, "⚠️ Foto terlalu besar untuk diproses (maks 10MB).", true)
	}
	file, err := a.tg.GetFile(ctx, photo.FileID)
	if err != nil || file.FilePath == "" {
		return a.safeReply(ctx, msg, "Gagal mengunduh foto.", true)
	}
	data, err := a.tg.DownloadFile(ctx, file.FilePath)
	if err != nil {
		return a.safeReply(ctx, msg, "Gagal mengunduh foto.", true)
	}
	return a.processImage(ctx, msg, data, caption)
}

// processImage handles an image upload (photo or image document) end-to-end:
// the image is never written to VFS — instead the vision model summarises it
// (description + any visible text) and the summary is injected together with
// the user's prompt inside a [context] marker.
func (a *App) processImage(ctx context.Context, msg *telegram.Message, data []byte, prompt string) error {
	userID := msg.From.ID
	chatID := msg.Chat.ID

	thID, err := a.sendThinking(ctx, msg, "🤔 PURU-AI sedang melihat gambar...")
	if err != nil {
		return err
	}

	endpoint := ""
	if a.cfg != nil {
		endpoint = a.cfg.VisionModelURL
	}
	summary, err := ai.DescribeImage(ctx, a.agent.HTTP, endpoint, imageVisionPrompt, data, "")
	if err != nil {
		_ = a.tg.DeleteMessage(ctx, chatID, thID)
		return a.safeReply(ctx, msg, "⚠️ Gagal menganalisis gambar: "+err.Error(), true)
	}
	if len(summary) > maxImageContextChars {
		summary = summary[:maxImageContextChars] + "\n…[hasil dipotong]"
	}
	injected := buildImageContext(summary, prompt)

	stored, err := a.hist.GetHistory(ctx, userID)
	if err != nil {
		return err
	}
	hs := messages.CapUserTurns(messages.PruneMessages(stored))

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
	a.recordUsage(ctx, userID, res)

	if err := a.safeSend(ctx, msg, res.Text); err != nil {
		log.Printf("[app] send image reply failed: %v", err)
	}
	if err := a.tg.DeleteMessage(ctx, chatID, thID); err != nil {
		log.Printf("[app] delete thinking message failed: %v", err)
	}
	a.maybeUpdateMemory(userID, saved)
	return nil
}

// imageVisionPrompt asks the vision model to summarise an uploaded image and
// report any text shown in it, so the agent gets a self-contained context.
const imageVisionPrompt = "Ringkas gambar ini secara singkat dalam Bahasa Indonesia, lalu sebutkan teks apa saja yang terlihat atau ditampilkan di dalam gambar tersebut."

// maxImageContextChars caps how much vision-model summary is injected, keeping
// a single image context within the same order of magnitude as a chat message.
const maxImageContextChars = 4_000

// buildImageContext wraps a vision-model summary and the user's prompt inside
// the [context] marker injected into the agent as the user message.
func buildImageContext(summary, prompt string) string {
	injected := "[context]\n" + strings.TrimSpace(summary)
	if p := strings.TrimSpace(prompt); p != "" {
		injected += "\n\n" + p
	}
	return injected
}

// injectUploadedFile builds the user-message body injected into the agent for
// a stored (non-image) user-uploaded file: a clean VFS path pointer plus the
// user's prompt. Content is never previewed — the AI reads the file itself via
// read_file. Images never reach VFS; they go through processImage instead.
func injectUploadedFile(vfsPath, prompt string) string {
	injected := fmt.Sprintf("[User mengupload file: /%s]", vfsPath)
	if prompt != "" {
		injected += "\n\n" + prompt
	}
	return injected
}

// largestPhoto returns the highest-resolution entry of a Telegram photo
// message (largest by pixel area, falling back to file size).
func largestPhoto(photos []telegram.PhotoSize) *telegram.PhotoSize {
	var best *telegram.PhotoSize
	for i := range photos {
		p := &photos[i]
		if best == nil || photoArea(p) > photoArea(best) {
			best = p
		}
	}
	return best
}

func photoArea(p *telegram.PhotoSize) int64 {
	if p.Width > 0 && p.Height > 0 {
		return int64(p.Width) * int64(p.Height)
	}
	return p.FileSize
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
