// Package app wires the Telegram update handling together: command dispatch,
// message pipeline (prune / cap / sanitize / token / memory), safe replies and
// file handling — a faithful port of the old src/bot.ts.
package app

import (
	"context"
	"encoding/json"
	"errors"
	"log"
	"strings"
	"sync"

	"github.com/purujawa06-bot/PURU-AI/internal/ai"
	"github.com/purujawa06-bot/PURU-AI/internal/config"
	"github.com/purujawa06-bot/PURU-AI/internal/history"
	"github.com/purujawa06-bot/PURU-AI/internal/memory"
	"github.com/purujawa06-bot/PURU-AI/internal/messages"
	"github.com/purujawa06-bot/PURU-AI/internal/settings"
	"github.com/purujawa06-bot/PURU-AI/internal/skills"
	"github.com/purujawa06-bot/PURU-AI/internal/telegram"
	"github.com/purujawa06-bot/PURU-AI/internal/vfs"
)

const (
	maxMessageLength = 4096
	maxUploadSize    = 10 * 1024 * 1024
	pipelineUsers    = 5
)

var knownCommands = []string{"/start", "/menu", "/clear", "/token", "/info", "/reset", "/skills", "/config"}

type App struct {
	cfg      *config.Config
	tg       *telegram.API
	hist     *history.Store
	vfs      *vfs.VFS
	agent    *ai.Agent
	mem      *memory.Manager
	catalog  *skills.Catalog
	registry *skills.Registry
	Settings *settings.Manager
	busy     sync.Map // set of user IDs with an in-flight request
}

func New(cfg *config.Config, tg *telegram.API, h *history.Store, v *vfs.VFS, a *ai.Agent, m *memory.Manager, c *skills.Catalog, r *skills.Registry) *App {
	app := &App{cfg: cfg, tg: tg, hist: h, vfs: v, agent: a, mem: m, catalog: c, registry: r}
	a.ToolsBuild = func(opts *ai.ProcessOptions) (map[string]*ai.Tool, error) {
		return ai.BuildTools(a, opts), nil
	}
	return app
}

// tryAcquire marks a user as busy; false means the user already has an
// in-flight request and the new one must be rejected.
func (a *App) tryAcquire(userID int64) bool {
	_, loaded := a.busy.LoadOrStore(userID, struct{}{})
	return !loaded
}

func (a *App) release(userID int64) {
	a.busy.Delete(userID)
}

// Handle dispatches one Telegram update. Processing runs in a goroutine so
// different users are handled in parallel; a second update from the same user
// while one is still in-flight gets a busy reply instead of queueing.
func (a *App) Handle(ctx context.Context, upd *telegram.Update) error {
	if upd.Message == nil || upd.Message.From == nil || upd.Message.Chat == nil {
		return nil
	}
	msg := upd.Message
	if msg.Document == nil && msg.Text == "" {
		return nil
	}
	userID := msg.From.ID

	// Non-AI commands run immediately and are never swallowed by the busy
	// guard: they don't touch the per-user AI pipeline. Only plain text, /ai
	// commands and documents are serialized per user.
	if msg.Document == nil && isCommandText(msg.Text) {
		direct, _ := splitCommand(msg.Text)
		if direct != "/ai" {
			go func() {
				if err := a.handleText(ctx, msg); err != nil {
					log.Printf("[app] async command for user %d failed: %v", userID, err)
				}
			}()
			return nil
		}
	}

	if !a.tryAcquire(userID) {
		return a.safeReply(ctx, msg, "⏳ Masih ada yang lagi diproses, tunggu sebentar ya...", true)
	}
	go func() {
		defer a.release(userID)
		var err error
		if msg.Document != nil {
			err = a.handleDocument(ctx, msg)
		} else {
			err = a.handleText(ctx, msg)
		}
		if err != nil {
			log.Printf("[app] async handle for user %d failed: %v", userID, err)
		}
	}()
	return nil
}

func (a *App) isGroup(msg *telegram.Message) bool {
	return msg.Chat.Type == "group" || msg.Chat.Type == "supergroup"
}

// ---------------------------------------------------------------------------
// Long message => file fallback
// ---------------------------------------------------------------------------

type sendTextFn func(parseMode string) error

func (a *App) withMarkdownFallback(fn sendTextFn) error {
	err := fn("Markdown")
	if err == nil {
		return nil
	}
	var te *telegram.TelegramError
	if errors.As(err, &te) && te.Code == 400 && strings.Contains(te.Message, "parse entities") {
		return fn("")
	}
	return err
}

