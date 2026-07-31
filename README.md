# PURU-AI Telegram Bot

Bot Telegram AI yang didukung oleh **Vercel AI SDK** dengan virtual file system berbasis Firebase, memory user, AI tools, dan chat history per-user.

## Fitur

- **AI Chat** — AI konversasional menggunakan `ToolLoopAgent` dari Vercel AI SDK dengan streaming respons
- **Virtual File System (VFS)** — Setiap user mendapatkan file system pribadi yang disimpan di Firebase (Realtime Database), dapat diakses via AI tools
- **User Memory** — Persona user (`/memory/USER.md`) dan persona AI (`/memory/SOUL.md`) di-inject ke system prompt; informasi percakapan disimpan di `/memory/MEMORY.md`
- **Persistent History** — Chat history tetap tersimpan setelah bot restart via Firebase RTDB + LRU cache
- **E2B Sandbox** — Eksekusi kode di lingkungan cloud terisolasi dengan instalasi package otomatis
- **Web Search** — Integrasi pencarian Yahoo dengan automatic retry (5x exponential backoff)
- **Web Crawl** — Ambil dan ringkas konten website
- **Math & Time** — Evaluasi matematika bawaan dan tools jam dengan timezone
- **Group Chat** — Gunakan `/ai <pesan>` untuk berinteraksi dengan bot di grup
- **Exponential Backoff** — Retry hingga 4 kali (1s→2s→4s→8s) pada API call; error 4xx (selain 408/429) langsung berhenti. Web search retry 5 kali (1s→16s)
- **Timeouts & Batas Memori** — `ToolLoopAgent` dibatasi timeout total 5 menit, per-step & per-tool 2 menit; `crawl` max 1.5MB, `read_file` max 30k char, upload dokumen max 10MB, isi history di-truncate max 8k char/message
- **Markdown Fallback** — Menangani error parse Telegram dengan retry tanpa parse mode

## Commands

| Command | Deskripsi |
|---------|-----------|
| `/start` | Memulai bot |
| `/menu` | Menampilkan daftar perintah |
| `/clear` | Menghapus riwayat percakapan |
| `/token` | Melihat penggunaan token |
| `/info` | Melihat info memory (`/info memory|user|soul`) |
| `/reset` | Menghapus semua data (riwayat + file VFS) |
| `/skills` | Menampilkan daftar skill |
| `/skills search <query>` | Mencari skill dari GitHub |
| `/skills install <url>` | Install skill dari GitHub (dukung `https://github.com/...` atau `owner/repo`) |
| `/skills info <nama>` | Menampilkan detail skill |
| `/skills read <nama>` | Membaca isi skill |
| `/skills delete <nama>` | Menghapus skill |
| `/skills migrate` | Migrate skill lama ke format baru |
| `/ai <pesan>` | Mengobrol dengan AI (wajib di grup) |

Di **chat pribadi**, kirim pesan langsung untuk mengobrol dengan AI. Di **grup**, gunakan `/ai` diikuti pesan Anda. Gunakan `/skills` untuk mengelola skill AI.

## Arsitektur

```
src/
├── index.ts      — Entry point, memulai bot + health server
├── bot.ts        — Setup Telegram bot (commands, message handler, safeReply/safeEdit)
├── agent.ts      — ToolLoopAgent dengan 21 tools + processMessage dengan retry + memory injection
├── vfs.ts        — Firebase VFS (read, write, edit, delete, list, deleteAll)
├── history.ts    — Chat history (LRU cache + Firebase RTDB persist)
├── tools.ts      — ToolNames type union
├── config.ts     — Config loader (env var BOT_TOKEN override)
├── skills-loader.ts — Parsing metadata skill, listing, loading
├── skills-registry.ts — Instalasi skill dari GitHub dan pencarian
└── server.ts     — HTTP health check server (port 3000)
```

### Tools yang Tersedia untuk AI

