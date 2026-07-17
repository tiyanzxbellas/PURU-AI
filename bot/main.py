import logging
import asyncio
import threading
import time
import os
from telegram import Update
from telegram.error import Conflict, TelegramError
from telegram.ext import ApplicationBuilder
from .setup import post_init, register_handlers
from .dashboard import run_web_server

TELEGRAM_BOT_TOKEN = os.getenv("TELEGRAM_BOT_TOKEN", "8780968685:AAGGWgQKGcmegNFpq28JHyLRLL0vwbbk3-M")

logging.basicConfig(format="%(asctime)s - %(name)s - %(levelname)s - %(message)s", level=logging.INFO)
logger = logging.getLogger(__name__)

def run_bot() -> None:
    app_tg = ApplicationBuilder().token(TELEGRAM_BOT_TOKEN).post_init(post_init).build()
    register_handlers(app_tg)
    logger.info("Starting Telegram bot polling...")
    app_tg.run_polling(drop_pending_updates=True)

if __name__ == "__main__":
    web_thread = threading.Thread(target=run_web_server, daemon=True)
    web_thread.start()
    logger.info("Dashboard running on http://0.0.0.0:3000")
    while True:
        try:
            run_bot()
        except (Conflict, TelegramError) as e:
            logger.error("Bot disconnected: %s — reconnecting in 10s...", e)
            time.sleep(10)
