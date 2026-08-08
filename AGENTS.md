# AGENTS.md

## Mulai Cepat
- `go run .` — jalankan bot lokal (load `.env` via godotenv)
- `go build -o dist/puru-ai .` — kompilasi binary
- `go vet ./...` — static analysis
- `go test ./...` — unit test (jsrun, messages, prompt)
- `gofmt -l .` — cek format

## Konfigurasi & Secrets
- Konfigurasi memakai environment variables, divalidasi saat startup di `internal/config/config.go`.
- File `.env` di root repo hold secrets.
- Firebase RTDB base URL dimuat dari `PUBLIC_RTDB`.
- Konfigurasi opsional (dengan default):
  - `HOSTNAME` — alamat bind health server (default: `localhost`)
  - `PORT` — port health server (default: `3000`)
  - `TEMPERATURE` — temperature model AI (default: `0`)
  - `MAX_LOOP` — iterasi maksimal agent per request (default: `20`)
  - `HISTORY_CACHE_MAX` — user maksimal di LRU cache (default: `500`)
  - `HISTORY_CACHE_TTL` — TTL cache dalam ms (default: `600000`)
  - `MEMORY_UPDATE_EVERY` — interval pesan user untuk auto-update MEMORY.md (default: `3`)
  - `MEMORY_MAX_CHARS` — cap konten MEMORY.md saat di-inject (default: `8000`)

## Arsitektur (Go, module `github.com/purujawa/puru-ai`)
- `main.go` — entrypoint: config → health server → `getMe` → `deleteWebhook(drop_pending)` → konflict-retry loop (exit setelah 5 conflict berurut).
- `internal/config` — config loader.
- `internal/telegram` — klien Telegram Bot API **ditulis manual** (poling `getUpdates` synchronous, `sendMessage`, `editMessageText`, `sendDocument/Audio/Video`, `getFile`+download). Sebab utama ditulis sendiri: bot memproses update **sekali-sekali secara beruntun** (satu goroutine) seperti `bot.start()` di grammY, dan deteksi conflict (409) harus presisi.
- `internal/app` — handler bot: dispatcher command, pipeline pesan (prune→cap→sanitize→token→memory), safe reply/edit, upload dokumen (maks 10MB).
- `internal/ai` — klien chat/completions (SSE-tolerant) + `ToolLoop` port dari ToolLoopAgent + 22 tools + prosesMessage (retry + guard tegur).
- `internal/messages` — model pesan `ModelMessage` kompatibel dengan schema Vercel AI SDK (round-trip field tak dikenal dipertahankan) + port `pruneMessages`/`EnsureStartsWithUser`/`CapUserTurns`/`Sanitize`.
- `internal/firebase` — helper REST ing RTDB (GET/PUT/DELETE `.json` + base64url path).
- `internal/vfs` — virtual file system per-user di RTDB.
- `internal/history` — persistensi history (LRU+TTL + RTDB; hasil copy bukan mutasi cache).
- `internal/tokens` — wrapper tiktoken-go (`o200k_base`).
- `internal/skills` — parsing SKILL.md (frontmatter) + registry GitHub (search/tree/install/migrate).
- `internal/prompt` — system prompt `text/template`. Literal braces seperti `/skills/{{name}}/SKILL.md` WAJIB di-escape (`{{"{{"}}` / `{{"}}"}}`), analog larvae AGENTS lama.
- `internal/memory` — auto-update `/memory/MEMORY.md` via `chat/completions` internal (SSE-tolerant seperti streamText).
- `internal/jsrun` — runtime goja untuk `crawl` (shim cheerio di atas goquery) + `calculate_math` (Math.*/alias).
- `internal/e2b` — client E2B murni HTTP (port wire `@e2b/code-interpreter`): create/kcreate `POST https://api.<domain>/sandboxes` (X-API-KEY), runCode `POST ...:49999/execute` (NDJSON), files `GET/POST ...:49983/files`.
- `internal/health` — HTTP health check.

