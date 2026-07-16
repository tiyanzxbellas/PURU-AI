import logging
from telegram import Update
from telegram.ext import ContextTypes
from .metrics import track_message

from sandbox import get_sandbox, close_sandbox
from .ai_handler import run_with_tools

logger = logging.getLogger(__name__)

async def ai_ask(update: Update, context: ContextTypes.DEFAULT_TYPE) -> None:
    if not update.message or not update.message.text: return
    user = update.effective_user
    track_message(user.id, user.username, "ai")
    parts = update.message.text.split(maxsplit=1)
    prompt = parts[1] if len(parts) > 1 else ""
    if not prompt:
        await update.message.reply_text("Usage: /ai <your question>", do_quote=True)
        return
    await run_with_tools(update, prompt)

async def handle_document(update: Update, context: ContextTypes.DEFAULT_TYPE) -> None:
    if not update.message or not update.message.document: return
    user = update.effective_user
    track_message(user.id, user.username)
    chat_id = update.effective_chat.id
    doc = update.message.document
    file_name = doc.file_name or "unknown_file"
    caption = update.message.caption or ""
    try:
        tg_file = await doc.get_file()
        file_bytes = await tg_file.download_as_bytearray()
    except Exception as e:
        logger.warning("Failed to download file: %s", e)
        await update.message.reply_text(f"Failed to download file: {e}", do_quote=True)
        return
    file_path = f"/home/user/uploads/{file_name}"
    success = False
    for attempt in range(2):
        try:
            sandbox = get_sandbox(chat_id)
            sandbox.files.write(file_path, bytes(file_bytes))
            success = True
            break
        except Exception as e:
            if "sandbox" in str(e).lower():
                close_sandbox(chat_id)
                continue
            logger.warning("Failed to upload file: %s", e)
            await update.message.reply_text(f"Failed to save file: {e}", do_quote=True)
            return
    if not success: return
    prompt = f"User has sent a file at `{file_path}`\n" + (f"Caption: {caption}\n" if caption else "") + f"Size: {len(file_bytes):,} bytes, type: {doc.mime_type or 'unknown'}\n" + "Please review it."
    await run_with_tools(update, prompt)

async def handle_message(update: Update, context: ContextTypes.DEFAULT_TYPE) -> None:
    if not update.message or not update.message.text: return
    user = update.effective_user
    track_message(user.id, user.username)
    chat_id = update.effective_chat.id
    text = update.message.text.strip()
    if update.effective_chat.type in ("group", "supergroup"):
        bot_username = context.bot.username
        if f"@{bot_username}" not in text and not text.startswith("/"): return
        text = text.replace(f"@{bot_username}", "").strip()
    if not text: return
    await run_with_tools(update, text)
