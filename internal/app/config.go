package app

import (
	"context"
	"strings"
	"time"

	"github.com/tmc/langchaingo/llms"

	"github.com/purujawa06-bot/PURU-AI/internal/history"
	"github.com/purujawa06-bot/PURU-AI/internal/settings"
	"github.com/purujawa06-bot/PURU-AI/internal/telegram"
)

// ---------------------------------------------------------------------------
// /reset — targeted resets: config, memory, chat
// ---------------------------------------------------------------------------

const resetHelpText = "🗑️ *Reset Data*\n\n" +
	"Pilih bagian yang ingin di-reset:\n\n" +
	"`/reset config` — Hapus pengaturan API sendiri (kembali ke default server)\n" +
	"`/reset memory` — Hapus MEMORY.md (ingatan user)\n" +
	"`/reset chat` — Hapus riwayat percakapan & file VFS"

func (a *App) cmdReset(ctx context.Context, msg *telegram.Message, rest string) error {
	args := []string{}
	if r := strings.TrimSpace(rest); r != "" {
		args = strings.Fields(r)
	}
	if len(args) == 0 {
		return a.safeReply(ctx, msg, resetHelpText, true)
	}

	switch strings.ToLower(args[0]) {
	case "config":
		if a.Settings != nil {
			_ = a.Settings.Delete(ctx, msg.From.ID)
		}
		return a.safeReply(ctx, msg, "⚙️ Pengaturan API sendiri telah dihapus. Sekarang memakai API default server.", true)
	case "memory":
		_, _ = a.vfs.DeleteFile(ctx, msg.From.ID, "memory/MEMORY.md")
		_ = a.hist.SetMeta(ctx, msg.From.ID, history.Meta{})
		return a.safeReply(ctx, msg, "🧠 MEMORY.md (ingatan user) telah dihapus.", true)
	case "chat":
		_ = a.hist.DeleteHistory(ctx, msg.From.ID)
		_ = a.vfs.DeleteAll(ctx, msg.From.ID)
		return a.safeReply(ctx, msg, "🗑️ Riwayat percakapan & file VFS telah dihapus.", true)
	}
	return a.safeReply(ctx, msg, resetHelpText, true)
}

// ---------------------------------------------------------------------------
// /config — per-user AI API settings
// ---------------------------------------------------------------------------

const configHelpText = "⚙️ *Konfigurasi API Pribadi*\n\n" +
	"Gunakan /config untuk melihat status, atau:\n\n" +
	"`/config api <api_key>` — Set API key sendiri\n" +
	"`/config model <nama_model>` — Set nama model\n" +
	"`/config base <url>` — Set base URL (mis. `https://api.openai.com/v1`)\n" +
	"`/config clear` — Hapus semua pengaturan (kembali ke default server)\n" +
	"`/config test` — Tes koneksi ke API yang dipakai\n\n" +
	"__Data tersimpan per user di Firebase. Field yang kosong memakai default server.__"

func (a *App) cmdConfig(ctx context.Context, msg *telegram.Message, rest string) error {
	userID := msg.From.ID
	if a.Settings == nil {
		return a.safeReply(ctx, msg, "Konfigurasi API pribadi tidak aktif.", true)
	}

	args := []string{}
	if r := strings.TrimSpace(rest); r != "" {
		args = strings.Fields(r)
	}
	if len(args) == 0 {
		return a.safeReply(ctx, msg, a.configStatus(ctx, userID), true)
	}

	sub := strings.ToLower(args[0])
	switch sub {
	case "api", "key", "apikey":
		return a.configSet(ctx, msg, args, 1, func(c *settings.Config, v string) { c.APIKey = &v })
	case "model":
		return a.configSet(ctx, msg, args, 1, func(c *settings.Config, v string) { c.Model = &v })
	case "base", "baseurl", "base_url":
		return a.configSet(ctx, msg, args, 1, func(c *settings.Config, v string) { c.BaseURL = &v })
	case "clear", "unset", "delete":
		if len(args) >= 2 {
			field := strings.ToLower(args[1])
			_ = a.Settings.ClearField(ctx, userID, field)
			return a.safeReply(ctx, msg, "⚙️ Field `"+field+"` dihapus dari pengaturan API Anda.", true)
		}
		_ = a.Settings.Delete(ctx, userID)
		return a.safeReply(ctx, msg, "⚙️ Pengaturan API sendiri telah dihapus. Sekarang memakai API default server.", true)
	case "test":
		return a.configTest(ctx, msg, userID)
	}
	return a.safeReply(ctx, msg, configHelpText, true)
}

