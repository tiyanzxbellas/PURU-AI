from telegram import Update
from telegram.ext import ContextTypes
from .metrics import track_message
from firebase import get_fb_file
from config import MEMORY_PATH

async def start(update: Update, context: ContextTypes.DEFAULT_TYPE) -> None:
    track_message(update.effective_user.id, update.effective_user.username, "start")
    await update.message.reply_text(
        "Puru Code - AI Coding Agent\n\n"
        "Send me any coding question or paste code to debug.",
        do_quote=True,
    )

async def menu(update: Update, context: ContextTypes.DEFAULT_TYPE) -> None:
    track_message(update.effective_user.id, update.effective_user.username, "menu")
    await update.message.reply_text(
        "*Puru Code - Menu*\n\n"
        "/start - Welcome message\n"
        "/menu - Show this menu\n"
        "/ai - Ask AI (use in groups)\n"
        "/agents - Lihat daftar agen\n"
        "/context - Token usage info\n"
        "/compact - Summarize context\n"
        "/clear - Clear history\n"
        "/clear\\_all - Wipe everything (history, files, versions)\n"
        "/memory - View saved memory\n\n"
        "_Send any message to chat with AI._\n"
        "_You can also send files — the AI will review them automatically._",
        parse_mode="Markdown",
        do_quote=True,
    )

async def agents_cmd(update: Update, context: ContextTypes.DEFAULT_TYPE) -> None:
    track_message(update.effective_user.id, update.effective_user.username, "agents")
    await update.message.reply_text(
        "*Daftar Agen*\n\n"
        "Bot ini pakai sistem dua agen:\n\n"
        "*Puru (Orchestrator)* — operasi file langsung\n"
        "• *ls* — Lihat daftar file/direktori\n"
        "• *read\\_file* — Baca isi file\n"
        "• *write\\_file* — Tulis/buat file\n"
        "• *edit\\_file* — Edit file (cari & ganti)\n"
        "• *send\\_file* — Kirim file ke chat\n"
        "• *delete\\_file* — Hapus file\n"
        "• *save\\_file* — Simpan file ke Firebase (max 2MB)\n"
        "• *delegate\\_task* — Delegasikan bash/search/download/code ke worker\n\n"
        "*Worker (Executor)*\n"
        "• *bash* — Eksekusi perintah bash\n"
        "• *ls* — Lihat daftar file/direktori\n"
        "• *write\\_file* — Tulis/buat file\n"
        "• *read\\_file* — Baca isi file\n"
        "• *edit\\_file* — Edit file (cari & ganti)\n"
        "• *send\\_file* — Kirim file ke chat (auto-detect audio/video/image)\n"
        "• *delete\\_file* — Hapus file\n"
        "• *save\\_file* — Simpan file ke Firebase (max 2MB)\n\n"
        "_Tinggal bilang aja apa yang lo mau, orchestrator bakal delegasiin tugas ke worker._",
        parse_mode="Markdown",
        do_quote=True,
    )

async def memory_cmd(update: Update, context: ContextTypes.DEFAULT_TYPE) -> None:
    track_message(update.effective_user.id, update.effective_user.username, "memory")
    chat_id = update.effective_chat.id
    content = get_fb_file(chat_id, MEMORY_PATH)
    if content:
        await update.message.reply_text(f"*🧠 Memory*\n\n{content}", parse_mode="Markdown", do_quote=True)
    else:
        await update.message.reply_text("Memori masih kosong. Belum ada data yang disimpan.", do_quote=True)
