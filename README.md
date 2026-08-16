# PURU-AI Telegram Bot

Bot Telegram AI berbahasa Go dengan agent tool-calling berbasis langchaingo (`github.com/tmc/langchaingo`), virtual file system berbasis Firebase, memory user, AI tools, dan chat history per-user.

## Fitur

- **AI Chat** — agent tool-calling berbasis langchaingo (`agents.Executor` + klien model OpenAI-compatible sendiri di `internal/ai/openai`) yang menjalankan 27 tools; jawaban final = teks alami executor (tanpa tool `finish` wajib). Model dipanggil **streaming** (`stream:true`) agar generasi panjang tidak kena timeout gateway 503; assembly tool-call SSE diperbaiki sendiri — klien langchaingo v0.1.14 memecah argument tool-call dari gateway yang men-tag tiap fragment (`type:"function"`) menjadi tool call kosong berantai. **Thinking-mode didukung end-to-end** (`reasoning_content`, ala deepseek-reasoner): reasoning yang dihasilkan model di-capture dari respons (SSE & JSON), di-persist ke history sebagai part `reasoning`, lalu di-echo kembali pada request berikutnya (tool-loop scratchpad & history) supaya gateway tidak membalas `400 The reasoning_content in the thinking mode must be passed back to the API`. **Upload gambar** (dokumen gambar maupun pesan photo native Telegram) tidak pernah disimpan ke VFS: di-ringkas oleh model visi terpisah (`VISION_MODEL_URL`, format Gemini) lalu hasilnya di-inject ke agent bersama prompt user dalam marker `[context]`; cek visi cepat dengan `cli -image <gambar> <prompt>`. File non-gambar yang di-upload user **tidak pernah di-preview isinya** — disimpan ke VFS dengan penanda path-only `[User mengupload file: /path]`, dibaca AI sendiri via `read_file`
- **Virtual File System (VFS)** — file system pribadi per user di Firebase (Realtime Database), diakses via AI tools. Node yang hilang (literal `null` dari RTDB) diperlakukan sebagai file **tidak ada** di read & delete — `delete_file` untuk file yang tak ada dilaporkan error, bukan sukses palsu
- **User Memory** — konteks percakapan & info user di `/memory/MEMORY.md`, di-inject ke system prompt. MEMORY.md di-update otomatis setiap `MEMORY_UPDATE_EVERY` pesan via model langchaingo dan dijaga minimal: maks 5 poin penting + baris topik yang sedang dibahas, info usang/irrelevan dibuang
- **Anti-Halusinasi** — system prompt melarang klaim tanpa tool call & filler; jawaban final = teks stop natural executor (tanpa tool `finish`/scold); bila AI berhenti tanpa menghasilkan teks, output tidak dipersist ke history dan request dicoba ulang; pesan user yang ambigu/gibberish membuat AI bertanya klarifikasi langsung tanpa tool; `arguments` tool call dinormalisasi jadi JSON objek valid dan tool-call `id` yang dikosongkan provider (mis. gateway Gemini) di-generate deterministik (`call_<n>`) sehingga tool result selalu berpasangan 1:1 dengan tool call-nya (mencegah `400 function response parts ≠ function call parts`)
- **Persistent History** — chat history via Firebase RTDB + LRU cache (schema `ModelMessage` Vercel AI SDK v7, kompatibel dengan data bot versi TypeScript)
- **E2B Sandbox** — eksekusi kode di lingkungan cloud terisolasi (klien HTTP port wire `@e2b/code-interpreter`, template default `code-interpreter-v1`). Bahasa `runCode` dinormalisasi (`node`/`nodejs`/`js` → `javascript`) karena kernel E2B hanya punya `python` & `javascript`; HTTP 500 tidak lagi menjatuhkan sandbox — hanya 502 & error koneksi yang dianggap sandbox mati. Kode javascript di-wrap blok `{ }` agar isolasi `const`/`let` per eksekusi (kernel JS E2B mempertahankan global scope antar call sehingga ulang deklarasi `const` jadi `SyntaxError`)
- **Web Search** — pencarian Yahoo dengan retry (5x exponential backoff)
- **Web Crawl** — ekstrak data dari website memakai snippet cheerio JavaScript (shim goja di atas goquery); request memakai User-Agent browser agar situs yang memblokir client non-browser (mis. Wikipedia HTTP 403 untuk `Go-http-client/1.1`) tetap bisa di-crawl; snippet boleh berbentuk ekspresi (`$("h1").text()`) maupun statement `return {...};` (dinormalisasi otomatis); URL di-normalisasi dulu (prefix `https://` bila tanpa skema) dan url kosong/invalid/skema non-http(s) mengembalikan error jelas
- **Math & Time** — evaluasi matematika (goja; hasil non-finite seperti `10/0` → Infinity ditolak sebagai error, bukan dikembalikan ke model) dan tools jam dengan timezone IANA
- **Scheduled Tasks** — jadwal tugas one-shot yang dijalankan agent lalu hasilnya dikirim ke private chat user via Telegram; timezone default UTC; waktu disimpan sebagai epoch UTC dengan format input fleksibel (RFC3339 ber-offset, datetime naive di timezone, time-only); task overdue langsung jalan saat bot start; task yang sudah selesai/dibatalkan **dihapus dari RTDB** (tidak menumpuk); diakses via AI tools `schedule_task`, `list_schedules`, `cancel_schedule`
- **Group Chat** — gunakan `/ai <pesan>` di grup (di chat pribadi perintah `/ai` ditolak karena pesan langsung sudah otomatis diproses); semua command juga bisa memakai suffix `@username_bot` (mis. `/menu@nama_bot`). File/dokumen di grup hanya diproses bila caption diawali `/ai`, mis. caption `/ai analisis file ini`
- **Exponential Backoff** — retry hingga 4 kali (1s→2s→4s→8s) pada API call; error 4xx (selain 408/429) langsung berhenti. Kegagalan balasan AI (toolset build, model tidak tersedia, respon kosong) selalu ditulis ke log dengan prefix `[ai]` (per-attempt + `finish_reason` + alasan akhir) supaya fallback "Maaf, saya tidak bisa merespons saat ini." mudah ditelusuri penyebabnya
- **Timeouts & Batas Memori** — agent dibatasi total 5 menit, per-tool 2 menit; loop & context tool dijaga langchaingo executor (regresi `context canceled` dijaga unit test); `crawl` max 1.5MB, `read_file` max 30k char, upload <10MB, history di-truncate max 8k char
- **Markdown Fallback** — retry tanpa parse_mode saat Telegram menolak entitas parse; semua teks keluar di-sanitasi jadi valid UTF-8 (`strings.ToValidUTF8`) agar tidak kena `400 text must be encoded in UTF-8`
- **Per-user API Config via Web** — `/pw <password>` lalu `/login` untuk link halaman pengaturan web mobile-friendly (`/login/{id}/{pw}`), dibangun dengan **Vite + React** (hasil bundle di-embed ke binary Go). Halaman didesain **"9ROUTER_STYLE"** (sidebar kiri permanen + traffic-lights macOS + header judul halaman + toggle tema light/dark, palet warm neutral + brand orange `#E56A4A`, card rounded, font **Inter** + **JetBrains Mono** + Material Symbols), dengan section **Providers**, **Combos**, **Usage**, **Server Logs**, **System Prompt**, **Skills**, **Files**. Konfirmasi/alert memakai **modal custom** + toast auto-dismiss, dan indikator loading yang jelas. Di sana user bisa memakai **provider built-in `puru`** (PURU Gateway, prefix `puru`, tidak bisa dihapus) & menambah **provider sendiri** (OpenAI Compatible ala 9router: name, prefix, API type, base URL, API key, proxy relay ON/OFF, extra headers), melihat **model online provider** (diambil live dari `GET {baseUrl}/models`, badge ONLINE + jumlah model / Failed singkat; provider built-in `puru` hanya menampilkan 1 model = "puru"), **set model default** dengan mengklik chip model (menulis `settings.model = prefix/model-id`), mengelola combos (**fallback only**), **menyuntik system prompt / role** (di-append ke system prompt bawaan), **mengatur Proxy Relay** (toggle ON/OFF untuk relay Vercel built-in, tersimpan di `settings.proxyUrl`), mengelola skills (list/search/install/delete), **melihat & mengedit MEMORY.md** (kosong + Simpan = hapus), dan **menjelajahi file VFS** (breadcrumb folder, baca file, edit & simpan, hapus file/folder dengan konfirmasi). Partial override global via `settings.Effective`; resolusi client per request sehingga aman paralel
- **Providers (OpenAI Compatible, Anthropic dihapus) & Proxy Relay** — section **Providers** di halaman web adalah registry provider per-user (9router provider nodes) yang **memiliki provider built-in `puru`** (PURU Gateway / gateway default server, prefix `puru`, tidak bisa dihapus/diubah; katalog model built-in hanya 1 model = "puru") dan **tidak memiliki kolom model buatan sendiri**: model hanya bisa dipilih dari **catalog online** provider (`/models`) dalam bentuk `prefix/model-id`. Tersimpan di RTDB `providers/{chat}` (`Provider{ID,Name,Prefix,Type,APIType,BaseURL,APIKey,Headers,ProxyURL}`; API key **tidak pernah dikembalikan** ke browser — hanya flag `hasApiKey`). `GET /api/providers` mendaftar, `POST /api/providers` create/update (validasi prefix unik), `POST /api/providers/delete` menghapus provider **sekalian membersihkan referensi `prefix/` di `settings.model` & combo**, `POST /api/providers/check` mengecek online + daftar model (mode "provider tersimpan" atau "value inline" untuk cek sebelum simpan). Saat runtime, `clientFor` (bot & CLI) me-resolve model `prefix/model-id` ke provider-nya (base URL/API key/headers/proxy relay dari provider, model-id sebagai nama model) — fallback ke `settings.Effective` bila prefix tidak dikenal (combo lama yang model polos tetap jalan). Header ekstra & relay didukung di `internal/ai/openai` (`NewWithOptions`): marker `@session` diganti id stabil per-chat (hash chatID via `ai.ChatSessionID`), `@request` diganti `msg_<random>` fresh per request (persis 9router `executors/opencode.js`); semua request model bisa dirutekan lewat **Proxy Relay** Vercel ala 9router (request dikirim ke relay root + header `x-relay-target`/`x-relay-path`, relay yang forward — protokol `proxyFetch.js` 9router) sehingga IP bot tersamar & TLS/fingerprint edge terbantu. Default relay global bisa diset lewat env `PROXY_RELAY_URL` (default kosong = langsung); per-provider via field `proxyUrl`. **Reset** (`POST /api/config/clear`) memakai `settings.DeleteKeepPrompt` — hanya base URL/API key/model/proxy/headers yang dihapus, **system prompt (role) user tetap dipertahankan**; tombol **Reset Connection** di UI dihapus (providers & combos dihapus manual lewat tombol delete masing-masing)
- **Combos** — gabungkan beberapa model provider di bawah satu nama dengan strategi **fallback** (model dicoba berurutan sesuai attempt retry - setiap kegagalan, termasuk 4xx, memajukan ke model/provider berikutnya di combo; Round Robin/Fusion dihapus). Disimpan di RTDB `combos/{chat}` (daftar combo di child `items`, marker `active` di sibling — tidak saling menimpa; layout lama array-di-akar bikin combo "hilang"/aktif terhapus, migrasi otomatis) (independent dari settings/fs/history); saat kombo diaktifkan di halaman web, `clientFor` menimpa nama model efektif per attempt retry (`combos.ModelForActive(ctx, chatID, attempt)` - attempt 1-based, gagal = pindah model/provider berikutnya). Penambahan model memakai **model picker ala 9router ModelSelectModal**: model dikelompokkan per provider dan diambil **online** dari `/models` (placeholder dashed `prefix/model-id` bila provider tidak mengembalikan daftar), search + chip toggle dengan nilai `prefix/model-id`. CRUD web: `GET/POST /api/combos`, `POST /api/combos/activate` (`{id}` / `{id:""}` = nonaktif), `POST /api/combos/delete`
- **Usage & Server Logs** — halaman **Usage** menampilkan ringkasan & histori token per-request per-user (disimpan di RTDB `usage/{chat}/logs`, cap 500 record; kompatibel ala 9router overview + recent requests + chart ringan), sedangkan **Server Logs** menampilkan tail konsol proses bot (ring buffer in-memory cap 1000; dashboard polling `GET /api/logs` tiap 2 detik)
- **Reset terpisah** — `/reset config`, `/reset memory`, `/reset chat` (masing-masing menargetkan data yang berbeda); Reset web (`/api/config/clear`) mempertahankan system prompt user

