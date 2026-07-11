import logging
import asyncio
from io import BytesIO
from telegram import Update, BotCommand, InputFile
from telegram.ext import (
    ApplicationBuilder,
    CommandHandler,
    MessageHandler,
    filters,
    ContextTypes,
)
from config import TELEGRAM_BOT_TOKEN, TOKEN_WARN_LIMIT, MAX_LOOPS
from agent import chat_with_tools, chat_stream, clear_history, get_context_info, compact_history

BOT_COMMANDS = [
    BotCommand("start", "Show welcome message"),
    BotCommand("menu", "Show all commands"),
    BotCommand("ai", "Ask Puru AI (use in groups)"),
    BotCommand("tools", "Show available tools"),
    BotCommand("context", "Show token usage info"),
    BotCommand("compact", "Summarize & compress context"),
    BotCommand("clear", "Clear conversation history"),
]

logging.basicConfig(
    format="%(asctime)s - %(name)s - %(levelname)s - %(message)s",
    level=logging.INFO,
)
logger = logging.getLogger(__name__)


async def post_init(application) -> None:
    await application.bot.set_my_commands(BOT_COMMANDS)
    print("Commands registered to Telegram.")


async def start(update: Update, context: ContextTypes.DEFAULT_TYPE) -> None:
    await update.message.reply_text(
        "Puru Code - AI Coding Agent\n\n"
        "Send me any coding question or paste code to debug.",
        quote=True,
    )


async def menu(update: Update, context: ContextTypes.DEFAULT_TYPE) -> None:
    await update.message.reply_text(
        "*Puru Code - Menu*\n\n"
        "/start - Welcome message\n"
        "/menu - Show this menu\n"
        "/context - Token usage info\n"
        "/compact - Summarize context\n"
        "/clear - Clear history\n\n"
        "_Send any message to chat with AI._",
        parse_mode="Markdown",
        quote=True,
    )


async def context_cmd(update: Update, context: ContextTypes.DEFAULT_TYPE) -> None:
    info = get_context_info(update.effective_user.id)
    await update.message.reply_text(
        f"*Context Info*\n\n"
        f"Messages: {info['messages']}\n"
        f"Estimated tokens: ~{info['tokens']:,}\n"
        f"Warn limit: {TOKEN_WARN_LIMIT:,}",
        parse_mode="Markdown",
        quote=True,
    )
    if info["warn"]:
        await update.message.reply_text(
            "Use /compact to summarize and compress your context.",
            quote=True,
        )


async def compact_cmd(update: Update, context: ContextTypes.DEFAULT_TYPE) -> None:
    await update.message.reply_text("Compacting context...", quote=True)
    result = compact_history(update.effective_user.id)
    await update.message.reply_text(result, quote=True)


async def tools_cmd(update: Update, context: ContextTypes.DEFAULT_TYPE) -> None:
    from config import MODEL_NAME
    await update.message.reply_text(
        "*Puru AI — Tools & Kemampuan*\n\n"
        "╭─ *🧠 AI Model* ─────────────╮\n"
        f"│  Model: `{MODEL_NAME}`\n"
        "│  Role: AI Coding Agent\n"
        "│  Bisa baca/koding/react\n"
        "╰────────────────────────────╯\n\n"
        "╭─ *📦 Sandbox Tools* ────────╮\n"
        "│  AI bisa pakai tools ini:\n"
        "│\n"
        "│  🔹 `/bash` — Jalanin command\n"
        "│     SUCCESS/FAIL, timeout 60s\n"
        "│\n"
        "│  🔹 `/write_file` — Buat file\n"
        "│     SUCCESS/FAIL, auto dir\n"
        "│\n"
        "│  🔹 `/read_file` — Baca file\n"
        "│     Start/end line optional\n"
        "│\n"
        "│  🔹 `/edit_file` — Edit file\n"
        "│     SUCCESS/FAIL, replace text\n"
        "│\n"
        "│  🔹 `/send_file` — Kirim file\n"
        "│     Timeout 120s, tanpa error palsu\n"
        "╰────────────────────────────╯\n\n"
        "╭─ *📋 Command Lain* ─────────╮\n"
        "│  /ai — Panggil AI (di grup)\n"
        "│  /context — Cek pemakaian token\n"
        "│  /compact — Ringkas history\n"
        "│  /clear — Hapus history chat\n"
        "│  /menu — Semua command\n"
        "╰────────────────────────────╯\n\n"
        "_Kirim pesan langsung atau /ai + pertanyaan untuk mulai coding!_",
        parse_mode="Markdown",
        quote=True,
    )


async def clear_cmd(update: Update, context: ContextTypes.DEFAULT_TYPE) -> None:
    clear_history(update.effective_user.id)
    await update.message.reply_text("Conversation history cleared.", quote=True)


async def ai_cmd(update: Update, context: ContextTypes.DEFAULT_TYPE) -> None:
    """Handle /ai command — works in both groups and private chats."""
    message = " ".join(context.args) if context.args else ""
    if not message:
        await update.message.reply_text("Usage: /ai <your question>", quote=True)
        return
    await _process_ai(update, context, message)


