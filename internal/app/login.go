package app

import (
	"context"
	"fmt"
	"strings"

	"github.com/purujawa06-bot/PURU-AI/internal/auth"
	"github.com/purujawa06-bot/PURU-AI/internal/telegram"
)

// ---------------------------------------------------------------------------

func (a *App) cmdLogin(ctx context.Context, msg *telegram.Message) error {
	userID := msg.From.ID
	if a.Auth == nil {
		return a.safeReply(ctx, msg, "Login web tidak tersedia.", true)
	}
	if !a.Auth.Has(ctx, userID) {
		return a.safeReply(ctx, msg, "Belum ada password login web.\n\nKetik `/pw <password>` terlebih dahulu untuk membuat password (minimal 4 karakter, hanya huruf/angka/underscore/dash).", true)
	}

	pw := a.Auth.Get(ctx, userID)
	baseURL := a.cfg.ResolvePublicBaseURL()
	if baseURL == "" {
		return a.safeReply(ctx, msg, "Link login tidak bisa dibuat otomatis di platform ini.\n\nSet environment `PUBLIC_BASE_URL` (mis. `https://bot.example.com`), restart bot, lalu ketik `/login` lagi.", true)
	}
	link := strings.TrimRight(baseURL, "/") + fmt.Sprintf("/login/%d/%s", userID, pw)

	return a.safeReply(ctx, msg, fmt.Sprintf("*Link Login Web*\n\nBuka link ini di browser (mobile friendly):\n\n%s\n\nLink ini rahasia — jangan bagikan ke orang lain. Di sana Anda bisa mengatur API, model, system prompt, dan skills.", link), true)
}

func (a *App) cmdPW(ctx context.Context, msg *telegram.Message, rest string) error {
	userID := msg.From.ID
	if a.Auth == nil {
		return a.safeReply(ctx, msg, "Login web tidak tersedia.", true)
	}

	pw := strings.TrimSpace(rest)
	if pw == "" {
		return a.safeReply(ctx, msg, "*Set Password Login Web*\n\nGunakan: `/pw <password>`\n\nAturan:\n- Minimal 4 karakter\n- Hanya huruf, angka, underscore (`_`), atau dash (`-`)\n\nContoh: `/pw MyBot123`\n\nSetelah password diset, ketik `/login` untuk mendapatkan link halaman pengaturan.", true)
	}

	if !auth.ValidPassword(pw) {
		return a.safeReply(ctx, msg, "Password tidak valid. Minimal 4 karakter, hanya huruf (a-z, A-Z), angka (0-9), underscore (`_`), atau dash (`-`).", true)
	}

	if err := a.Auth.Set(ctx, userID, pw); err != nil {
		return a.safeReply(ctx, msg, "Gagal menyimpan password: "+err.Error(), true)
	}

	return a.safeReply(ctx, msg, "Password tersimpan!\n\nKetik `/login` untuk mendapatkan link halaman pengaturan.", true)
}
