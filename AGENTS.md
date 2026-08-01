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
  - `MEMORY_UPDATE_EVERY` — Interval pesan user untuk auto-update MEMORY.md (default: `3`)
  - `MEMORY_MAX_CHARS` — Cap konten MEMORY.md saat di-inject ke system prompt (default: `8000`)

## Arsitektur
- `src/index.ts` — entrypoint, memulai health server lalu bot dalam conflict-retry loop (exit setelah 5x conflict berturut-turut)
- `src/bot.ts` — Setup grammY Bot, commands (`/start`, `/menu`, `/clear`, `/token`, `/info`, `/reset`, `/ai`, `/skills`), message handlers
- `src/agent.ts` — `ToolLoopAgent` (Vercel AI SDK) dengan 22 tools; `toolChoice` default `'auto'` (model bebas panggil tool), `stopWhen` = `hasToolCall('finish')` + `isStepCount(maxLoop)`; `temperature` dan `maxLoop` bisa dikonfigurasi
- `src/vfs.ts` — Virtual file system per-user disimpan di Firebase Realtime Database
- `src/history.ts` — Persistensi chat history (LRU cache + Firebase RTDB)
- `src/e2b.ts` — E2B sandbox (satu per chat, timeout 5 menit, auto-killed saat idle)
- `src/skills-loader.ts` — Parsing metadata skill, listing, loading dari VFS
- `src/skills-registry.ts` — Instalasi skill dari GitHub dan pencarian. Otomatis mendeteksi root direktori SKILL.md untuk menghindari nesting path yang salah.
- `src/memory.ts` — Auto-update `/memory/MEMORY.md` via pemanggilan `streamText` internal (model sama) ketika counter user mencapai kelipatan `MEMORY_UPDATE_EVERY`. Output MEMORY.md minimal: maksimal 10 poin bernomor (1-10), data lama boleh diganti. Memakai `streamText` (bukan `generateText`) karena proxy AI bisa membalas format SSE bahkan untuk request non-streaming.
- `src/server.ts` — HTTP health check di port 3000
- `src/instruction.ts` — System prompt agent dibangun dengan `ChatPromptTemplate` (`@langchain/core/prompts`). Variabel template: `{soul}`, `{user}`, `{memory}`, `{skills}`. Kurung kurawal literal di teks prompt WAJIB di-escape sebagai `{{...}}` (contoh: `/skills/{{name}}/SKILL.md`) agar tidak dianggap variabel — kalau tidak, muncul error `INVALID_PROMPT_INPUT: Missing value for input variable ...`

## Perilaku Penting
- **Retry**: API call retry hingga 4 kali dengan exponential backoff (1s→2s→4s→8s, cap 30s). Error 4xx selain 408/429 (mis. invalid key) TIDAK di-retry — langsung berhenti. Web search retry 5 kali (1s→16s).
- **Timeout agent** (`agent.ts`): `ToolLoopAgent` dikonfigurasi `timeout: { totalMs: 300000, stepMs: 120000, toolMs: 120000 }` — SDK abort beneran (bukan `Promise.race` yang bocor). `withTimeout` tetap ada sebagai jaring pengaman.
- **Batas data** (`agent.ts`, `bot.ts`): `crawl` baca body max 1.5MB & hasil max 20k char; `read_file` max 30k char; upload dokumen max 10MB; konten tiap message history di-truncate max 8k char (`sanitizeHistoryMessages`) sebelum disimpan. Bertujuan menjaga RAM peak tetap rendah di mesin 512MB.
- **Persistensi history** (`history.ts`): Chat history disimpan di Firebase RTDB dengan LRU cache (maks 500 user, TTL 10 menit). Cache miss load dari Firebase; write bersifat synchronous (await). Array kosong tidak di-cache. `getHistory` return **copy** array (cache tidak termutasi oleh pemanggil).
- **Batas history** (`bot.ts`): Sebelum dikirim ke AI, history hanya di-prune (hapus reasoning lama & tool-call lama via `pruneMessages`) lalu dibatasi maksimal 5 pesan user (`capUserTurns`). Turn user terlama dihapus beserta jawaban assistant & tool-call-nya, urutan sisanya dipertahankan.
- **Conflict loop** (`index.ts`): conflict beruntun ≥5x (tanda ada instance lain dengan token sama) → `process.exit(1)` agar platform restart bersih, bukan spin selamanya.
- **Pemrosesan sekuensial**: Bot menggunakan `bot.start()` (bukan `bot.run()`) — update diproses satu per satu, menjaga RAM peak tetap rendah di mesin 512MB.
- **Memory user**: `/memory/USER.md` (persona user), `/memory/SOUL.md` (persona AI), dan `/memory/MEMORY.md` (konteks percakapan) di-inject ke system prompt di setiap request (MEMORY.md dipotong max `MEMORY_MAX_CHARS`). MEMORY.md **dikelola otomatis oleh sistem** — AI DILARANG membaca/menulisnya sendiri; `updateMemory` dipicu setiap `MEMORY_UPDATE_EVERY` pesan user (counter `history/{id}/meta`) SETELAH reply terkirim, error tidak menggagalkan alur chat.
- **Safe reply/edit** (`bot.ts:36-78`): Error parsing Markdown ditangkap dan retry tanpa `parse_mode`.
- **E2B sandbox**: create → tulis kode ke VFS → `e2b_run_code` membaca dari VFS. Satu sandbox per chat, kill setelah 5 menit idle.
- **SoundCloud / web search** via `puruboy-api.vercel.app`.
- **Math**: `Function()` constructor eval.

## Konvensi
- Semua import source menggunakan ekstensi `.js` (ESM).
- `"type": "module"` di package.json.
- System prompt (instruksi) agent berbahasa Inggris dan ringkas; respons ke user dalam Bahasa Indonesia dan singkat.
- `src/tools.ts` mengekspor type union `ToolNames` — update saat menambah tools.
- **Setiap perubahan codebase WAJIB diikuti update `AGENTS.md` dan `README.md`** agar informasi (arsitektur, perilaku, command, config) selalu relevan.

## Git & Tagging
- Setiap commit WAJIB diikuti dengan pembuatan **annotated tag** (`git tag -a`).
- Format tag: `v<major>.<minor>.<patch>` — increment sesuai semver (patch untuk fix, minor untuk fitur, major untuk breaking change).
- Pesan tag harus berisi **penjelasan detail** tentang perubahan yang dilakukan (bukan cuma judul commit).
- Setelah commit dan tag dibuat, push semuanya: `git push origin main --follow-tags`.