## Perilaku Penting (dipertahankan dari generasi sebelumnya)
- **Retry**: API call retry hingga 4 kali dengan exponential backoff (1s→2s→4s→8s, cap 30s). Error 4xx selain 408/429 TIDAK di-retry (`isNonRetryableError`). Web search retry 5 kali (1s→16s, cap 30s).
- **Timeout agent** (`agent.go`): per-step 120s, per-tool 120s, total 300s via `context.WithTimeout`; model dipanggil streaming (SSE) tapi hasil dirangkai penuh.
- **Batas data**: crawl baca body max 1.5MB & hasil max 20k char (tool), `read_file` max 30 ribu char; upload dokumen max 10MB; isi history di-truncate max 8k char/message (`messages.Sanitize`).
- **Persistensi history**: path `history/{chat}/messages|tokens|meta` di RTDB, LRU+TTL di dalam proses (hasil getHistory berupa copy; array kosong tidak di-cache). JSON schema = `ModelMessage` Vercel AI SDK v7 → data user lama tetap terbaca & round-trip field tidak hilang.
- **Batas history**: sebelum dikirim ke model, history di-prune (`reasoning: before-last-message`, `toolCalls: before-last-6-messages`, kosong dihapus) lalu di-cap maksimal 5 pesan user (`messages.CapUserTurns`).
- **Conflict loop** (`main.go`): conflict beruntun ≥5 → `os.Exit(1)` agar platform restart bersih.
- **Pemrosesan sekuensial**: update dipotong dan diproses satu-per-satu (`for _, u := range updates`), bukan paralel — demi menjaga RAM.
- **Anti-halusinasi & protocol guard** (`internal/ai/agent.go`): system prompt melarang klaim tanpa tool call & filler; `runOnce` berhenti saat tool `finish` dipanggil atau jumlah step = `MAX_LOOP`. Di `ProcessMessage`: bila result ronde tanpa tool/finish (stop natural teks-polos), AI **ditegur** dengan direktif ber-tag `[system]` lalu dijalankan ulang sekali (`maxCorrection = 1`); token & responseMessages kedua ronde digabung.
- **Elusive memory**: auto-update `/memory/MEMORY.md` setelah `MEMORY_UPDATE_EVERY` pesan user (counter `history/{id}/meta`), error non-fatal. MEMORY.md dikelola sistem, AI dilarang baca/tulis sendiri.
- **Safe reply/edit**: fallback parse-mode Markdown bila Telegram menolak entitas parse; respon >4096 char dikirim sebagai file.
- **E2B**: satu sandbox per chat, TTL 5 menit idle, auto-kill; `runCode` hasil NDJSON `{type: stdout/stderr/result/error}`.
- **Math & crawl**: evaluasi lewat goja. `calculate_math` menyediakan alias Math (`sqrt`, `pow`, ...). `crawl` mengharapkan snippet cheerio JavaScript (shim compat: `text`, `html`, `attr`, `val`, `length`, `first`, `last`, `eq`, `find`, `parent`, `parents`, `children`, `filter`, `each`, `map`, `get`, `toArray`, passthrough `$(el)`).

## Konvensi
- Go toolchain 1.26+; format `gofmt`; tidak ada linter wajib selain `go vet`.
- Import internal memakai module path `github.com/purujawa/puru-ai/internal/...`.
- Response ke user dalam Bahasa Indonesia & singkat; system prompt bahasa Inggris.
- **Setiap perubahan codebase WAJIB di-update `AGENTS.md` dan `README.md`.**

## Git & Tagging
- Setiap commit WAJIB diikuti annotated tag (`git tag -a`).
- Format tag `v<major>.<minor>.<patch>` — semver (patch fix, minor fitur, major breaking).
- Pesan tag harus berisi penjelasan detail perubahan.
- Sesudah commit & tag: `git push origin main --follow-tags`.