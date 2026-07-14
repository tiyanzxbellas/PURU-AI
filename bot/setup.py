from telegram import BotCommand
from telegram.ext import CommandHandler, MessageHandler, filters
from .commands_core import start, menu, tools_cmd
from .commands_ctx import context_cmd, compact_cmd, clear, clear_all
from .commands_ai import ai_ask, handle_document, handle_message

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

async def post_init(application) -> None:
    await application.bot.set_my_commands(BOT_COMMANDS)
    me = await application.bot.get_me()
    # We use a logger or something else here since BOT_USERNAME is now in dashboard
    import logging
    logging.getLogger(__name__).info("Bot started as @%s", me.username)

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
