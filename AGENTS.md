# Agent Guidelines - Puru Code AI

## Developer Commands
- Run locally: `py bot.py` (Use `py` launcher on Windows)
- Install dependencies: `pip install -r requirements.txt`

## Architecture & Flow
- **Flat Structure**: Core logic resides in the root directory.
- **Key Files**:
    - `bot.py`: Entry point, Telegram handlers, and Flask dashboard (port 3000).
    - `agent.py`: AI logic, history management, and tool-calling loop.
    - `sandbox.py`: E2B sandbox integration, tool execution, and Firebase syncing.
    - `config.py`: Central configuration for API keys, model settings, and limits.
- **Data Flow**: `bot.py` $\rightarrow$ `agent.py` $\rightarrow$ External AI API $\rightarrow$ `sandbox.py` $\rightarrow$ E2B.

## Implementation Details
- **Tool Format**: AI communicates tool calls using XML-like tags: `<tool><name>...</name><parameter><name>...</name><value>...</value></parameter></tool>`.
- **Self-Correction**: The agent implements a retry loop in `agent.py`. Failures are fed back to the AI as `[SYSTEM ERROR]` to trigger self-correction.
- **State Management**: History and file versions are persisted in Firebase, keyed by `chat_id`.

## Important Constraints
- **Config**: No `.env` used; edit `config.py` directly.
- **Tooling**: No built-in linter or test suite; manual verification via temporary scripts is required.
- **Scoping**: `chat_id` is the primary key; group chats share the same AI context and sandbox.