func optsWithReply(replyTo *telegram.Message) map[string]any {
	if replyTo != nil && replyTo.MessageID > 0 {
		return map[string]any{"reply_to_message_id": replyTo.MessageID}
	}
	return map[string]any{}
}

func (a *App) safeReply(ctx context.Context, msg *telegram.Message, text string, replyTo bool) error {
	o := optsWithReply(msg)
	if !replyTo {
		o = map[string]any{}
	}
	return a.withMarkdownFallback(func(pm string) error {
		opts := copyOpts(o)
		if pm != "" {
			opts["parse_mode"] = pm
		}
		_, err := a.tg.SendMessage(ctx, msg.Chat.ID, text, opts)
		return err
	})
}

// safeSend delivers the final AI reply as a brand-new message (replied to the
// user's message). Oversized replies fall back to a short note + a file.
func (a *App) safeSend(ctx context.Context, msg *telegram.Message, text string) error {
	if len(text) > maxMessageLength {
		_ = a.safeReply(ctx, msg, "⚠️ Respon terlalu panjang, dikirim sebagai file.", false)
		_, err := a.tg.SendFile(ctx, msg.Chat.ID, "respon.md", []byte(text), "sendDocument",
			map[string]any{"caption": "Respon lengkap terlalu panjang untuk ditampilkan di chat."})
		return err
	}
	return a.safeReply(ctx, msg, text, true)
}

func (a *App) safeEdit(ctx context.Context, chatID, msgID int64, text string) error {
	if len(text) > maxMessageLength {
		// edit placeholder to a short note, then send the full text as a file
		_ = a.withMarkdownFallback(func(pm string) error {
			opts := map[string]any{}
			if pm != "" {
				opts["parse_mode"] = pm
			}
			_, err := a.tg.EditMessageText(ctx, chatID, msgID, "⚠️ Respon terlalu panjang, dikirim sebagai file.", opts)
			return err
		})
		_, err := a.tg.SendFile(ctx, chatID, "respon.md", []byte(text), "sendDocument",
			map[string]any{"caption": "Respon lengkap terlalu panjang untuk ditampilkan di chat."})
		return err
	}
	return a.withMarkdownFallback(func(pm string) error {
		opts := map[string]any{}
		if pm != "" {
			opts["parse_mode"] = pm
		}
		_, err := a.tg.EditMessageText(ctx, chatID, msgID, text, opts)
		return err
	})
}

func copyOpts(o map[string]any) map[string]any {
	out := make(map[string]any, len(o))
	for k, v := range o {
		out[k] = v
	}
	return out
}

func extractMessageID(raw json.RawMessage) (int64, error) {
	var r struct {
		MessageID int64 `json:"message_id"`
	}
	if err := json.Unmarshal(raw, &r); err != nil {
		return 0, err
	}
	return r.MessageID, nil
}

// sendBuffer dispatches a binary file by extension (audio/video/document).
func (a *App) sendBuffer(ctx context.Context, chat *telegram.Message, data []byte, filename, caption string) error {
	ext := strings.ToLower(extFromName(filename))
	method := "sendDocument"
	switch ext {
	case "mp3", "wav", "flac", "ogg", "m4a", "aac", "wma":
		method = "sendAudio"
	case "mp4", "webm", "avi", "mkv", "mov":
		method = "sendVideo"
	}
	opts := map[string]any{}
	if caption != "" {
		opts["caption"] = caption
	}
	_, err := a.tg.SendFile(ctx, chat.Chat.ID, filename, data, method, opts)
	return err
}

func extFromName(name string) string {
	if i := strings.LastIndex(name, "."); i >= 0 {
		return name[i+1:]
	}
	return ""
}

// ---------------------------------------------------------------------------
// Token formatting
// ---------------------------------------------------------------------------

type msgCounter struct{ user, assistant int }

func countRoles(msgs []*messages.Message) (user, assistant int) {
	for _, m := range msgs {
		if m == nil {
			continue
		}
		if m.Role == "user" {
			user++
		} else if m.Role == "assistant" {
			assistant++
		}
	}
	return
}

func idFormat(n int) string {
	s := itoa(n)
	if len(s) <= 3 {
		return s
	}
	var b strings.Builder
	for i, c := range s {
		if i > 0 && (len(s)-i)%3 == 0 {
			b.WriteByte(',')
		}
		b.WriteRune(c)
	}
	return b.String()
}

func itoa(n int) string {
	if n == 0 {
		return "0"
	}
	neg := n < 0
	if neg {
		n = -n
	}
	var buf [12]byte
	i := len(buf)
	for n > 0 {
		i--
		buf[i] = byte('0' + n%10)
		n /= 10
	}
	if neg {
		i--
		buf[i] = '-'
	}
	return string(buf[i:])
}
