# CLAUDE.md

This file provides guidance to Claude Code (claude.ai/code) when working with code in this repository.

## Project Overview

Python Telegram bot ("Puru Code AI" / @PuruAI_bot) that integrates with an OpenAI-compatible API for chat completions. Includes E2B cloud sandboxes for secure code execution and a Flask web dashboard with live metrics.

## Running the Project

```bash
# Install dependencies
pip install -r requirements.txt

# Run locally
python bot.py

# Run with Docker
docker build -t puru-ai-bot . && docker run -p 3000:3000 puru-ai-bot
```

The app starts a Flask dashboard on port 3000 and Telegram bot polling in the main thread. No test suite or linter is configured.

## Architecture

All source lives in the root directory (flat structure, 4 files):

- **bot.py** — Entry point. Telegram command handlers (`/start`, `/menu`, `/tools`, `/context`, `/compact`, `/clear`), Flask dashboard (routes: `GET /` HTML page, `GET /health` JSON), and reconnection loop for Telegram polling. Tracks in-memory metrics (messages, users, command usage).
- **agent.py** — AI logic layer. Manages per-user conversation history (in-memory `conversations` dict, max 30 messages). Three chat modes: `chat_with_tools()` (agentic tool-calling loop up to 50 iterations), `chat_stream()` (streaming text, no tools — currently used by the main handler), `chat()` (sync, legacy). Handles context compaction via summarization and token estimation (`len(text) // 4`).
- **sandbox.py** — E2B sandbox integration. Defines 5 tools: `bash` (60s timeout), `write_file`, `read_file`, `edit_file`, `send_file`. Each tool returns `(result_text, file_data)` where `file_data` is non-null only when sending files to the user. Per-user sandbox instances.
- **config.py** — All configuration as module-level constants (API keys, model name, token limits, system prompt). No `.env` loading — values are currently hardcoded.

## Data Flow

User message → `bot.py` handle_message → `agent.py` chat_stream → OpenAI-compatible API → streaming response → Telegram reply.

The `chat_with_tools()` path with E2B sandbox exists but is not wired into the main message handler.

## Key Configuration (config.py)

| Constant | Purpose |
|---|---|
| `TELEGRAM_BOT_TOKEN` | Telegram Bot API token |
| `OPENAI_API_KEY` / `OPENAI_BASE_URL` | OpenAI-compatible endpoint credentials |
| `MODEL_NAME` | Model identifier sent to API |
| `E2B_API_KEY` | E2B sandbox service key |
| `MAX_LOOPS` | Tool-calling iteration limit (50) |
| `TOKEN_WARN_LIMIT` / `TOKEN_COMPACT_LIMIT` | Token thresholds for warnings and auto-compaction (20k / 35k) |

## Dependencies

`python-telegram-bot` (Telegram API), `openai` (API client), `e2b` (cloud sandboxes), `flask` (dashboard), `psutil` (system metrics).
