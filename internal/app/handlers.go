package app

import (
	"context"
	"errors"
	"fmt"
	"strings"

	"github.com/purujawa06-bot/PURU-AI/internal/messages"
	"github.com/purujawa06-bot/PURU-AI/internal/telegram"
	"github.com/purujawa06-bot/PURU-AI/internal/tokens"
)

const menuText = "*PURU-AI*\n\n" +
	"*Perintah:*\n" +
	"/ai <pesan> — Mengobrol dengan AI (khusus grup)\n" +
	"/clear — Hapus riwayat percakapan\n" +
	"/reset chat — Reset riwayat & file\n" +
	"/pw <password> — Set password login web\n" +
	"/login — Dapatkan link halaman pengaturan\n\n" +
	"Di chat pribadi, kirim pesan langsung untuk mengobrol dengan AI.\nDi grup, gunakan /ai diikuti pesan Anda.\n\n" +
	"Pengaturan API, model, system prompt, dan skills — semua melalui halaman web (/login)."

const invalidCommandText = "❌ Perintah tidak dikenal. Gunakan /menu untuk melihat daftar perintah yang tersedia."

// splitCommand splits a message into its leading command token — with any
// "@bot" handle stripped ("/menu@my_bot" → "/menu") — and the rest of the
// message. For non-command text the whole line is cmd and rest.
func splitCommand(text string) (cmd, rest string) {
	fields := strings.Fields(text)
	if len(fields) == 0 {
		return "", ""
	}
	token := fields[0]
	if i := strings.IndexByte(token, '@'); i > 0 {
		token = token[:i]
	}
	return token, strings.TrimSpace(strings.TrimPrefix(text, fields[0]))
}

// isCommandText reports whether text begins with a "/" command token. Used to
// decide whether a message bypasses the per-user busy guard.
func isCommandText(text string) bool {
	cmd, _ := splitCommand(text)
	return strings.HasPrefix(cmd, "/")
}

// ---------------------------------------------------------------------------
// Text handler
// ---------------------------------------------------------------------------

func (a *App) handleText(ctx context.Context, msg *telegram.Message) error {
	cmd, rest := splitCommand(msg.Text)
	if cmd == "/ai" {
		// /ai hanya untuk grup; di chat pribadi semua pesan sudah otomatis
		// diproses sehingga perintahnya dilarang.
		if !a.isGroup(msg) {
			return a.safeReply(ctx, msg, "Tidak perlu /ai di chat pribadi - langsung kirim pesan saja untuk mengobrol dengan AI.", true)
		}
		if rest == "" {
			return a.safeReply(ctx, msg, "Gunakan /ai diikuti pesan, contoh: /ai apa kabar?", true)
		}
		return a.processMessage(ctx, msg, rest)
	}
	if strings.HasPrefix(cmd, "/") {
		if isKnownCommand(cmd) {
			return a.dispatchCommand(ctx, msg)
		}
		if cmd == "/config" || cmd == "/skills" {
			return a.safeReply(ctx, msg, "Perintah ini sudah dipindah ke halaman web.\n\nKetik /login untuk mendapatkan link pengaturan.", true)
		}
		if a.isGroup(msg) {
			return nil
		}
		return a.safeReply(ctx, msg, invalidCommandText, true)
	}
	if a.isGroup(msg) {
		return nil
	}
	return a.processMessage(ctx, msg, msg.Text)
}

func isKnownCommand(cmd string) bool {
	for _, c := range knownCommands {
		if c == cmd {
			return true
		}
	}
	return false
}

// ---------------------------------------------------------------------------
// Commands
// ---------------------------------------------------------------------------

func (a *App) dispatchCommand(ctx context.Context, msg *telegram.Message) error {
	cmd, rest := splitCommand(msg.Text)
	switch cmd {
	case "/start":
		return a.safeReply(ctx, msg, startText, true)
	case "/menu":
		return a.safeReply(ctx, msg, menuText, true)
	case "/clear":
		_ = a.hist.DeleteHistory(ctx, msg.From.ID)
		return a.safeReply(ctx, msg, "Riwayat percakapan telah dihapus!", true)
	case "/reset":
		return a.cmdReset(ctx, msg, rest)
	case "/login":
		return a.cmdLogin(ctx, msg)
	case "/pw":
		return a.cmdPW(ctx, msg, rest)
	case "/token":
		return a.cmdToken(ctx, msg)
	case "/info":
		return a.cmdInfo(ctx, msg, rest)
	}
	return nil
}

