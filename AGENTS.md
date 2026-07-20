# Agents.md

## Quick start
- `npm run dev` — nodemon + tsx (hot reload)
- `npm start` — tsx
- `npm run build` — tsc
- `npm run typecheck` — tsc --noEmit
- No tests, no lint, no formatter configured

## Config & secrets
- `config.js` at repo root holds secrets (bot token, AI endpoint, E2B API key). **Already committed.**
- `config.d.ts` provides TS types; `src/config.ts` loads it, with `BOT_TOKEN` env var overriding `telegramBotToken`.
- Firestore base URL is hardcoded in `src/vfs.ts:1`.

## Architecture
- `src/index.ts` — entrypoint, starts health server then bot in a conflict-retry loop
- `src/bot.ts` — grammY Bot setup, commands (`/start`, `/menu`, `/clear`, `/token`, `/reset`, `/ai`), message handlers
- `src/agent.ts` — `ToolLoopAgent` (Vercel AI SDK) with 18 tools; uses `wrapLanguageModel` middleware for 18k token limit
- `src/vfs.ts` — per-user virtual file system stored in Firebase Realtime Database
- `src/e2b.ts` — E2B sandbox (one per chat, 5 min timeout, auto-killed on expiry)
- `src/server.ts` — HTTP health check on port 3000

## Key behaviors
- **Retries**: API calls retry up to 8 times (3s→...→45s exp backoff). Web search retries 5 times (1s→16s).
- **Token limit middleware** (`agent.ts:52`): drops tool-call/tool-result parts from old messages when prompt exceeds 18k tokens.
- **History compaction** (`bot.ts:45`): after each response, prunes reasoning/tool-call parts, then trims oldest non-system messages to stay under 2048 estimated tokens.
- **User memory**: agent reads `/memory/MEMORY.md` from VFS and injects it as a system message on every request.
- **Safe reply/edit** (`bot.ts:21-43`): Markdown parsing errors are caught and retried without `parse_mode`.
- **E2B sandbox**: create → write code to VFS → `e2b_run_code` reads from VFS. One sandbox per chat, 5 min idle kill.
- **SoundCloud / web search** via `puruboy-api.vercel.app`.
- **Math**: `Function()` constructor eval.

## Conventions
- All source imports use `.js` extension (ESM).
- `"type": "module"` in package.json.
- Agent instructions are in Indonesian; responses should be concise.
- `src/tools.ts` exports a `ToolNames` union type — update when adding tools.