> ⚠️ Catatan: API key user disimpan **plaintext** di Firebase RTDB (`settings/<id>`), dan password login web juga **plaintext** di `auth/<id>` (diperlukan karena link `/login/{id}/{pw}` memakai nilai mentahnya). RTDB di sini bernama `PUBLIC_RTDB` — pastikan aturan keamanan database membatasi akses read/write path sensitif.
- **Paralel antar-user** — semua user diproses bersamaan (tanpa antrian); jika user yang sama mengirim pesan AI saat request masih berjalan, dibalas `⏳ Masih ada yang lagi diproses, tunggu sebentar ya...` (command non-AI seperti `/menu`, `/clear`, `/reset`, `/login`, `/pw` tetap langsung dieksekusi tanpa diblok)
- **Jawaban final sebagai pesan baru** — hasil AI dikirim sebagai pesan baru (bukan edit), placeholder "🤔 sedang berpikir..." dihapus setelah jawaban terkirim

## Commands

| Command | Deskripsi |
|---------|-----------|
| `/start` | Memulai bot |
| `/menu` | Menampilkan daftar perintah |
| `/clear` | Menghapus riwayat percakapan |
| `/ai <pesan>` | Mengobrol dengan AI (wajib di grup) |
| `/reset chat` | Reset riwayat percakapan + file VFS (lihat `/reset` untuk semua subcommand) |
| `/pw <password>` | Set password login web (minimal 4 karakter, URL-safe) |
| `/login` | Dapatkan link halaman pengaturan web `https://<base_url>/login/<id>/<pw>` |