const startText = "Halo! Saya PURU-AI 🤖\n\n" +
	"Ketik /menu untuk melihat daftar perintah.\n" +
	"Ketik /login untuk membuka halaman pengaturan web."

func (a *App) cmdToken(ctx context.Context, msg *telegram.Message) error {
	userID := msg.From.ID
	lastStep := a.hist.GetTokens(ctx, userID)
	hs, err := a.hist.GetHistory(ctx, userID)
	if err != nil {
		return err
	}
	if len(hs) == 0 && lastStep == nil {
		return a.safeReply(ctx, msg, "Belum ada riwayat percakapan.", true)
	}

	userCount, assistantCount := countRoles(hs)
	rawTokens := tokens.CountConvTokens(hs)
	postTokens := tokens.CountConvTokens(messages.CapUserTurns(messages.PruneMessages(hs)))

	reply := "📊 *Penggunaan Token*\n\n" +
		fmt.Sprintf("👤 User: %d pesan\n", userCount) +
		fmt.Sprintf("🤖 Assistant: %d pesan\n", assistantCount) +
		fmt.Sprintf("📜 History (raw): %s token\n", idFormat(rawTokens)) +
		fmt.Sprintf("✂️ History (post-prune): %s token\n", idFormat(postTokens))
	if lastStep != nil {
		reply += fmt.Sprintf("🔢 Last step: %s token (input: %s + output: %s)\n\n",
			idFormat(lastStep.Total), idFormat(lastStep.Input), idFormat(lastStep.Output))
	}
	reply += "_ℹ️ History di-prune & dibatasi maksimal 5 pesan user sebelum request. Estimasi tidak termasuk system prompt._"
	return a.safeReply(ctx, msg, reply, true)
}

func (a *App) cmdInfo(ctx context.Context, msg *telegram.Message, rest string) error {
	args := []string{}
	if m := strings.TrimSpace(rest); m != "" {
		args = strings.Fields(m)
	}
	arg := ""
	if len(args) > 0 {
		arg = strings.ToLower(args[0])
	}
	if arg == "" {
		_, has := a.vfs.ReadFile(ctx, msg.From.ID, "memory/MEMORY.md")
		status := "kosong"
		if has {
			status = "ada"
		}
		return a.safeReply(ctx, msg, fmt.Sprintf("📁 *Info Memory*\n\n• /info memory — %s\n\nGunakan:\n/info memory — Isi MEMORY.md", status), true)
	}
	if arg != "memory" {
		return a.safeReply(ctx, msg, "Subperintah tidak dikenal.\n\nGunakan:\n/info memory — Isi MEMORY.md", true)
	}
	content, ok := a.vfs.ReadFile(ctx, msg.From.ID, "memory/MEMORY.md")
	if !ok {
		return a.safeReply(ctx, msg, "Belum ada MEMORY.md.", true)
	}
	return a.safeReply(ctx, msg, fmt.Sprintf("📄 *MEMORY.md*\n\n%s", content), true)
}

// cmdSkills removed — skill management is now via the /login web page.

func (a *App) sendThinking(ctx context.Context, msg *telegram.Message, text string) (int64, error) {
	id, err := a.sendThinkingCmd(ctx, msg.Chat.ID, text, optsWithReply(msg))
	if err != nil {
		var te *telegram.TelegramError
		if errors.As(err, &te) && te.Code == 400 && strings.Contains(te.Message, "message to be replied not found") {
			return a.sendThinkingCmd(ctx, msg.Chat.ID, text, map[string]any{})
		}
		return 0, err
	}
	return id, nil
}

func (a *App) sendThinkingCmd(ctx context.Context, chatID int64, text string, opts map[string]any) (int64, error) {
	opts["parse_mode"] = "Markdown"
	raw, err := a.tg.SendMessage(ctx, chatID, text, opts)
	if err != nil {
		return 0, err
	}
	return extractMessageID(raw)
}

func (a *App) sendDocumentOK(ctx context.Context, msg *telegram.Message, filename, content string) error {
	_, err := a.tg.SendFile(ctx, msg.Chat.ID, filename, []byte(content), "sendDocument", map[string]any{"caption": filename})
	return err
}
