from telegram import Update
from telegram.ext import ContextTypes
from .metrics import track_message
from config import TOKEN_WARN_LIMIT, TOKEN_COMPACT_LIMIT, TOKEN_BLOCK_LIMIT, MODEL_NAME
from agent import get_context_info, compact_history, clear_history, clear_all_data

async def context_cmd(update: Update, context: ContextTypes.DEFAULT_TYPE) -> None:
    track_message(update.effective_user.id, update.effective_user.username, "context")
    info = get_context_info(update.effective_chat.id)
    used = info["tokens"]
    limit = TOKEN_BLOCK_LIMIT
    pct = (used / limit) * 100
    bar_len = 20
    filled = int(bar_len * used / limit)
    bar = "\u2588" * filled + "\u2591" * (bar_len - filled)
    status = "Normal"
    if used >= TOKEN_COMPACT_LIMIT: status = "\u26a0\ufe0f Auto-compact"
    elif used >= TOKEN_WARN_LIMIT: status = "\u23f3 Getting high"
    elif pct >= 80: status = "\u23f3 Getting high"
    await update.message.reply_text(
        f"*Context Status*\n\nModel: `{MODEL_NAME}`\nTokens used: `{used:,}` / `{limit:,}`\n"
        f"[{bar}] {pct:.1f}%\nStatus: {status}\nMessages: `{info['messages']}`\n\n"
        f"/compact to summarize and free tokens",
        parse_mode="Markdown", quote=True,
    )

async def compact_cmd(update: Update, context: ContextTypes.DEFAULT_TYPE) -> None:
    track_message(update.effective_user.id, update.effective_user.username, "compact")
    await update.message.reply_text("Summarizing conversation history...", quote=True)
    import asyncio
    result = await asyncio.to_thread(compact_history, update.effective_chat.id)
    await update.message.reply_text(result, quote=True)

async def clear(update: Update, context: ContextTypes.DEFAULT_TYPE) -> None:
    track_message(update.effective_user.id, update.effective_user.username, "clear")
    clear_history(update.effective_chat.id)
    await update.message.reply_text("History cleared.", quote=True)

async def clear_all(update: Update, context: ContextTypes.DEFAULT_TYPE) -> None:
    track_message(update.effective_user.id, update.effective_user.username, "clear_all")
    clear_all_data(update.effective_chat.id)
    await update.message.reply_text("Everything wiped clean. Starting fresh! 🧹", quote=True)