Di **chat pribadi**, kirim pesan langsung untuk mengobrol dengan AI. Di **grup**, gunakan `/ai` diikuti pesan Anda.

Pengaturan API (base URL, API key, model, system prompt/role), manajemen skills (list/search/install/delete), memory (`MEMORY.md`) dan file VFS (baca/edit/hapus) kini dikelola lewat **halaman web** (`/login`), bukan lagi command Telegram `/config` & `/skills`. Perintah `/config` & `/skills` diarahkan ke `/login`.

Command lama yang masih tersedia (tidak dipromosikan): `/token`, `/info`, `/reset config`, `/reset memory`.

## Arsitektur

```
main.go                 — entrypoint: config, web/health server, long-poll loop + conflict retry
internal/
├── config/             — config loader & validasi env
├── telegram/           — klien Bot API (long-poll synchronous, send/edit/upload, download)
├── app/                — dispatcher command, busy-guard per-user, pipeline pesan, safe reply/send
├── ai/                 — agent langchaingo (executor + ChatPromptTemplate + custom agents.Agent) + 27 tools + processMessage (retry & normalisasi arguments); `NewModelWithOptions` mendukung header ekstra provider + proxy relay (`@session`/`@request` marker) & `ChatSessionID` per-chat
├── messages/           — ModelMessage (kompatibel Vercel AI SDK v7), pruneMessages port
├── firebase/           — REST RTDB (GET/PUT/DELETE .json, base64url) + ListKeys shallow
├── vfs/                — virtual file system per-user (DeleteDir pindai store content/index, tahan index korup)
├── history/            — history persistence (LRU+TTL + RTDB)
├── settings/           — per-user API config (base URL/key/model/system prompt) di RTDB + cache TTL; `DeleteKeepPrompt` mempertahankan system prompt saat reset
├── usage/              — per-request token usage per-user (RTDB `usage/{chat}/logs`): overview cards + recent requests di halaman web
├── combos/             — kombo model per-user (fallback only) + seleksi model per attempt retry untuk `clientFor`
├── providers/          — registry provider per-user ala 9router (RTDB `providers/{chat}`): CRUD, live `/models` fetch (status ONLINE + catalog), `Resolve` `prefix/model-id` → provider config untuk `clientFor` + **provider built-in `puru`** (prefix `puru`, tidak bisa dihapus). Hanya OpenAI-compatible (Anthropic dihapus)
├── servelog/           — ring buffer log in-memory (Server Logs di halaman web)
├── tokens/             — tiktoken-go (o200k_base)
├── skills/             — loader/manifest SKILL.md + registry manager (import logika `pkg/skills` picoclaw: code search, install disk→VFS, builtin)
├── prompt/             — system prompt (text/template, braces di-escape)
├── memory/             — auto-update MEMORY.md (model langchaingo)
├── jsrun/              — goja: cheerio shim + evaluate math
├── e2b/                — client E2B murni HTTP (sandbox/execute/files)
├── auth/               — password login web per-user di RTDB `auth/{chatID}` (`/pw` & `/login`)
├── scheduler/           — scheduler tugas one-shot (poll RTDB, parse waktu fleksibel, inject prompt ke agent, kirim hasil ke private chat)
└── web/                — halaman settings `/login/{id}/{pw}` (mobile-friendly, embed static) + API JSON; menggantikan health/ (JSON health `/` & `/health` dipertahankan)
```

