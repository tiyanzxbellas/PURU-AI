# PURU-AI Telegram Bot

Bot Telegram AI berbahasa Go dengan agent loop (tool-calling), virtual file system berbasis Firebase, memory user, AI tools, dan chat history per-user.

## Fitur

- **AI Chat** — agent tool-loop port dari `ToolLoopAgent` (Vercel AI SDK) yang memanggil model streaming (SSE) dan menjalankan 22 tools
- **Virtual File System (VFS)** — file system pribadi per user di Firebase (Realtime Database), diakses via AI tools
- **User Memory** — konteks percakapan & info user di `/memory/MEMORY.md`, di-inject ke system prompt. MEMORY.md di-update otomatis setiap `MEMORY_UPDATE_EVERY` pesan via pemanggilan AI internal (maks 10 poin bernomor)
- **Anti-Halusinasi** — system prompt melarang klaim tanpa tool call; jika last step AI tidak memanggil tool maupun `finish`, AI ditegur dengan direktif `[system]` lalu dijalankan ulang sekali. Hanya round yang menyelesaikan turn yang dipersist ke history — stub text-only kosong dari round yang ditegur tidak mencemari konteks. Pesan user yang ambigu/gibberish membuat AI bertanya klarifikasi (via `finish`) tanpa menebak dengan tool
- **Persistent History** — chat history via Firebase RTDB + LRU cache (schema `ModelMessage` Vercel AI SDK v7, kompatibel dengan data bot versi TypeScript)
- **E2B Sandbox** — eksekusi kode di lingkungan cloud terisolasi (klien HTTP port wire `@e2b/code-interpreter`, template default `code-interpreter-v1`)
- **Web Search** — pencarian Yahoo dengan retry (5x exponential backoff)
- **Web Crawl** — ekstrak data dari website memakai snippet cheerio JavaScript (shim goja di atas goquery); request memakai User-Agent browser agar situs yang memblokir client non-browser (mis. Wikipedia HTTP 403 untuk `Go-http-client/1.1`) tetap bisa di-crawl; snippet boleh berbentuk ekspresi (`$("h1").text()`) maupun statement `return {...};` (dinormalisasi otomatis)
- **Math & Time** — evaluasi matematika (goja) dan tools jam dengan timezone IANA
- **Group Chat** — gunakan `/ai <pesan>` di grup
- **Exponential Backoff** — retry hingga 4 kali (1s→2s→4s→8s) pada API call; error 4xx (selain 408/429) langsung berhenti
- **Timeouts & Batas Memori** — agent dibatasi total 5 menit, per-step & per-tool 2 menit; context step tetap hidup sampai semua tool selesai (regresi `context canceled` dijaga unit test); `crawl` max 1.5MB, `read_file` max 30k char, upload <10MB, history di-truncate max 8k char
- **Markdown Fallback** — retry tanpa parse_mode saat Telegram menolak entitas parse; semua teks keluar di-sanitasi jadi valid UTF-8 (`strings.ToValidUTF8`) agar tidak kena `400 text must be encoded in UTF-8`
- **Per-user API Config** — user bisa memakai API sendiri (`/config api|model|base`) disimpan di Firebase RTDB, partial override atas default server; resolusi client per request sehingga aman paralel
- **Reset terpisah** — `/reset config`, `/reset memory`, `/reset chat` (masing-masing menargetkan data yang berbeda)

> ⚠️ Catatan: API key user disimpan **plaintext** di Firebase RTDB (`settings/<id>`). RTDB di sini bernama `PUBLIC_RTDB` — pastikan aturan keamanan database membatasi akses read/write path sensitif.
- **Paralel antar-user** — semua user diproses bersamaan (tanpa antrian); jika user yang sama mengirim pesan saat request masih berjalan, dibalas `⏳ Masih ada yang lagi diproses, tunggu sebentar ya...`
- **Jawaban final sebagai pesan baru** — hasil AI dikirim sebagai pesan baru (bukan edit), placeholder "🤔 sedang berpikir..." dihapus setelah jawaban terkirim

## Commands

| Command | Deskripsi |
|---------|-----------|
| `/start` | Memulai bot |
| `/menu` | Menampilkan daftar perintah |
| `/clear` | Menghapus riwayat percakapan |
| `/token` | Melihat penggunaan token |
| `/info` | Melihat info memory (`/info memory`) |
| `/config` | Melihat status API yang dipakai |
| `/config api <key>` | Set API key sendiri |
| `/config model <nama>` | Set model sendiri |
| `/config base <url>` | Set base URL sendiri (OpenAI-compatible) |
| `/config clear` | Hapus pengaturan API sendiri (kembali ke default server) |
| `/config test` | Tes koneksi ke API yang dipakai |
| `/reset` | Melihat daftar reset |
| `/reset config` | Hapus pengaturan API sendiri |
| `/reset memory` | Hapus MEMORY.md (ingatan user) |
| `/reset chat` | Hapus riwayat percakapan + file VFS |
| `/skills` | Menampilkan daftar skill |
| `/skills search <query>` | Mencari skill dari GitHub |
| `/skills install <url>` | Install skill dari GitHub (dukung `https://github.com/...` atau `owner/repo`) |
| `/skills info <nama>` | Menampilkan detail skill |
| `/skills read <nama>` | Membaca isi skill |
| `/skills delete <nama>` | Menghapus skill |
| `/skills migrate` | Migrate skill lama ke format baru |
| `/ai <pesan>` | Mengobrol dengan AI (wajib di grup) |

