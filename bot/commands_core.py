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
        "/tools - Show available tools\n"
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

async def tools_cmd(update: Update, context: ContextTypes.DEFAULT_TYPE) -> None:
    track_message(update.effective_user.id, update.effective_user.username, "tools")
    await update.message.reply_text(
        "*Available Tools*\n\n"
        "The AI uses a two-agent system:\n\n"
        "*Puru (Orchestrator)* — file operations langsung\n"
        "• *ls* — List files/directories\n"
        "• *read\\_file* — Read file contents\n"
        "• *write\\_file* — Write/create files\n"
        "• *edit\\_file* — Edit files (find & replace)\n"
        "• *delegate\\_task* — Delegate bash/search/download/code ke worker\n\n"
        "*Worker (Executor)*\n"
        "• *bash* — Execute bash commands\n"
        "• *write\\_file* — Write/create files\n"
        "• *read\\_file* — Read file contents\n"
        "• *edit\\_file* — Edit files (find & replace)\n"
        "• *delete\\_file* — Delete files\n"
        "• *send\\_file* — Send files to chat (auto-detects audio/video/image)\n"
        "• *save\\_file* — Save file to Firebase permanently (max 2MB)\n\n"
        "_Just describe what you want, the orchestrator delegates tasks to the worker._",
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