## Tools yang Tersedia untuk AI

list_directory, read_file, write_file, edit_file, delete_file, move_file, send_file, search_web, crawl, get_current_time, calculate_math, e2b_sandbox_create, e2b_run_code, e2b_install_package, e2b_send_file, e2b_sandbox_kill, create_skill, use_skills, delete_skill, search_skills, install_skill, schedule_task, list_schedules, cancel_schedule.

Skema tools memakai JSON-Schema yang valid untuk provider ketat: `required` selalu array saat ada dan dihilangkan bila kosong (dijamin unit test).

## Tech Stack

- Go 1.26
- [langchaingo](https://github.com/tmc/langchaingo) — agent loop (`agents.Executor`) + prompt/sesi; model OpenAI-compatible dipakai klien sendiri di `internal/ai/openai` (streaming SSE dengan assembly tool-call yang diperbaiki)
- [goja](https://github.com/dop251/goja) — runtime JS untuk kode cheerio & evaluasi math
- [goquery](https://github.com/PuerkitoBio/goquery) — HTML parsing untuk crawl
- [tiktoken-go](https://github.com/tiktoken-go/tokenizer) — tokenizer `o200k_base`
- [godotenv](https://github.com/joho/godotenv) — load `.env`
- [picoclaw](https://github.com/sipeed/picoclaw) — `pkg/skills` (registry GitHub code search `filename:SKILL.md`, ClawHub, installer) diadaptasi ke VFS
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

3. (Opsional, jika `internal/web/dist/` belum ada) build halaman web Vite + React:
```
cd web && npm install && npm run build && cd ..
```
Hasil bundle di-embed ke binary Go (`go:embed all:dist` di `internal/web/web.go`).

4. Jalankan:
```
go run .
```

## Environment Variables

| Variable | Deskripsi |
|----------|-----------|
| `BOT_TOKEN` | Token bot Telegram |
| `PUBLIC_RTDB` | Base URL Firebase Realtime Database |
| `E2B_APIKEY` | API key E2B untuk eksekusi kode |

Variabel di atas **wajib**. Aplikasi akan keluar dengan error jika ada yang kurang.

### Variabel Opsional

| Variabel | Default | Deskripsi |
|----------|---------|-----------|
| `HOSTNAME` | `localhost` | Alamat bind web/health server |
| `PORT` | `3000` | Port web/health server |
| `PUBLIC_BASE_URL` | *(kosong)* | URL publik untuk link halaman `/login/{id}/{pw}` (mis. `https://bot.example.com`). Bila kosong, default ke `http://localhost:{port}` — `/login` tidak pernah menghasilkan link kosong |
| `TEMPERATURE` | `0` | Temperature model AI |
| `MAX_LOOP` | `20` | Iterasi maksimal agent per request |
| `HISTORY_CACHE_MAX` | `500` | User maksimal di LRU cache |
| `HISTORY_CACHE_TTL` | `600000` | TTL cache dalam ms (default 10 menit) |
| `MEMORY_UPDATE_EVERY` | `3` | Interval pesan user untuk auto-update MEMORY.md |
| `MEMORY_MAX_CHARS` | `8000` | Cap konten MEMORY.md saat di-inject ke system prompt |
| `SCHEDULE_POLL_INTERVAL` | `15` | Interval detik pengecekan scheduler jadwal tugas |
| `VISION_MODEL_URL` | `https://puruboy-api.vercel.app/api/ai/gemini-v3` | URL model visi (format Gemini) untuk meringkas gambar yang di-upload user |
| `OPENAI_BASEURL` | `https://betatestervueui2-b.hf.space/v1` | Base URL API OpenAI-compatible (bila kosong dipakai default) |
| `OPENAI_APIKEY` | `sk-843e3f05f05eacfe-55n2je-f2c2b844` | API key (bila kosong dipakai default) |
| `OPENAI_MODEL` | `puru` | Nama model (bila kosong dipakai default) |
| `PROXY_RELAY_URL` | relay Vercel built-in (`https://vercel-relay-ijhklxg99-rikipurpur98-dotcoms-projects.vercel.app/`) | Base URL relay Vercel/edge ala 9router untuk semua request model OpenAI-compatible (header `x-relay-target`/`x-relay-path`). Bila kosong = koneksi langsung. Per-user bisa diatur via halaman web (toggle **Proxy Relay ON/OFF** di section System Prompt & modal provider) |
| `E2B_TEMPLATE` | `code-interpreter-v1` | Template sandbox E2B untuk eksekusi kode (template `default` sudah dihapus dari platform E2B) |
| `GITHUB_TOKEN` | *(kosong)* | Token GitHub untuk search skill di halaman web. **Wajib** — GitHub code search API menolak tanpa token (HTTP 401 → hasil kosong) |
| `CLAWHUB_APIKEY` | *(kosong)* | Token ClawHub (opsional). Registry ClawHub aktif bila diisi (base URL default `https://clawhub.ai`) |

## Debug CLI

Chat langsung dengan agent PURU-AI dari terminal **tanpa Telegram** — berguna untuk
debugging cepat karena pipeline (prune → cap → proses → persist → memory) identik
dengan handler bot asli.

```bash
go run ./cmd/cli "halo, siapa kamu?"   # one-shot, jawaban ke stdout
cli.bat "halo"                         # alias Windows
./cli.sh "halo"                        # alias Unix
go run ./cmd/cli                       # REPL interaktif (ketik /exit untuk keluar)
```

Opsi:

| Flag | Deskripsi |
|------|-----------|
| `-chat <id>` | Chat/user id debug (default `-777`, terpisah dari user publik) |
| `-reset` | Hapus history + VFS untuk chat id lalu exit |
| `-verbose` | Tampilkan trace tool yang benar-benar tereksekusi (tool-call + output result penuh) + token usage + `finish_reason` |
| `-json` | Output satu objek JSON machine-readable ke stdout (`text`, `steps`, `usage`) — untuk parsing/CI; jawaban tidak dicetak terpisah |
| `-dump <dir>` | Simpan transcript JSON per run (`run-<unix>.json`: text + steps + usage) untuk pembanding faktual apa yang agent eksekusi |
| `-timeout <dur>` | Batas waktu proses di sisi klien (mis. `5m`, `90s`); default 0 = tidak dibatasi CLI (batas internal agent `MAX_LOOP`/5 menit tetap berlaku) |
| `-save-files <dir>` | Simpan file hasil `send_file` ke direktori (default: hanya di-print) |
| `-no-memory` | Nonaktifkan auto-update MEMORY.md |

History & VFS disimpan di Firebase RTDB per chat id, jadi konteks multi-turn tetap
bertahan antar-run.

## Docker

Build dan jalankan dengan Docker (frontend Vite + React otomatis di-build di stage `web-build`, lalu bundle-nya di-embed ke binary Go):
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
| `go run ./cmd/cli "pesan"` | Debug CLI: chat langsung dengan agent (tanpa Telegram) |
| `go test ./...` | Unit test (ai, jsrun, messages, prompt, settings) |
| `go vet ./...` | Static analysis |
| `gofmt -l .` | Cek format |

## Lisensi

MIT