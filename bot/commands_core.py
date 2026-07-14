from telegram import Update
from telegram.ext import ContextTypes
from .metrics import track_message

async def start(update: Update, context: ContextTypes.DEFAULT_TYPE) -> None:
    track_message(update.effective_user.id, update.effective_user.username, "start")
    await update.message.reply_text(
        "Puru Code - AI Coding Agent\n\n"
        "Send me any coding question or paste code to debug.",
        quote=True,
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
        "/clear\_all - Wipe everything (history, files, versions)\n\n"
        "_Send any message to chat with AI._\n"
        "_You can also send files — the AI will review them automatically._",
        parse_mode="Markdown",
        quote=True,
    )

async def tools_cmd(update: Update, context: ContextTypes.DEFAULT_TYPE) -> None:
    track_message(update.effective_user.id, update.effective_user.username, "tools")
    await update.message.reply_text(
        "*Available Tools*\n\n"
        "The AI agent can use these tools in the sandbox:\n\n"
        "• *bash* — Execute bash commands\n"
        "• *write\_file* — Write/create files\n"
        "• *read\_file* — Read file contents\n"
        "• *edit\_file* — Edit files (find & replace)\n"
        "• *delete\_file* — Delete files\n"
        "• *send\_file* — Send files to chat (auto-detects audio/video/image)\n"
        "• *save\_file* — Save file to Firebase permanently (max 2MB)\n\n"
        "_Just describe what you want to build or debug, and the AI will use these tools automatically._",
        parse_mode="Markdown",
        quote=True,
    )
