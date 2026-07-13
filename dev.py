import os
import subprocess
import sys

# To automatically restart bot.py on code changes, run this script with watchmedo:
# First, install watchdog: pip install watchdog
# Then, run: watchmedo auto-restart -d . -p "*.py" -- python dev.py

os.environ["TELEGRAM_BOT_TOKEN"] = "8535593654:AAEaEddzmyzVxkwWPy1l8JiHT7UfPZ-pOtA"

print("Starting bot.py with development token...")
# Use sys.executable to ensure the same python interpreter is used
subprocess.run([sys.executable, "bot.py"])