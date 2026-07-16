# Agent Guide: Telegram AI Bot

## Run
```powershell
python run.py               # prod token from config.py
python dev.py                # dev token from .env / env var (uses .venv/Scripts/python.exe)
```
Dashboard at `http://0.0.0.0:3000`. No tests, no linter, no typecheck.

## Architecture
- `bot/`: PTB handlers + Flask dashboard. Entry: `bot/main.py:run_bot()` → polling.
- `agent/autogen_engine.py`: **Primary engine** — AutoGen v0.7.5, model `"puru"` at `AUTOGEN_BASE_URL`. Async generator yielding `(text, is_done, loop_count, pending_files)`.
- `agent/engine.py`: **Legacy** — old Gemini XML engine. Not used unless explicitly called.
- `agent/ai.py`: Legacy Gemini API client. Used **only** by `compact.py` and old engine.
- `sandbox.py`: e2b sandbox management (bash, file I/O). Auto-recreates on death.
- `firebase.py`: Firebase RTDB — encrypted history, file storage, version system.

## AutoGen Engine (`autogen_engine.py`)
- `_populate_context_from_fb()`: Rebuilds `model_context` from `fb_history` each turn (no persistent state file).
- Retry wrapper: 5 attempts, backoff [1,2,4,8,16]s. Resets `model_context` per attempt.
- `OpenAIChatCompletionClient(max_retries=5)`. Endpoint handles `stream=false` internally.
- `ToolCallRequestEvent.content`: `List[FunctionCall]` (`.name`, `.arguments`)
- `ToolCallExecutionEvent.content`: `List[FunctionExecutionResult]` (`.name`, `.content`, `.is_error`)
- `tool_results` collected during run → appended as `⚙️ Tool executions:` to assistant message in `fb_history`. User sees clean `display_response`.
- Each assistant message in `fb_history` includes tool execution summary.

## Conversation History
- Single source of truth: `fb_history` in Firebase (encrypted JSON at `history/{chat_id}`).
- Loaded into `conversations[user_id]` dict on startup.
- Every turn: user msg appended → AutoGen processes → assistant msg (with tool summary) appended → `save_history()`.
- `/clear_all` wipes `history/`, `files/`, `versions/` from Firebase.

## Auto-Compact
- Trigger: `get_token_count() >= TOKEN_COMPACT_LIMIT` (10k).
- Uses **legacy Gemini API** (`agent/ai.py:_call_ai_api`) — will fail if Gemini is down.
- `compact_history()`: Formats all non-system messages (user + assistant with tool info) as dialog → sends to Gemini → replaces history with summary + dummy assistant msg.

## Memory
- Path: `/memory/MEMORY.md` in Firebase (defined in `config.py`).
- Injected into system prompt on every message.
- AI saves via `write_file`/`edit_file` (tool context: "Memory system").
- Wiped by `/clear_all`.

## Key Gotchas
- `do_quote=True` (PTB v22.8), not `quote=True`.
- `agent/engine.py`, `agent/ai.py`, `agent/formatter.py` are **legacy** — do not touch unless specifically asked.
- No lockfile (`requirements.txt` only). Actual installed versions may differ (e.g., `autogen-agentchat==0.7.5` vs `>=0.5.0` in reqs).
- `get_context_info` `messages` field counts **all** non-system messages (user + assistant), not just user.
- Token estimation is `len(content) // 4` — rough approximation.
