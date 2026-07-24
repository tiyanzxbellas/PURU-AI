# Agents.md

## Quick start
- `npm run dev` — nodemon + tsx (hot reload)
- `npm start` — tsx
- `npm run build` — tsc
- `npm run build:bundle` — esbuild single-file bundle
- `npm run typecheck` — tsc --noEmit
- No tests, no lint, no formatter configured

## Config & secrets
- Configuration is handled via environment variables, validated at startup in `src/config.ts`.
- `.env` file at repo root stores secrets.
- Firebase RTDB base URL is loaded from `PUBLIC_RTDB` environment variable.
- Optional configuration (with defaults):
  - `HOSTNAME` — Server bind address (default: `localhost`)
  - `PORT` — Server port (default: `3000`)
  - `TEMPERATURE` — AI model temperature (default: `0`)
  - `COMPACT_TOKEN` — Max tokens for history compaction (default: `20480`)
  - `MAX_LOOP` — Max agent iterations per request (default: `20`)
  - `HISTORY_CACHE_MAX` — Max users in LRU cache (default: `500`)
  - `HISTORY_CACHE_TTL` — Cache TTL in ms (default: `600000` / 10 min)

## Architecture
- `src/index.ts` — entrypoint, starts health server then bot in a conflict-retry loop
- `src/bot.ts` — grammY Bot setup, commands (`/start`, `/menu`, `/clear`, `/token`, `/reset`, `/ai`), message handlers
- `src/agent.ts` — `ToolLoopAgent` (Vercel AI SDK) with 19 tools; configurable `temperature` and `maxLoop`
- `src/vfs.ts` — per-user virtual file system stored in Firebase Realtime Database
- `src/history.ts` — chat history persistence (LRU cache + Firebase RTDB)
- `src/e2b.ts` — E2B sandbox (one per chat, 5 min timeout, auto-killed on expiry)
- `src/server.ts` — HTTP health check on port 3000

## Key behaviors
- **Retries**: API calls retry up to 8 times (3s→...→45s exp backoff). Web search retries 5 times (1s→16s).
- **History persistence** (`history.ts`): Chat history stored in Firebase RTDB with LRU cache (max 500 users, 10 min TTL). Cache miss loads from Firebase; writes are synchronous (await). Empty arrays are not cached.
- **History compaction** (`bot.ts:90`): After each response, prunes reasoning/tool-call parts and trims oldest non-system messages to stay under configurable `COMPACT_TOKEN` (default: 20480) estimated tokens, using `@langchain/core/messages.trimMessages`.
- **Sequential processing**: Bot uses `bot.start()` (not `bot.run()`) — updates processed one at a time, keeping peak RAM low on 512MB machines.
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
