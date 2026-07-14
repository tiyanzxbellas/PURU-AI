# Agent Guide: Telegram AI Bot

## Developer Commands
- **Run Bot & Dashboard**: `python run.py`
- **Run in Dev Mode**: `python dev.py` (uses dev token)
- **Hot-reloading**: `pip install watchdog` then `watchmedo auto-restart -d . -p "*.py" -- python dev.py`

## Architecture
- `bot/`: Telegram interface, command handlers, and Flask dashboard.
- `agent/`: AI orchestration engine, conversation history, and LLM interactions.
- `sandbox.py`: Manages e2b sandboxes and provides tools for the AI.
- `firebase.py`: Handles persistent file storage for user sandboxes.
- `config.py`: Central configuration, including tokens and the system prompt.

## AI Tooling & Sandbox
The AI has access to several tools defined in `sandbox.py`:
- `bash(command)`: Executes commands in the e2b sandbox.
- `write_file(path, content)`: Creates/overwrites files; **auto-saves to Firebase**.
- `edit_file(path, old_text, new_text)`: Exact text replacement; **auto-saves to Firebase**.
- `read_file(path, start_line, end_line)`: Reads file contents from the sandbox.
- `delete_file(path)`: Deletes a file from Firebase storage.
- `save_file(path)`: Permanently saves a sandbox file to Firebase (max 2MB).
- `send_file(path, caption)`: Sends a file from the sandbox to the Telegram chat.

## Memory System
The bot has a persistent memory system for remembering user info:
- **File**: `/memory/MEMORY.md` stored in Firebase.
- **Usage**: The AI agent uses `write_file` or `edit_file` to save/update user personal info (name, hobbies, preferences) to this file.
- **Auto-injection**: On every message, `_ensure_history()` in `agent/history.py` fetches MEMORY.md from Firebase and injects its content into the system prompt so the AI always remembers.
- **Auto-deletion**: `/clear_all` wipes all Firebase data including `/memory/MEMORY.md` via `clear_all_fb_data()` in `firebase.py`.
- **Config**: The memory path is defined as `MEMORY_PATH` in `config.py`.

**Note**: Sandbox files are transient but are synchronized with Firebase storage based on a versioning system in `sandbox.py:_sync_sandbox`.
