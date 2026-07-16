import logging

from telegram import BotCommand
from telegram.ext import CommandHandler, MessageHandler, filters
from .commands_core import start, menu, tools_cmd
from .commands_ctx import context_cmd, compact_cmd, clear, clear_all
from .commands_ai import ai_ask, handle_document, handle_message
from .metrics import get_bio_texts

logger = logging.getLogger(__name__)

BOT_COMMANDS = [
    BotCommand("start", "Show welcome message"),
    BotCommand("menu", "Show all commands"),
    BotCommand("ai", "Ask Puru AI (use in groups)"),
    BotCommand("tools", "Show available tools"),
    BotCommand("context", "Show token usage info"),
    BotCommand("compact", "Summarize & compress context"),
    BotCommand("clear", "Clear conversation history"),
    BotCommand("clear_all", "Wipe ALL data: history, files, versions"),
]

async def update_bot_bio(application):
    try:
        short, full = get_bio_texts()
        await application.bot.set_my_short_description(short)
        await application.bot.set_my_description(full)
        logger.debug("Bio updated — %s", short)
    except Exception as e:
        logger.warning("Failed to update bio: %s", e)

async def post_init(application) -> None:
    await application.bot.set_my_commands(BOT_COMMANDS)
    me = await application.bot.get_me()
    logger.info("Bot started as @%s", me.username)
    if application.job_queue:
        application.job_queue.run_repeating(
            lambda ctx: update_bot_bio(ctx.application),
            interval=300,
            first=15,
        )
    else:
        logger.warning("JobQueue not available — bio auto-update disabled")

def register_handlers(app):
    app.add_handler(CommandHandler("start", start))
    app.add_handler(CommandHandler("menu", menu))
    app.add_handler(CommandHandler("ai", ai_ask))
    app.add_handler(CommandHandler("tools", tools_cmd))
    app.add_handler(CommandHandler("context", context_cmd))
    app.add_handler(CommandHandler("compact", compact_cmd))
    app.add_handler(CommandHandler("clear", clear))
    app.add_handler(CommandHandler("clear_all", clear_all))
    app.add_handler(MessageHandler(filters.Document.ALL, handle_document))
    app.add_handler(MessageHandler(filters.TEXT & ~filters.COMMAND, handle_message))
