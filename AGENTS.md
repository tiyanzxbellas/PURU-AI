# Agent Guide: Telegram AI Bot

## Run
```powershell
python run.py               # prod token from config.py
python dev.py                # dev token from .env / env var (uses .venv/Scripts/python.exe)
```
Dashboard at `http://0.0.0.0:3000`. No tests, no linter, no typecheck.

## Two-Agent Architecture (`agent/autogen_engine.py`)

Two `AssistantAgent`s share one `OpenAIChatCompletionClient` (`model="puru"` at `AUTOGEN_BASE_URL`).

**Orchestrator (Puru)** — tools: `ls`, `read_file`, `write_file`, `edit_file`, `delegate_task`. Context loaded from Firebase history (clean — only user + final answer). For file ops use tools directly; for bash/search/download/code, calls `delegate_task`.

**Worker (ephemeral)** — spawned fresh per `delegate_task` call with **empty context** (only system prompt + task). Tools: `bash`, `write_file`, `read_file`, `edit_file`, `delete_file`, `send_file`, `save_file`. Discarded after returning result. Never saved to history.

Main loop yields `(text, is_done, loop_count, pending_files)`. Retry wrapper: 5 attempts, backoff [1,2,4,8,16]s. Resets `model_context` per attempt.

## Conversation History
- Single source: encrypted JSON at Firebase `history/{chat_id}`.
- Loaded into `conversations[user_id]` dict on startup.
- **Only clean user + assistant messages saved** — no tool execution artifacts in Firebase.
- `/clear_all` wipes `history/`, `files/`, `versions/`.

## Auto-Compact
- Trigger: `get_token_count() >= TOKEN_COMPACT_LIMIT` (5k).
- Uses **legacy Gemini API** (`agent/ai.py:_call_ai_api`) — will fail if Gemini is down.
- Formats non-system messages as dialog → Gemini summary → replaces history with 2 messages.

## Key Gotchas
- `do_quote=True` (PTB v22.8), not `quote=True`.
- `agent/engine.py`, `agent/ai.py`, `agent/formatter.py` are **legacy** — do not touch unless specifically asked.
- No lockfile (`requirements.txt` only). Actual installed versions may differ.
- `get_context_info` `messages` field counts all non-system messages.
- Token estimation: `len(content) // 4`.
- `sandbox.py`'s `TOOLS` list (OpenAI JSON schemas) is **legacy** — not used by AutoGen engine.
