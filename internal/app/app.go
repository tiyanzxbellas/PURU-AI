// Package app wires the Telegram update handling together: command dispatch,
// message pipeline (prune / cap / sanitize / token / memory), safe replies and
// file handling — a faithful port of the old src/bot.ts.
package app

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log"
	"strings"
	"sync"
	"time"

	"github.com/purujawa06-bot/PURU-AI/internal/ai"
	"github.com/purujawa06-bot/PURU-AI/internal/auth"
	"github.com/purujawa06-bot/PURU-AI/internal/config"
	"github.com/purujawa06-bot/PURU-AI/internal/history"
	"github.com/purujawa06-bot/PURU-AI/internal/memory"
	"github.com/purujawa06-bot/PURU-AI/internal/messages"
	"github.com/purujawa06-bot/PURU-AI/internal/scheduler"
	"github.com/purujawa06-bot/PURU-AI/internal/settings"
	"github.com/purujawa06-bot/PURU-AI/internal/skills"
	"github.com/purujawa06-bot/PURU-AI/internal/telegram"
	"github.com/purujawa06-bot/PURU-AI/internal/usage"
	"github.com/purujawa06-bot/PURU-AI/internal/vfs"
)

const (
	maxMessageLength = 4096
	maxUploadSize    = 10 * 1024 * 1024
	pipelineUsers    = 5
)

var knownCommands = []string{"/start", "/menu", "/clear", "/token", "/info", "/reset", "/login", "/pw"}

type App struct {
	cfg      *config.Config
	tg       *telegram.API
	hist     *history.Store
	vfs      *vfs.VFS
	agent    *ai.Agent
	mem      *memory.Manager
	catalog  *skills.Catalog
	registry *skills.Registry
	sched    *scheduler.Manager
	// Usage is optional; when set, every model reply's token usage is recorded
	// to the usage store (powers the web dashboard Usage section).
	Usage    *usage.Manager
	Settings *settings.Manager
	Auth     *auth.Manager
	busy     sync.Map // set of user IDs with an in-flight request
	memMu    sync.Map // per-user mutex serializing background memory updates
}

func New(cfg *config.Config, tg *telegram.API, h *history.Store, v *vfs.VFS, a *ai.Agent, m *memory.Manager, c *skills.Catalog, r *skills.Registry, sched *scheduler.Manager) *App {
	app := &App{cfg: cfg, tg: tg, hist: h, vfs: v, agent: a, mem: m, catalog: c, registry: r, sched: sched}
	a.ToolsBuild = func(opts *ai.ProcessOptions) (map[string]*ai.Tool, error) {
		return ai.BuildTools(a, opts), nil
	}
	// Wire scheduler hooks
	if sched != nil {
		a.ScheduleTask = func(ctx context.Context, userID int64, prompt string, runAt int64, tz string) (*scheduler.Task, error) {
			return sched.Schedule(ctx, userID, prompt, runAt, tz)
		}
		a.ListSchedules = func(ctx context.Context, userID int64) ([]*scheduler.Task, error) {
			return sched.List(ctx, userID)
		}
		a.CancelSchedule = func(ctx context.Context, userID int64, id string) error {
			return sched.Cancel(ctx, userID, id)
		}
		sched.SetRunner(app.runScheduled)
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

// memMuFor returns the per-user mutex serializing background memory updates so
// two memory refreshes for the same user can never race on meta/counter.
func (a *App) memMuFor(userID int64) *sync.Mutex {
	v, _ := a.memMu.LoadOrStore(userID, &sync.Mutex{})
	return v.(*sync.Mutex)
}

// Handle dispatches one Telegram update. Processing runs in a goroutine so
// different users are handled in parallel; a second update from the same user
// while one is still in-flight gets a busy reply instead of queueing.
func (a *App) Handle(ctx context.Context, upd *telegram.Update) error {
	if upd.Message == nil || upd.Message.From == nil || upd.Message.Chat == nil {
		return nil
	}
	msg := upd.Message
	if msg.Document == nil && len(msg.Photo) == 0 && msg.Text == "" {
		return nil
	}
	userID := msg.From.ID

	// Non-AI commands run immediately and are never swallowed by the busy
	// guard: they don't touch the per-user AI pipeline. Only plain text, /ai
	// commands, documents and photos are serialized per user.
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
		} else if len(msg.Photo) > 0 {
			err = a.handlePhoto(ctx, msg)
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

// runScheduled executes a scheduled task: runs the prompt through the agent
// and sends the result to the user's private chat (userID == chatID).
func (a *App) runScheduled(ctx context.Context, task *scheduler.Task) {
	userID := task.UserID
	prompt := task.Prompt

	// Create a context with timeout for the scheduled task execution
	taskCtx, cancel := context.WithTimeout(ctx, 30*time.Minute)
	defer cancel()

	// Run the prompt through the agent with empty history (fresh context)
	// but with MEMORY.md injected via system prompt
	res := a.agent.ProcessMessage(taskCtx, prompt, nil, &ai.ProcessOptions{
		ChatID: userID,
		SendFile: func(content, filename, caption string) error {
			opts := map[string]any{}
			c := caption
			if c == "" {
				c = filename
			}
			opts["caption"] = c
			_, err := a.tg.SendFile(taskCtx, userID, filename, []byte(content), "sendDocument", opts)
			return err
		},
		SendBuffer: func(data []byte, filename, caption string) error {
			return a.sendBuffer(taskCtx, &telegram.Message{Chat: &telegram.Chat{ID: userID}}, data, filename, caption)
		},
	})

	// Format result message
	header := "⏰ *Hasil Tugas Terjadwal*\n\n"
	header += fmt.Sprintf("📋 *Prompt:* %s\n\n", prompt)
	header += "🤖 *Hasil:*\n"
	fullText := header + res.Text

	// Send to user's private chat
	if err := a.sendToPrivateChat(taskCtx, userID, fullText); err != nil {
		log.Printf("[scheduler] send result to user %d failed: %v", userID, err)
		task.Error = "Gagal mengirim hasil: " + err.Error()
		return
	}

	task.Result = res.Text
}

// sendToPrivateChat sends a message to user's private chat (userID == chatID).
// Falls back to file if message too long. Uses markdown with fallback.
func (a *App) sendToPrivateChat(ctx context.Context, userID int64, text string) error {
	if len(text) > maxMessageLength {
		// Send short note + file
		note := "⚠️ Respon terlalu panjang, dikirim sebagai file."
		if err := a.sendSimpleMessage(ctx, userID, note); err != nil {
			return err
		}
		_, err := a.tg.SendFile(ctx, userID, "scheduled_result.md", []byte(text), "sendDocument",
			map[string]any{"caption": "Hasil tugas terjadwal terlalu panjang untuk ditampilkan di chat."})
		return err
	}
	return a.sendSimpleMessage(ctx, userID, text)
}

func (a *App) sendSimpleMessage(ctx context.Context, chatID int64, text string) error {
	return a.withMarkdownFallback(func(pm string) error {
		opts := map[string]any{}
		if pm != "" {
			opts["parse_mode"] = pm
		}
		_, err := a.tg.SendMessage(ctx, chatID, text, opts)
		return err
	})
}

// StartScheduler starts the scheduler background loop.
func (a *App) StartScheduler(ctx context.Context) {
	if a.sched != nil {
		a.sched.Start(ctx)
	}
}
