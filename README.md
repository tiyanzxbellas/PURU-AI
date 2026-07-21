# PURU-AI Telegram Bot

A Telegram AI bot powered by the **Vercel AI SDK** with a Firebase-backed virtual file system, user memory, AI tools, and per-user chat history.

## Features

- **AI Chat** — Conversational AI using the Vercel AI SDK's `ToolLoopAgent` with streaming responses
- **Virtual File System (VFS)** — Each user gets a personal file system stored in Firebase (Realtime Database), accessible via AI tools
- **User Memory** — AI remembers user information by reading/writing `/memory/MEMORY.md` in the user's VFS
- **E2B Sandbox** — Execute code in isolated cloud environments with automatic package installation
- **Web Search** — Yahoo search integration with automatic retry (5x exponential backoff)
- **Web Crawl** — Fetch and summarize website content
- **Math & Time** — Built-in math evaluation and timezone-aware clock tools
- **Group Chat** — Use `/ai <message>` to interact with the bot in groups
- **Token Limit** — Automatic prompt trimming when exceeding 18k tokens
- **Exponential Backoff** — Retry up to 5 times with 1s→2s→4s→8s→16s delays on API failures
- **Markdown Fallback** — Handles Telegram parse errors gracefully by retrying without parse mode

## Commands

| Command | Description |
|---------|-------------|
| `/start` | Start the bot |
| `/menu` | Show available commands |
| `/clear` | Clear conversation history |
| `/token` | Show estimated token usage |
| `/reset` | Delete all data (history + VFS files) |
| `/skills` | List, read, or delete skills |
| `/ai <message>` | Chat with AI (required in groups) |

In **private chat**, just send any message to talk to the AI. In **group chat**, use `/ai` followed by your message. Use `/skills` to manage AI skills (list, read, delete).

## Architecture

```
src/
├── index.ts      — Entry point, starts bot + health server
├── bot.ts        — Telegram bot setup (commands, message handler, safeReply/safeEdit)
├── agent.ts      — ToolLoopAgent with 19 tools + processMessage with retry + memory injection
├── vfs.ts        — Firebase VFS (read, write, edit, delete, list, deleteAll)
├── tools.ts      — ToolNames type union
├── config.ts     — Config loader (env var BOT_TOKEN override)
└── server.ts     — HTTP health check server (port 3000)
```

### Tools Available to AI

1. **list_directory** — List files in VFS directory
2. **read_file** — Read file contents from VFS
3. **write_file** — Write/create file in VFS
4. **edit_file** — Find-and-replace in a VFS file
5. **delete_file** — Delete file from VFS
6. **move_file** — Move or rename file in VFS
7. **send_file** — Send a VFS file to Telegram chat
8. **search_web** — Yahoo web search with retry
9. **crawl** — Fetch and extract text from a webpage using cheerio
10. **get_current_time** — Current time in any IANA timezone
11. **calculate_math** — Evaluate mathematical expressions
12. **e2b_sandbox_create** — Create isolated E2B sandbox
13. **e2b_run_code** — Execute code from VFS in E2B sandbox
14. **e2b_install_package** — Install packages in E2B sandbox
15. **e2b_send_file** — Send file from E2B sandbox to Telegram
16. **e2b_sandbox_kill** — Terminate E2B sandbox
17. **create_skill** — Create new skill in /skills/ directory
18. **use_skills** — Read and use skill from /skills/ directory
19. **delete_skill** — Delete skill from /skills/ directory

## Tech Stack

- [grammY](https://grammy.dev/) — Telegram Bot Framework
- [Vercel AI SDK](https://sdk.vercel.ai/) — AI streaming, tool calling, `ToolLoopAgent`
- [Firebase Realtime Database](https://firebase.google.com/) — User file storage (VFS)
- [Zod](https://zod.dev/) — Schema validation for AI tool inputs
- TypeScript, Node.js

## Setup

1. Clone the repo:
```
git clone <repo-url>
cd telegram-ai-bot
```

2. Install dependencies:
```
npm install
```

3. Configure `config.js` with your bot token and AI endpoint:
```js
export default {
  telegramBotToken: 'YOUR_BOT_TOKEN',
  ai: {
    baseURL: 'https://your-ai-endpoint/v1',
    apiKey: 'your-api-key',
    model: 'your-model',
  },
};
```

4. Optionally override bot token via environment variable:
```
set BOT_TOKEN=your_token_here
```

5. Run:
```
npm run dev
```

## Scripts

| Command | Description |
|---------|-------------|
| `npm run dev` | Start with nodemon + tsx (hot reload) |
| `npm start` | Start with tsx |
| `npm run build` | Compile TypeScript |
| `npm run typecheck` | Check types without emitting |

## License

MIT
