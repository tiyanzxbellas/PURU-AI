package app

import (
	"context"
	"errors"
	"fmt"
	"strings"

	"github.com/purujawa/puru-ai/internal/messages"
	"github.com/purujawa/puru-ai/internal/telegram"
	"github.com/purujawa/puru-ai/internal/tokens"
)

const menuText = "*PURU-AI*\n\n" +
	"CMD: /start\nDESC: \"Mulai bot\"\n\n" +
	"CMD: /menu\nDESC: \"Tampilkan menu ini\"\n\n" +
	"CMD: /clear\nDESC: \"Hapus riwayat percakapan\"\n\n" +
	"CMD: /token\nDESC: \"Lihat penggunaan token\"\n\n" +
	"CMD: /info\nDESC: \"Lihat info memori\"\n\n" +
	"CMD: /reset\nDESC: \"Reset semua data (riwayat dan file)\"\n\n" +
	"CMD: /ai <pesan>\nDESC: \"Mengobrol dengan AI (khusus grup)\"\n\n" +
	"---SKILLS-MENU---\n\n" +
	"CMD: /skills\nDESC: \"Lihat daftar skill\"\n\n" +
	"CMD: /skills search <query>\nDESC: \"Cari skill dari GitHub\"\n\n" +
	"CMD: /skills install <url>\nDESC: \"Install skill dari GitHub\"\n\n" +
	"CMD: /skills info <nama>\nDESC: \"Info detail skill\"\n\n" +
	"CMD: /skills read <nama>\nDESC: \"Baca isi skill\"\n\n" +
	"CMD: /skills delete <nama>\nDESC: \"Hapus skill\"\n\n" +
	"CMD: /skills migrate\nDESC: \"Migrate skill lama ke format baru\"\n\n" +
	"Di chat pribadi, kirim pesan langsung untuk mengobrol dengan AI.\nDi grup, gunakan /ai diikuti pesan Anda."

const invalidCommandText = "❌ Perintah tidak dikenal. Gunakan /menu untuk melihat daftar perintah yang tersedia."

// ---------------------------------------------------------------------------
// Text handler
// ---------------------------------------------------------------------------

func (a *App) handleText(ctx context.Context, msg *telegram.Message) error {
	raw := msg.Text
	if strings.HasPrefix(raw, "/ai") {
		rest := strings.TrimSpace(raw[len("/ai"):])
		if rest == "" {
			return a.safeReply(ctx, msg, "Gunakan /ai diikuti pesan, contoh: /ai apa kabar?", true)
		}
		return a.processMessage(ctx, msg, rest)
	}
	if strings.HasPrefix(raw, "/") {
		cmd := strings.Fields(raw)[0]
		if isKnownCommand(cmd) {
			return a.dispatchCommand(ctx, msg)
		}
		if a.isGroup(msg) {
			return nil
		}
		return a.safeReply(ctx, msg, invalidCommandText, true)
	}
	if a.isGroup(msg) {
		return nil
	}
	return a.processMessage(ctx, msg, raw)
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
	cmd := strings.Fields(msg.Text)[0]
	switch cmd {
	case "/start":
		return a.safeReply(ctx, msg, startText, true)
	case "/menu":
		return a.safeReply(ctx, msg, menuText, true)
	case "/clear":
		_ = a.hist.DeleteHistory(ctx, msg.From.ID)
		return a.safeReply(ctx, msg, "Riwayat percakapan telah dihapus!", true)
	case "/reset":
		_ = a.hist.DeleteHistory(ctx, msg.From.ID)
		_ = a.vfs.DeleteAll(ctx, msg.From.ID)
		return a.safeReply(ctx, msg, "🗑️ Semua data Anda (riwayat percakapan & file VFS) telah dihapus.", true)
	case "/token":
		return a.cmdToken(ctx, msg)
	case "/info":
		return a.cmdInfo(ctx, msg)
	case "/skills":
		return a.cmdSkills(ctx, msg)
	}
	return nil
}

