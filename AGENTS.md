# AGENTS.md

## Mulai Cepat
- `npm run dev` — nodemon + tsx (hot reload)
- `npm start` — tsx
- `npm run build` — tsc
- `npm run build:bundle` — esbuild single-file bundle
- `npm run typecheck` — tsc --noEmit
- Tidak ada test, lint, atau formatter

## Konfigurasi & Secrets
- Konfigurasi menggunakan environment variables, divalidasi saat startup di `src/config.ts`.
- File `.env` di root repo menyimpan secrets.
- Firebase RTDB base URL dimuat dari environment variable `PUBLIC_RTDB`.
- Konfigurasi opsional (dengan default):
  - `HOSTNAME` — Alamat bind server (default: `localhost`)
  - `PORT` — Port server (default: `3000`)
  - `TEMPERATURE` — Temperature model AI (default: `0`)
  - `MAX_LOOP` — Iterasi maksimal agent per request (default: `20`)
  - `HISTORY_CACHE_MAX` — User maksimal di LRU cache (default: `500`)
  - `HISTORY_CACHE_TTL` — TTL cache dalam ms (default: `600000` / 10 menit)

## Arsitektur
- `src/index.ts` — entrypoint, memulai health server lalu bot dalam conflict-retry loop
- `src/bot.ts` — Setup grammY Bot, commands (`/start`, `/menu`, `/clear`, `/token`, `/info`, `/reset`, `/ai`, `/skills`), message handlers
- `src/agent.ts` — `ToolLoopAgent` (Vercel AI SDK) dengan 21 tools; `temperature` dan `maxLoop` bisa dikonfigurasi
- `src/vfs.ts` — Virtual file system per-user disimpan di Firebase Realtime Database
- `src/history.ts` — Persistensi chat history (LRU cache + Firebase RTDB)
- `src/e2b.ts` — E2B sandbox (satu per chat, timeout 5 menit, auto-killed saat idle)
- `src/skills-loader.ts` — Parsing metadata skill, listing, loading dari VFS
- `src/skills-registry.ts` — Instalasi skill dari GitHub dan pencarian. Otomatis mendeteksi root direktori SKILL.md untuk menghindari nesting path yang salah.
- `src/server.ts` — HTTP health check di port 3000

## Perilaku Penting
- **Retry**: API call retry hingga 8 kali (3s→...→45s exponential backoff). Web search retry 5 kali (1s→16s).
- **Persistensi history** (`history.ts`): Chat history disimpan di Firebase RTDB dengan LRU cache (maks 500 user, TTL 10 menit). Cache miss load dari Firebase; write bersifat synchronous (await). Array kosong tidak di-cache.
- **Batas history** (`bot.ts`): Sebelum dikirim ke AI, history hanya di-prune (hapus reasoning lama & tool-call lama via `pruneMessages`) lalu dibatasi maksimal 5 pesan user (`capUserTurns`). Turn user terlama dihapus beserta jawaban assistant & tool-call-nya, urutan sisanya dipertahankan.
- **Pemrosesan sekuensial**: Bot menggunakan `bot.start()` (bukan `bot.run()`) — update diproses satu per satu, menjaga RAM peak tetap rendah di mesin 512MB.
- **Memory user**: `/memory/USER.md` (persona user) dan `/memory/SOUL.md` (persona AI) di-inject ke system prompt di setiap request. `/memory/MEMORY.md` hanya tempat simpan info percakapan — TIDAK di-inject.
- **Safe reply/edit** (`bot.ts:36-78`): Error parsing Markdown ditangkap dan retry tanpa `parse_mode`.
- **E2B sandbox**: create → tulis kode ke VFS → `e2b_run_code` membaca dari VFS. Satu sandbox per chat, kill setelah 5 menit idle.
- **SoundCloud / web search** via `puruboy-api.vercel.app`.
- **Math**: `Function()` constructor eval.

## Konvensi
- Semua import source menggunakan ekstensi `.js` (ESM).
- `"type": "module"` di package.json.
- Instruksi dan respons agent menggunakan Bahasa Indonesia; jawaban harus singkat.
- `src/tools.ts` mengekspor type union `ToolNames` — update saat menambah tools.

## Git & Tagging
- Setiap commit WAJIB diikuti dengan pembuatan **annotated tag** (`git tag -a`).
- Format tag: `v<major>.<minor>.<patch>` — increment sesuai semver (patch untuk fix, minor untuk fitur, major untuk breaking change).
- Pesan tag harus berisi **penjelasan detail** tentang perubahan yang dilakukan (bukan cuma judul commit).
- Setelah commit dan tag dibuat, push semuanya: `git push origin main --follow-tags`.