async def handle_message(update: Update, context: ContextTypes.DEFAULT_TYPE) -> None:
    """Handle plain text — only in private chats."""
    if update.message.chat.type != "private":
        return
    await _process_ai(update, context, update.message.text)


async def _process_ai(update: Update, context: ContextTypes.DEFAULT_TYPE, message: str) -> None:
    user_id = update.effective_user.id
    user_name = update.effective_user.first_name
    message = update.message.text

    logger.info(f"Message from {user_name} ({user_id}): {message[:50]}...")
    await update.message.chat.send_action("typing")

    # Send initial placeholder
    sent_msg = await update.message.reply_text("Thinking...", quote=True)
    msg_id = sent_msg.message_id
    chat_id = update.message.chat.id

    last_text = "Thinking..."
    loop_count = 0

    try:
        for text, is_done, lc, file_list in chat_with_tools(user_id, dated_message):
            loop_count = lc

            # Send all files returned from tool calls
            for file_data in file_list:
                filename, file_bytes, caption = file_data
                try:
                    await context.bot.send_document(
                        chat_id=chat_id,
                        document=InputFile(BytesIO(file_bytes), filename=filename),
                        caption=caption or "",
                        read_timeout=120,
                        write_timeout=120,
                    )
                except Exception as e:
                    logger.error(f"Send file error: {e}")
                    # Don't send error message if the file might have been sent
                    # Check if it's a timeout — Telegram often still delivers the file
                    err_str = str(e).lower()
                    if "timed out" in err_str or "timeout" in err_str:
                        await context.bot.send_message(
                            chat_id=chat_id,
                            text=f"File `{filename}` dikirim (mungkin butuh beberapa detik untuk muncul).",
                            parse_mode="Markdown",
                        )
                    else:
                        try:
                            await context.bot.send_message(
                                chat_id=chat_id,
                                text=f"Failed to send file {filename}: {str(e)[:100]}",
                            )
                        except Exception:
                            pass

            if is_done:
                # Final answer — render markdown
                if len(text) > 4096:
                    for i in range(0, len(text), 4096):
                        chunk = text[i : i + 4096]
                        if i == 0:
                            try:
                                await context.bot.edit_message_text(
                                    chat_id=chat_id,
                                    message_id=msg_id,
                                    text=chunk,
                                    parse_mode="Markdown",
                                )
                            except Exception:
                                await context.bot.edit_message_text(
                                    chat_id=chat_id,
                                    message_id=msg_id,
                                    text=chunk,
                                )
                        else:
                            try:
                                await context.bot.send_message(
                                    chat_id=chat_id,
                                    text=chunk,
                                    parse_mode="Markdown",
                                )
                            except Exception:
                                await context.bot.send_message(
                                    chat_id=chat_id,
                                    text=chunk,
                                )
                else:
                    try:
                        await context.bot.edit_message_text(
                            chat_id=chat_id,
                            message_id=msg_id,
                            text=text,
                            parse_mode="Markdown",
                        )
                    except Exception:
                        await context.bot.edit_message_text(
                            chat_id=chat_id,
                            message_id=msg_id,
                            text=text,
                        )
                break

            # Progress update
            new_text = text
            if new_text != last_text:
                try:
                    await context.bot.edit_message_text(
                        chat_id=chat_id,
                        message_id=msg_id,
                        text=new_text,
                    )
                    last_text = new_text
                except Exception:
                    pass

    except Exception as e:
        logger.error(f"Error: {e}")
        try:
            await context.bot.edit_message_text(
                chat_id=chat_id,
                message_id=msg_id,
                text=f"Error: {str(e)}",
            )
        except Exception:
            pass

    # Token warning
    info = get_context_info(user_id)
    if info["warn"]:
        await update.message.reply_text(
            "*WARNING:* Context is large! Use /compact to summarize.",
            parse_mode="Markdown",
            quote=True,
        )


def main() -> None:
    if not TELEGRAM_BOT_TOKEN:
        print("Error: TELEGRAM_BOT_TOKEN not set")
        return

    app = ApplicationBuilder().token(TELEGRAM_BOT_TOKEN).post_init(post_init).build()

    app.add_handler(CommandHandler("start", start))
    app.add_handler(CommandHandler("menu", menu))
    app.add_handler(CommandHandler("ai", ai_cmd))
    app.add_handler(CommandHandler("tools", tools_cmd))
    app.add_handler(CommandHandler("context", context_cmd))
    app.add_handler(CommandHandler("compact", compact_cmd))
    app.add_handler(CommandHandler("clear", clear_cmd))
    app.add_handler(MessageHandler(filters.TEXT & ~filters.COMMAND, handle_message))

    print("Puru Code bot is running...")
    app.run_polling(allowed_updates=Update.ALL_TYPES)


if __name__ == "__main__":
    main()