func (a *App) configSet(ctx context.Context, msg *telegram.Message, args []string, valueIndex int, apply func(*settings.Config, string)) error {
	if len(args) <= valueIndex || strings.TrimSpace(args[valueIndex]) == "" {
		return a.safeReply(ctx, msg, "❌ Nilai tidak lengkap.\n\n"+configHelpText, true)
	}
	cfg := a.Settings.Get(ctx, msg.From.ID)
	if cfg == nil {
		cfg = &settings.Config{}
	}
	apply(cfg, args[valueIndex])
	if err := a.Settings.Set(ctx, msg.From.ID, cfg); err != nil {
		return a.safeReply(ctx, msg, "❌ Gagal menyimpan pengaturan: "+err.Error(), true)
	}
	return a.safeReply(ctx, msg, "✅ Pengaturan API tersimpan!\n\n"+a.configStatus(ctx, msg.From.ID), true)
}

func (a *App) configStatus(ctx context.Context, userID int64) string {
	user := a.Settings.Get(ctx, userID)
	eff := settings.Effective(a.cfg.AI, user)

	status := "⚙️ *Konfigurasi API*\n\n"
	if user == nil {
		status += "• Sumber: **default server**\n"
	} else {
		status += "• Sumber: **API sendiri**\n"
	}

	var missing []string
	status += "• Base URL: `" + eff.BaseURL + "`"
	switch {
	case user != nil && user.BaseURL != nil:
		status += " _(kustom)_\n"
	default:
		status += " _(default server)_\n"
		if user != nil {
			missing = append(missing, "base")
		}
	}

	status += "• Model: `" + eff.Model + "`"
	switch {
	case user != nil && user.Model != nil:
		status += " _(kustom)_\n"
	default:
		status += " _(default server)_\n"
		if user != nil {
			missing = append(missing, "model")
		}
	}

	switch {
	case user != nil && user.APIKey != nil:
		status += "• API Key: `" + maskKey(eff.APIKey) + "` _(kustom)_\n"
	case eff.APIKey != "":
		status += "• API Key: `" + maskKey(eff.APIKey) + "` _(default server)_\n"
		if user != nil {
			missing = append(missing, "api")
		}
	default:
		status += "• API Key: _(kosong)_\n"
	}

	if user != nil && len(missing) > 0 {
		status += "\n⚠️ Field yang belum diisi memakai default server: /config api • /config base • /config model\n"
	}

	status += "\n" + "Gunakan:\n/config api <key> • /config model <nama> • /config base <url>\n/config clear — kembali ke default\n/config test — tes koneksi"
	return status
}

// clientForUser returns the model for a user (their own config when set),
// falling back to the shared default model.
func (a *App) clientForUser(ctx context.Context, userID int64) llms.Model {
	if a.agent != nil && a.agent.ClientFor != nil {
		if m := a.agent.ClientFor(ctx, userID); m != nil {
			return m
		}
	}
	if a.agent != nil {
		return a.agent.Client
	}
	return nil
}

func (a *App) configTest(ctx context.Context, msg *telegram.Message, userID int64) error {
	model := a.clientForUser(ctx, userID)
	if model == nil {
		return a.safeReply(ctx, msg, "Tidak dapat membuat klien AI.", true)
	}
	thID, err := a.sendThinking(ctx, msg, "🔌 Menghubungi API...")
	if err != nil {
		return err
	}
	tctx, cancel := context.WithTimeout(ctx, 30*time.Second)
	defer cancel()
	res, err := model.GenerateContent(tctx, []llms.MessageContent{
		{
			Role:  llms.ChatMessageTypeHuman,
			Parts: []llms.ContentPart{llms.TextContent{Text: "You are a connectivity check. Reply with exactly: OK"}},
		},
	}, llms.WithMaxTokens(10), llms.WithStreamingFunc(func(context.Context, []byte) error { return nil }))
	if err != nil {
		return a.safeEdit(ctx, msg.Chat.ID, thID, "❌ *Gagal koneksi:*\n"+err.Error())
	}
	text := ""
	if res != nil && len(res.Choices) > 0 {
		text = res.Choices[0].Content
	}
	return a.safeEdit(ctx, msg.Chat.ID, thID, "✅ *Koneksi berhasil!* API merespons:\n\n"+text)
}

func maskKey(key string) string {
	if len(key) <= 8 {
		return strings.Repeat("•", len(key))
	}
	return key[:4] + "…" + key[len(key)-4:]
}