Di **chat pribadi**, kirim pesan langsung untuk mengobrol dengan AI. Di **grup**, gunakan `/ai` diikuti pesan Anda.

## Arsitektur

```
main.go                 — entrypoint: config, health server, long-poll loop + conflict retry
internal/
├── config/             — config loader & validasi env
├── telegram/           — klien Bot API (long-poll synchronous, send/edit/upload, download)
├── app/                — dispatcher command, busy-guard per-user, pipeline pesan, safe reply/send
├── ai/                 — client chat/completions + agent (22 tool) + processMessage (retry & guard)
├── messages/           — ModelMessage (kompatibel Vercel AI SDK v7), pruneMessages port
├── firebase/           — REST RTDB (GET/PUT/DELETE .json, base64url)
├── vfs/                — virtual file system per-user
├── history/            — history persistence (LRU+TTL + RTDB)
├── settings/           — per-user API config (base URL/key/model) di RTDB + cache TTL
├── tokens/             — tiktoken-go (o200k_base)
├── skills/             — loader/manifest SKILL.md + registry GitHub
├── prompt/             — system prompt (text/template, braces di-escape)
├── memory/             — auto-update MEMORY.md (chat completions SSE-tolerant)
├── jsrun/              — goja: cheerio shim + evaluate math
├── e2b/                — client E2B murni HTTP (sandbox/execute/files)
└── health/             — HTTP health check
```

## Tools yang Tersedia untuk AI

list_directory, read_file, write_file, edit_file, delete_file, move_file, send_file, search_web, crawl, get_current_time, calculate_math, e2b_sandbox_create, e2b_run_code, e2b_install_package, e2b_send_file, e2b_sandbox_kill, create_skill, use_skills, delete_skill, search_skills, install_skill, finish.

## Tech Stack

- Go 1.26
- [goja](https://github.com/dop251/goja) — runtime JS untuk kode cheerio & evaluasi math
- [goquery](https://github.com/PuerkitoBio/goquery) — HTML parsing untuk crawl
- [tiktoken-go](https://github.com/tiktoken-go/tokenizer) — tokenizer `o200k_base`
- [godotenv](https://github.com/joho/godotenv) — load `.env`
- Firebase Realtime Database — storage VFS & history

## Instalasi

1. Clone repo:
```
git clone <repo-url>
cd telegram-ai-bot
```

2. Copy `.env.example` ke `.env` dan isi nilai Anda:
```
cp .env.example .env
```

3. Jalankan:
```
go run .
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

### Variabel Opsional

| Variabel | Default | Deskripsi |
|----------|---------|-----------|
| `HOSTNAME` | `localhost` | Alamat bind server |
| `PORT` | `3000` | Port server |
| `TEMPERATURE` | `0` | Temperature model AI |
| `MAX_LOOP` | `20` | Iterasi maksimal agent per request |
| `HISTORY_CACHE_MAX` | `500` | User maksimal di LRU cache |
| `HISTORY_CACHE_TTL` | `600000` | TTL cache dalam ms (default 10 menit) |
| `MEMORY_UPDATE_EVERY` | `3` | Interval pesan user untuk auto-update MEMORY.md |
| `MEMORY_MAX_CHARS` | `8000` | Cap konten MEMORY.md saat di-inject ke system prompt |
| `E2B_TEMPLATE` | `code-interpreter-v1` | Template sandbox E2B untuk eksekusi kode (template `default` sudah dihapus dari platform E2B) |

## Docker

Build dan jalankan dengan Docker:
```bash
docker build -t puru-ai .
docker run -d --env-file .env -p 3000:3000 puru-ai
```

### CI/CD

GitHub Actions otomatis build & push ke Docker Hub saat push ke `main`. Secrets yang digunakan:
- `DOCKERHUB_USERNAME`
- `DOCKERHUB_TOKEN`

## Scripts

| Command | Deskripsi |
|---------|-----------|
| `go run .` | Jalankan bot lokal |
| `go build -o dist/puru-ai .` | Compile binary |
| `go test ./...` | Unit test (ai, jsrun, messages, prompt, settings) |
| `go vet ./...` | Static analysis |
| `gofmt -l .` | Cek format |

## Lisensi

MIT