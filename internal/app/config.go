package app

import (
	"context"
	"strings"

	"github.com/purujawa06-bot/PURU-AI/internal/history"
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
		return a.safeReply(ctx, msg, "🗑️ Pengaturan API sendiri telah dihapus. Sekarang memakai API default server.", true)
	case "memory":
		_, _ = a.vfs.DeleteFile(ctx, msg.From.ID, "memory/MEMORY.md")
		_ = a.hist.SetMeta(ctx, msg.From.ID, history.Meta{})
		return a.safeReply(ctx, msg, "🗑️ MEMORY.md (ingatan user) telah dihapus.", true)
	case "chat":
		_ = a.hist.DeleteHistory(ctx, msg.From.ID)
		_ = a.vfs.DeleteAll(ctx, msg.From.ID)
		return a.safeReply(ctx, msg, "🗑️ Riwayat percakapan & file VFS telah dihapus.", true)
	}
	return a.safeReply(ctx, msg, resetHelpText, true)
}
