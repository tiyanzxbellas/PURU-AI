import threading
import logging
from bot.main import run_bot
from bot.dashboard import run_web_server

if __name__ == "__main__":
    web_thread = threading.Thread(target=run_web_server, daemon=True)
    web_thread.start()
    logging.basicConfig(level=logging.INFO)
    logging.getLogger(__name__).info("Dashboard running on http://0.0.0.0:3000")
    run_bot()