1. **list_directory** — Lihat daftar file/folder di direktori VFS
2. **read_file** — Baca isi file dari VFS
3. **write_file** — Tulis/buat file di VFS
4. **edit_file** — Cari dan ganti teks dalam file VFS
5. **delete_file** — Hapus file dari VFS
6. **move_file** — Pindahkan atau ganti nama file di VFS
7. **send_file** — Kirim file dari VFS ke chat Telegram
8. **search_web** — Pencarian Yahoo dengan retry
9. **crawl** — Ambil dan ekstrak teks dari halaman web menggunakan cheerio
10. **get_current_time** — Waktu saat ini di timezone IANA manapun
11. **calculate_math** — Evaluasi ekspresi matematika
12. **e2b_sandbox_create** — Buat sandbox E2B terisolasi
13. **e2b_run_code** — Eksekusi kode dari VFS di sandbox E2B
14. **e2b_install_package** — Install package di sandbox E2B
15. **e2b_send_file** — Kirim file dari sandbox E2B ke Telegram
16. **e2b_sandbox_kill** — Tutup sandbox E2B
17. **create_skill** — Buat skill baru dengan metadata di direktori /skills/
18. **use_skills** — Baca dan gunakan skill dari direktori /skills/
19. **delete_skill** — Hapus skill dari direktori /skills/
20. **search_skills** — Cari skill dari GitHub
21. **install_skill** — Install skill dari repository GitHub

## Tech Stack

- [grammY](https://grammy.dev/) — Telegram Bot Framework
- [Vercel AI SDK](https://sdk.vercel.ai/) — AI streaming, tool calling, `ToolLoopAgent`
- [Firebase Realtime Database](https://firebase.google.com/) — Penyimpanan file user (VFS)
- [@langchain/core](https://www.npmjs.com/package/@langchain/core) — Prompt template system
- [lru-cache](https://www.npmjs.com/package/lru-cache) — LRU cache in-memory untuk chat history
- [Zod](https://zod.dev/) — Validasi schema untuk input AI tools
- TypeScript, Node.js

## Instalasi

1. Clone repo:
```
git clone <repo-url>
cd telegram-ai-bot
```

2. Install dependencies:
```
npm install
```

3. Copy `.env.example` ke `.env` dan isi nilai Anda:
```
cp .env.example .env
```

4. Jalankan:
```
npm run dev
```

## Environment Variables

| Variable | Deskripsi |
|----------|-----------|
| `BOT_TOKEN` | Token bot Telegram |
| `PUBLIC_RTDB` | Base URL Firebase Realtime Database |
| `E2B_APIKEY` | API key E2B untuk eksekusi kode |
| `OPENAI_BASEURL` | Base URL API OpenAI-compatible |
| `OPENAI_APIKEY` | API key |
| `OPENAI_MODEL` | Nama model |

Semua variable di atas **wajib**. Aplikasi akan keluar dengan error jika ada yang kurang.

### Variable Opsional

| Variable | Default | Deskripsi |
|----------|---------|-----------|
| `HOSTNAME` | `localhost` | Alamat bind server |
| `PORT` | `3000` | Port server |
| `TEMPERATURE` | `0` | Temperature model AI |
| `MAX_LOOP` | `20` | Iterasi maksimal agent per request |
| `HISTORY_CACHE_MAX` | `500` | User maksimal di LRU cache |
| `HISTORY_CACHE_TTL` | `600000` | TTL cache dalam ms (default 10 menit) |

Variable ini opsional. Aplikasi menggunakan nilai default jika tidak diset.

## Docker

Build dan jalankan dengan Docker:
```bash
docker build -t puru-ai .
docker run -d --env-file .env -p 3000:3000 puru-ai
```

Atau pull dari Docker Hub:
```bash
docker pull purujawa/puru-ai:latest
docker run -d --env-file .env -p 3000:3000 purujawa/puru-ai:latest
```

### CI/CD

GitHub Actions secara otomatis build dan push ke Docker Hub saat push ke `main`. Secrets yang diperlukan:
- `DOCKERHUB_USERNAME`
- `DOCKERHUB_TOKEN`

## Scripts

| Command | Deskripsi |
|---------|-----------|
| `npm run dev` | Jalankan dengan nodemon + tsx (hot reload) |
| `npm start` | Jalankan dengan tsx |
| `npm run build` | Compile TypeScript |
| `npm run build:bundle` | Bundle ke single file dengan esbuild |
| `npm run typecheck` | Cek types tanpa emit |

## Lisensi

MIT
