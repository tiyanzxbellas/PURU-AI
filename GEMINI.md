# Puru Code AI - Project Documentation

## Project Overview
Puru Code AI is a Python-based Telegram bot (@PuruAI_bot) designed as an AI coding agent. It integrates an OpenAI-compatible API for chat completions and uses E2B cloud sandboxes for secure, remote code execution. The project includes a live Flask-based web dashboard to monitor bot metrics.

### Main Technologies
- **Language:** Python 3.10+
- **Bot Framework:** `python-telegram-bot` (v21.3)
- **AI Integration:** `openai` (v1.35.0) for agentic tool-calling.
- **Sandboxing:** `e2b` (v1.0.4) for secure bash and file system operations.
- **Web Dashboard:** `flask` (v3.1.1) for live monitoring.
- **Metrics:** `psutil` for system resource tracking.

## Architecture
The project follows a flat directory structure in the root:

- `bot.py`: The entry point. Handles Telegram command dispatching, Flask web server (running in a daemon thread), and the main polling loop with automatic reconnection.
- `agent.py`: Contains the AI logic. Manages conversation history, token estimation, context compaction (summarization), and the tool-calling loop (`chat_with_tools`).
- `sandbox.py`: Integrates with E2B to provide a secure environment for the AI. Defines tools like `bash`, `write_file`, `read_file`, `edit_file`, and `send_file`.
- `config.py`: Centralized configuration using module-level constants for API keys, model names, and system prompts.
- `firebase.py`: (Internal/Placeholder) Potential future integration for persistence (currently mostly in-memory).

## Building and Running

### Prerequisites
- Python 3.10+
- API Keys for OpenAI-compatible provider, Telegram Bot API, and E2B.

### Local Setup
1. **Install dependencies:**
   ```bash
   pip install -r requirements.txt
   ```
2. **Configure API Keys:**
   Edit `config.py` to include your `TELEGRAM_BOT_TOKEN`, `OPENAI_API_KEY`, and `E2B_API_KEY`.
3. **Run the bot:**
   ```bash
   python bot.py
   ```

### Docker
A `Dockerfile` is provided for containerized deployment:
```bash
docker build -t puru-ai-bot .
docker run -p 3000:3000 puru-ai-bot
```
The dashboard will be available at `http://localhost:3000`.

## Development Conventions

### Bot Persona and Persistence
- **Persona:** The bot ("Puru") is a soft-spoken, Gen Z-style coding buddy. It dislikes being called an "AI" and will react accordingly if referred to as one.
- **Persistence:** 
  - The E2B sandbox is temporary and will be wiped.
  - `write_file` and `edit_file` tools automatically persist files to Firebase. No manual save is required.
  - Use `send_file` to share files from the sandbox to the Telegram chat.

### Tool Execution
The agent uses a "thought-action-observation" loop (up to 50 iterations). Tools are executed in an E2B sandbox, and results are fed back into the conversation history.

### Context Management
- **Token Estimation:** Calculated as `len(text) // 4`.
- **Compaction:** When tokens exceed limits, the `/compact` command (or auto-trigger) summarizes history using an external Gemini-based API to free up context.
- **Stripping Thoughts:** `<thought>...</thought>` blocks from the model's response are stripped before being displayed to the user.

### Testing
There is currently no automated test suite or linter configured for this project.
