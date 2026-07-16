import os
import subprocess
import sys

# To automatically restart run.py on code changes, run this script with watchmedo:
# First, install watchdog: pip install watchdog
# Then, run: watchmedo auto-restart -d . -p "*.py" -- python dev.py

os.environ["TELEGRAM_BOT_TOKEN"] = "8535593654:AAEaEddzmyzVxkwWPy1l8JiHT7UfPZ-pOtA"
os.environ["DEV_MODE"] = "true"

print("Starting run.py with development token...")
# Use .venv Python to ensure all dependencies are available
venv_python = os.path.join(os.path.dirname(__file__), ".venv", "Scripts", "python.exe")
subprocess.run([venv_python, "run.py"])