const startText = "Halo! Saya PURU-AI 🤖\n\n" +
	"Saya bisa membantu Anda dengan:\n" +
	"• Informasi waktu saat ini\n" +
	"• Perhitungan matematika\n" +
	"• Tanya jawab umum\n\n" +
	"Silakan kirim pesan!"

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

func (a *App) cmdInfo(ctx context.Context, msg *telegram.Message) error {
	args := []string{}
	if m := strings.TrimSpace(msg.Text[len("/info"):]); m != "" {
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

func (a *App) cmdSkills(ctx context.Context, msg *telegram.Message) error {
	userID := msg.From.ID
	rest := strings.TrimSpace(msg.Text[len("/skills"):])
	args := []string{}
	if rest != "" {
		args = strings.Fields(rest)
	}
	if len(args) == 0 {
		list := a.catalog.ListSkills(ctx, userID)
		if len(list) == 0 {
			return a.safeReply(ctx, msg, "Belum ada skill tersimpan.\n\nGunakan:\n/skills search <query> — Mencari skill\n/skills install <url> — Install dari GitHub", true)
		}
		var sb strings.Builder
		for i, s := range list {
			desc := s.Description
			if len(desc) > 50 {
				desc = desc[:50] + "..."
			}
			fmt.Fprintf(&sb, "%d. *%s* — %s\n", i+1, s.Name, desc)
		}
		return a.safeReply(ctx, msg, fmt.Sprintf("📚 *Daftar Skills:*\n\n%s\n\nGunakan:\n/skills info <nama> — Info detail\n/skills read <nama> — Baca isi\n/skills delete <nama> — Hapus", sb.String()), true)
	}

	sub := strings.ToLower(args[0])
	switch sub {
	case "search":
		query := strings.Join(args[1:], " ")
		if query == "" {
			return a.safeReply(ctx, msg, "Gunakan: /skills search <query>", true)
		}
		thID, err := a.sendThinking(ctx, msg, "🔍 Mencari skill...")
		if err != nil {
			return err
		}
		results := a.registry.SearchSkills(ctx, query)
		if len(results) == 0 {
			return a.safeEdit(ctx, msg.Chat.ID, thID, fmt.Sprintf(`Tidak ditemukan skill untuk %q`, query))
		}
		var sb strings.Builder
		for i, r := range results {
			if i >= 10 {
				break
			}
			fmt.Fprintf(&sb, "%d. *%s*\n   %s...\n   %s\n\n", i+1, r.DisplayName, truncateStrIn(r.Summary, 100), r.URL)
		}
		return a.safeEdit(ctx, msg.Chat.ID, thID, fmt.Sprintf("🔍 *Hasil Pencarian \"%s\":*\n\n%s\n\nGunakan /skills install <url> untuk menginstall", query, sb.String()))
	case "install":
		if len(args) < 2 {
			return a.safeReply(ctx, msg, "Gunakan: /skills install <url>\n\nContoh:\n/skills install https://github.com/user/repo\n/skills install user/repo", true)
		}
		thID, err := a.sendThinking(ctx, msg, "📦 Menginstall skill...")
		if err != nil {
			return err
		}
		res := a.registry.InstallFromGitHub(ctx, userID, args[1])
		if res.Success {
			return a.safeEdit(ctx, msg.Chat.ID, thID, fmt.Sprintf("✅ Skill \"%s\" berhasil diinstall!\n\nPath: %s\n\nGunakan /skills info %s untuk melihat detail.", res.Name, res.Path, res.Name))
		}
		return a.safeEdit(ctx, msg.Chat.ID, thID, fmt.Sprintf("❌ Gagal install: %s", res.Error))
	case "info":
		if len(args) < 2 {
			return a.safeReply(ctx, msg, "Gunakan: /skill info <nama>", true)
		}
		name := args[1]
		_, meta, ok := a.catalog.LoadSkillWithMetadata(ctx, userID, name)
		if !ok {
			return a.safeReply(ctx, msg, fmt.Sprintf(`Skill "%s" tidak ditemukan.`, name), true)
		}
		info := fmt.Sprintf("📋 *Info Skill*\n\n*Nama:* %s\n*Deskripsi:* %s\n", meta.Name, meta.Description)
		if meta.Homepage != "" {
			info += fmt.Sprintf("*Homepage:* %s\n", meta.Homepage)
		}
		info += fmt.Sprintf("\nGunakan:\n/skills read %s — Baca isi\n/skills delete %s — Hapus", name, name)
		return a.safeReply(ctx, msg, info, true)
	case "read":
		if len(args) < 2 {
			return a.safeReply(ctx, msg, "Gunakan: /skills read <nama> [file]", true)
		}
		name := args[1]
		if len(args) >= 3 {
			filePath := "skills/" + name + "/" + args[2]
			content, ok := a.vfs.ReadFile(ctx, userID, filePath)
			if !ok {
				return a.safeReply(ctx, msg, fmt.Sprintf(`File "%s" tidak ditemukan di skill "%s".`, args[2], name), true)
			}
			filename := name + "-" + args[2]
			return a.sendDocumentOK(ctx, msg, filename, content)
		}
		content, ok := a.catalog.LoadSkill(ctx, userID, name)
		if !ok {
			return a.safeReply(ctx, msg, fmt.Sprintf(`Skill "%s" tidak ditemukan.`, name), true)
		}
		var caption = name + ".md"
		if files := a.catalog.ListSkillFiles(ctx, userID, name); len(files) > 1 {
			caption += "\n\nFile tersedia:\n• " + strings.Join(files, "\n• ") + "\nGunakan: /skill read " + name + " <file>"
		}
		return a.sendDocumentOK(ctx, msg, name+".md", content)
	case "delete":
		if len(args) < 2 {
			return a.safeReply(ctx, msg, "Gunakan: /skills delete <nama>", true)
		}
		name := args[1]
		deleted, err := a.catalog.DeleteSkill(ctx, userID, name)
		if err != nil {
			return err
		}
		if deleted {
			return a.safeReply(ctx, msg, fmt.Sprintf("🗑️ Skill \"%s\" berhasil dihapus.", name), true)
		}
		return a.safeReply(ctx, msg, fmt.Sprintf(`Skill "%s" tidak ditemukan.`, name), true)
	case "migrate":
		thID, err := a.sendThinking(ctx, msg, "🔄 Migrating skills...")
		if err != nil {
			return err
		}
		migrated, errs := a.registry.MigrateOldSkills(ctx, userID)
		switch {
		case migrated > 0 && len(errs) > 0:
			return a.safeEdit(ctx, msg.Chat.ID, thID, fmt.Sprintf("✅ Berhasil migrate %d skill\n\n⚠️ Errors:\n%s", migrated, strings.Join(errs, "\n")))
		case migrated > 0:
			return a.safeEdit(ctx, msg.Chat.ID, thID, fmt.Sprintf("✅ Berhasil migrate %d skill", migrated))
		case len(errs) > 0:
			return a.safeEdit(ctx, msg.Chat.ID, thID, fmt.Sprintf("❌ Tidak ada skill yang di-migrate:\n%s", strings.Join(errs, "\n")))
		default:
			return a.safeEdit(ctx, msg.Chat.ID, thID, "Tidak ada skill lama yang perlu di-migrate.")
		}
	}
	return a.safeReply(ctx, msg, "Subperintah tidak dikenal.\n\nGunakan:\n/skill — Daftar skill\n/skills search <query> — Cari skill\n/skills install <url> — Install dari GitHub\n/skills info <nama> — Info detail\n/skills read <nama> — Baca isi\n/skills delete <nama> — Hapus\n/skills migrate — Migrate skill lama", true)
}

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
