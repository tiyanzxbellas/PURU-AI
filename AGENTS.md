# AGENTS.md

## Mulai Cepat
- `go run .` — jalankan bot lokal (load `.env` via godotenv)
- `go build -o dist/puru-ai .` — kompilasi binary
- `go run ./cmd/cli "pesan..."` — chat langsung dengan agent (debug, tanpa Telegram)
- `cli.bat "pesan..."` / `cli.sh "pesan..."` — alias cepat ke `go run ./cmd/cli`
- `go vet ./...` — static analysis
- `go test ./...` — unit test (jsrun, messages, prompt, settings)
- `gofmt -l .` — cek format

## Testing Teamwork (Claude Code)
- Definisi agent tester per-area di `.claude/agents/tester-{vfs,web,e2b,skills,memory}.md`
  — tiap agent menguji tools end-to-end lewat `go run ./cmd/cli -chat <id> -verbose "…"`
  memakai **chat id terisolasi** (VFS/MEMORY/sandbox E2B keyed by chat id di Firebase;
  bentrok antar-area = hasil palsu). Agent tester hanya melaporkan, TIDAK mengubah kode.
- Workflow orchestrator `.claude/workflows/test-puru-ai.md` mem-fan-out kelima tester
  paralel lalu mensintesis laporan. Jalankan via `Workflow(test-puru-ai)`.
- Alokasi chat id: VFS `71001`, Web `71002`, E2B `71003`, Skills `71004`, Memory `71005`
  (negatif `-777` hanya untuk debug ad-hoc, bukan untuk tester paralel).

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
  - `E2B_TEMPLATE` — template sandbox E2B untuk eksekusi kode (default: `code-interpreter-v1`; template `default` lama sudah dihapus dari platform E2B → HTTP 404). Dibaca via `getEnv` di `internal/e2b`, bukan `config.go`.
  - `GITHUB_TOKEN` — token GitHub untuk `/skills search`. **Wajib** untuk code search (`filename:SKILL.md`); tanpa token API balas HTTP 401 → `/skills search` menampilkan pesan "GITHUB_TOKEN belum diset".
  - `CLAWHUB_APIKEY` — token registry skill ClawHub (opsional). Registry clawhub aktif bila diisi; base URL memakai default (`https://clawhub.ai`).

## Arsitektur (Go, module `github.com/purujawa06-bot/PURU-AI`)
- `main.go` — entrypoint: config → health server → `getMe` → `deleteWebhook(drop_pending)` → konflict-retry loop (exit setelah 5 conflict berurut).
- `cmd/cli` — debug CLI tanpa Telegram: wiring service sama persis `main.go` (config → firebase → vfs/history/skills/e2b → model → memory), lalu `ai.Agent.ProcessMessage` dengan pipeline identik `processMessage` (prune→cap→proses→persist→memory). One-shot (`cli.bat "pesan"`) atau REPL; chat id debug default `-777` (terpisah dari user publik), flag `-reset`/`-verbose`/`-save-files`/`-no-memory`. Dipakai untuk debugging cepat tanpa Telegram.
- `internal/config` — config loader.
- `internal/telegram` — klien Telegram Bot API **ditulis manual** (poling `getUpdates` synchronous, `sendMessage`, `editMessageText`, `sendDocument/Audio/Video`, `getFile`+download). Sebab utama ditulis sendiri: bot memproses update **sekali-sekali secara beruntun** (satu goroutine) seperti `bot.start()` di grammY, dan deteksi conflict (409) harus presisi.
- `internal/app` — handler bot: dispatcher command + busy-guard per-user (paralel antar-user), pipeline pesan (prune→cap→sanitize→token→memory), safe reply/edit/send, upload dokumen (maks 10MB).
- `internal/ai` — agent tool-loop & sesi dipegang **langchaingo** (`github.com/tmc/langchaingo`): model via **klien OpenAI-compatible sendiri** (`internal/ai/openai`, implementasi `llms.Model`), loop iterasi + scratchpad dikelola `agents.Executor`, sesi di-assemble `prompts.ChatPromptTemplate` (`system` → `chat_history` → `input` → `agent_scratchpad`; system memakai formatter non-templating agar literal `{{name}}` aman), custom `agents.Agent` hanya format prompt + parse output, 21 tools (adapter `tools.Tool` di atas runner lama) + prosesMessage (retry + normalisasi arguments). Tidak ada lagi tool `finish`; jawaban final = teks stop natural executor. **Tool-call id di-normalisasi** (`ra.toolID` → `call_<n>` bila provider mengosongkannya, mis. gateway Gemini) supaya pasangan tool call↔tool-result selalu 1:1 saat di-replay; history lama tanpa id ikut diperbaiki di `toChatHistory` (perbaikan gemini `400 ... function response parts`). **Model DI-stream** (`stream:true` via `noopStream`) supaya generasi panjang tidak kena timeout gateway 503; assembly tool-call SSE diperbaiki di `internal/ai/openai` — langchaingo v0.1.14 hanya menyambung fragment argumen bila delta punya `type` kosong, gateway yang men-tag tiap fragment (`type:"function"`) memecah args jadi tool call kosong (regresi dijaga `internal/ai/openai/openai_test.go` & `internal/ai/agent_test.go`).
- `internal/ai/openai` — klien OpenAI-compatible minimal (implementasi `llms.Model.GenerateContent`): SSE streaming benar, fallback non-SSE untuk proxy yang membalas JSON biasa walau `stream:true`, dan **assembly tool-call yang diperbaiki** (fragment argumen di-append ke tool call pada index yang sama, bukan jadi tool call baru — lihat `mergeToolCallDeltas`). Konsisten etos repo: klien Telegram & E2B juga ditulis manual.
- `internal/messages` — model pesan `ModelMessage` kompatibel dengan schema Vercel AI SDK (round-trip field tak dikenal dipertahankan) + port `pruneMessages`/`EnsureStartsWithUser`/`CapUserTurns`/`Sanitize`.
- `internal/firebase` — helper REST ing RTDB (GET/PUT/DELETE `.json` + base64url path) + `ListKeys` (shallow `?shallow=true`) untuk enumerasi key anak sebuah node — dipakai `VFS.DeleteDir` agar pembersihan tidak bergantung pada index (RTDB eventual consistency bisa membuat index stale).
- `internal/settings` — config API per-user (base URL/key/model) di RTDB `settings/{chatID}` (partial override; field kosong = inherit global) + cache TTL in-memory. `Effective(global, user)` menggabungkan override.
- `internal/vfs` — virtual file system per-user di RTDB. `ReadFile` memperlakukan respons RTDB literal `null` sebagai file **tidak ada** (return `false`); tanpa pengecekan ini, `json.Unmarshal("null", &string)` diam-diam menghasilkan file "ada tapi kosong" dan membuat install skill salah melaporkan "sudah terinstall". `DeleteFile` punya guard `null` yang sama — file yang tak ada dilaporkan `false` (tidak ada), bukan sukses palsu (regresi `delete_file` phantom-success yang ditemukan tester edge-case; dijaga `vfs_test.go`). `DeleteDir` menghapus sebuah direktori beserta seluruh isinya dengan **memindai store `content/` dan `index/` langsung** (via `firebase.ListKeys` shallow), bukan menelusuri index direktori — sehingga tetap bersih walau index rusak oleh race read-modify-write RTDB saat install (mis. entri `SKILL.md` hilang dari index `skills/pdf` tapi file content-nya masih ada; regresi `delete_skill` yang hanya menghapus `SKILL.md` meninggalkan scripts/`.skill-origin.json` yatim ditemukan tester end-to-end; dijaga `vfs_test.go` & `manifest_test.go`).
- `internal/history` — persistensi history (LRU+TTL + RTDB; hasil copy bukan mutasi cache).
- `internal/tokens` — wrapper tiktoken-go (`o200k_base`).
- `internal/skills` — parsing SKILL.md (frontmatter) + registry manager yang **mengimpor logika `pkg/skills` picoclaw** (`github.com/sipeed/picoclaw/pkg/skills`). Search memakai **GitHub code search** `GET /search/code?q=<query> filename:SKILL.md` (dedup per slug `owner/repo[/subdir]`, sort by score, clamp 20; 401/403 → hasil kosong + warning) + registry ClawHub via `RegistryManager.SearchAll` (fan-out concurrent, semaphore, merge by score). Install picoclaw menulis ke **temp dir disk** → hasil walk dipindah ke per-user VFS (`skills/<dir>/...`, file >2MB ditolak) lalu temp dir dihapus; metadata asal ditulis ke `skills/<dir>/.skill-origin.json`. Skill bawaan di-embed (`internal/skills/builtin/` via `go:embed`: weather, summarize, github, skill-creator, lisensi MIT picoclaw) untuk `/skills builtin`. Tool AI `install_skill` merutekan target ber-prefix `clawhub:` ke `InstallFromClawHub`, selain itu ke `InstallFromGitHub`. Tool AI `delete_skill` dan command `/skills delete` sama-sama memakai `Catalog.DeleteSkill` (hapus seluruh subtree via `vfs.DeleteDir`, bukan hanya `SKILL.md`).
- `internal/prompt` — system prompt `text/template`. Literal braces seperti `/skills/{{name}}/SKILL.md` WAJIB di-escape (`{{"{{"}}` / `{{"}}"}}`), analog larvae AGENTS lama.
- `internal/memory` — auto-update `/memory/MEMORY.md` via model langchaingo (`llms.Model.GenerateContent`).
- `internal/jsrun` — runtime goja untuk `crawl` (shim cheerio di atas goquery) + `calculate_math` (Math.*/alias).
- `internal/e2b` — client E2B murni HTTP (port wire `@e2b/code-interpreter`): create/kcreate `POST https://api.<domain>/sandboxes` (X-API-KEY), runCode `POST ...:49999/execute` (NDJSON), files `GET/POST ...:49983/files`. Template default `code-interpreter-v1` (dari env `E2B_TEMPLATE`).
- `internal/health` — HTTP health check.

## Perilaku Penting (dipertahankan dari generasi sebelumnya)
- **Retry**: API call retry hingga 4 kali dengan exponential backoff (1s→2s→4s→8s, cap 30s). Error 4xx selain 408/429 TIDAK di-retry (`isNonRetryableError`). Web search retry 5 kali (1s→16s, cap 30s).
- **Diagnostik error agent** (`agent.go`): bila jawaban gagal — `ToolsBuild` error, model tidak tersedia (`errNoModel`), `GenerateContent` error, `empty model response` (tanpa `choices`), atau final teks kosong — penyebabnya SELALU di-log dengan prefix `[ai]` (per-attempt, `finish_reason`, alasan akhir). Sebelumnya `lastErr` dibuang diam-diam (`_ =`) sehingga "API sukses tapi bot diam" tak terbaca. Kondisi pasti-gagal (`isNonRetryableError`/`errNoModel`) langsung `break retryLoop` tanpa bila perlu; hanya error transien & empty-text yang di-retry. User tetap mendapat fallback pendek `Maaf, saya tidak bisa merespons saat ini.`
- **User-Agent browser** (`main.go`): shared `http.Client` memakai transport yang menyuntikkan UA Chrome bila header kosong — supaya `crawl` tidak ditolak HTTP 403 oleh situs seperti Wikipedia yang memblokir `Go-http-client/1.1`. Header UA eksplisit (mis. e2b `puru-ai/1.0`) tetap menang.
- **Timeout agent** (`agent.go`): total 300s via `context.WithTimeout`; per-tool 120s (`toolTimeout`) di adapter `tools.Tool`; model dipanggil streaming (`GenerateContent` + `WithStreamingFunc(noopStream)`) — assembly tool-call SSE diperbaiki di `internal/ai/openai` (klien langchaingo v0.1.14 memecah fragment argumen ber-`type:"function"` jadi tool call kosong). **Context tool WAJIB tetap hidup** — langchaingo `agents.Executor` tidak membatalkan ctx di antara langkah tool; regresi lama (`cancel()` prematur setelah Chat membuat semua tool HTTP gagal `context canceled`) dijaga oleh `internal/ai/agent_test.go`.
- **Batas data**: crawl baca body max 1.5MB & hasil max 20k char (tool), `read_file` max 30 ribu char; upload dokumen max 10MB; isi history di-truncate max 8k char/message (`messages.Sanitize`).
- **Persistensi history**: path `history/{chat}/messages|tokens|meta` di RTDB, LRU+TTL di dalam proses (hasil getHistory berupa copy; array kosong tidak di-cache). JSON schema = `ModelMessage` Vercel AI SDK v7 → data user lama tetap terbaca & round-trip field tidak hilang.
- **Batas history**: sebelum dikirim ke model, history di-prune (`reasoning: before-last-message`, `toolCalls: before-last-6-messages`, kosong dihapus) lalu di-cap maksimal 5 pesan user (`messages.CapUserTurns`).
- **Conflict loop** (`main.go`): conflict beruntun ≥5 → `os.Exit(1)` agar platform restart bersih.
- **Pemrosesan paralel per-user** (`internal/app/app.go`): `Handle` hanya dispatcher — setiap update text/document dijalankan di goroutine sendiri sehingga semua user diproses bersamaan tanpa antrian. User yang sama dikunci via `busy sync.Map` (`tryAcquire`/`release`): update kedua saat request masih berjalan langsung dibalas `⏳ Masih ada yang lagi diproses, tunggu sebentar ya...` (tidak di-queue). **Busy guard hanya berlaku untuk pesan AI** (teks polos, `/ai`, dokumen) — command non-AI (`/menu`, `/token`, `/reset`, `/skills`, `/config`, dst.) SELALU langsung dieksekusi tanpa diblok busy. Command token dinormalisasi (`splitCommand`) dengan strip suffix `@bot` sehingga `/menu@nama_bot` di grup tetap dikenali; subcommand membaca argumen dari sisa token, bukan `msg.Text[len("/cmd"):]`. Konsistensi per-user (history/LRU/e2b) aman karena dua request user yang sama tidak pernah bersamaan; `main.go` hanya memajukan offset, error async hanya di-log.
- **Anti-halusinasi & protocol guard** (`internal/ai/agent.go`): system prompt melarang klaim tanpa tool call & filler; loop iterasi dikelola langchaingo `agents.Executor` (`MAX_LOOP` = `WithMaxIterations`). Jawaban final = teks stop natural executor (tidak ada lagi tool `finish`/scold-correction). Loop yang habis (`agents.ErrNotFinished`) → hint step-limit `lanjut`. Hanya turn yang menghasilkan teks non-kosong yang dipersist (`runOnce` menambahkan assistant-final ke `responseMessages`; output kosong tidak pernah ikut tersimpan — regresi polusi stub dijaga `internal/ai/agent_test.go`); `messages.SanitizeHistoryMessages` tetap membuang text part kosong/whitespace & pesan assistant kosong tanpa tool-call; `makeResult` fallback ke pesan error bila teks kosong. `arguments` tool call dinormalisasi (`toolArgsJSON`) sebelum replay ke provider agar provider ketat tidak menolak. Di `internal/prompt`: Rule 9 — bila pesan user ambigu/pendek/gibberish, AI dilarang menebak dengan tool dan wajib tanya klarifikasi langsung (dijaga assertion `prompt_test.go`).
- **Elusive memory**: auto-update `/memory/MEMORY.md` setelah `MEMORY_UPDATE_EVERY` pesan user (counter `history/{id}/meta`), error non-fatal. MEMORY.md dikelola sistem, AI dilarang baca/tulis sendiri. Konten di-update agar **minimal**: maks 5 poin penting bernomor + baris `Topik sedang dibahas:`; info usang/irrelevan dihapus (`memoryPrompt`, cap output 1500 char).
- **Safe reply/edit**: fallback parse-mode Markdown bila Telegram menolak entitas parse; respon >4096 char dikirim sebagai file. Semua teks keluar (sendMessage/editMessageText/caption) di-sanitasi jadi valid UTF-8 via `sanitizeText` (`strings.ToValidUTF8`, U+FFFD) di `internal/telegram` agar tidak kena `400 text must be encoded in UTF-8` bila output model/scraped berisi byte invalid.
- **Jawaban final pesan baru**: jawaban AI dikirim sebagai **pesan baru** (reply ke pesan user) via `safeSend`, bukan edit — placeholder "🤔 sedang berpikir..."/pesan "Tersimpan di /path..." dihapus via `deleteMessage` setelah jawaban terkirim (`internal/app/generate.go`). Command skills (search/install/migrate) tetap pakai `sendThinking` + `safeEdit`.
- **Per-user API config**: `/config` (api/model/base/clear/test) dipakai user untuk memakai API sendiri; partial override global via `settings.Effective`. Resolver `ClientFor(ctx, chatID)` membuat model langchaingo `llms.Model` fresh per request (pakai http.Client global) ⇒ aman paralel. Memory update juga ikut API user. Path `settings/{chatID}` di RTDB terpisah dari `fs/` & `history/` sehingga `/reset chat` tidak menghapus config.
- **/reset terpisah**: `/reset` polos = help; `/reset config` hapus settings user; `/reset memory` hapus MEMORY.md + reset counter `meta.userTurns`; `/reset chat` = `DeleteHistory` + `vfs.DeleteAll` (perilaku /reset lama).
- **E2B**: satu sandbox per chat, TTL 5 menit idle, auto-kill; `runCode` hasil NDJSON `{type: stdout/stderr/result/error}`.
- **Math & crawl**: evaluasi lewat goja. `calculate_math` menyediakan alias Math (`sqrt`, `pow`, ...). Hasil non-finite (Infinity/NaN, mis. `10/0`) ditolak sebagai error "Ekspresi matematika tidak valid" — tidak dikembalikan ke model (regresi ditemukan tester edge-case; dijaga `jsrun_test.go`). `crawl` mengharapkan snippet cheerio JavaScript (shim compat: `text`, `html`, `attr`, `val`, `length`, `first`, `last`, `eq`, `find`, `parent`, `parents`, `children`, `filter`, `each`, `map`, `get`, `toArray`, passthrough `$(el)`); snippet boleh ekspresi atau statement `return {...};` (`normalizeCheerioCode` men-strip leading `return` + trailing `;` sebelum di-wrap evaluator, dijaga `jsrun_test.go`). URL crawl di-normalisasi dulu (`normalizeURL` di `internal/ai/tools.go`): url kosong/invalid/skema non-http(s)/host kosong mengembalikan error jelas (bukan `unsupported protocol scheme` mentah dari `http.Client`), url tanpa skema diberi prefix `https://`; dijaga `tools_test.go`.
- **Tool schema**: `required` di function schema hanya disertakan bila ada (dihilangkan saat kosong) — provider OpenAI-compatible tertentu menolak `"required": null`; dijaga `internal/ai/tools_test.go`.

## Konvensi
- Go toolchain 1.26+; format `gofmt`; tidak ada linter wajib selain `go vet`.
- Import internal memakai module path `github.com/purujawa06-bot/PURU-AI/internal/...`.
- Response ke user dalam Bahasa Indonesia & singkat; system prompt bahasa Inggris.
- **Asisten Claude Code**: respons ke user (pengembang) WAJIB dalam Bahasa Indonesia (dialog alami, bukan terjemahan kaku). Kode, komentar, dan dokumentasi repo tetap mengikuti gaya yang ada.
- **Setiap perubahan codebase WAJIB di-update `AGENTS.md` dan `README.md`.**

## Git & Tagging
- Setiap commit WAJIB diikuti annotated tag (`git tag -a`).
- Format tag `v<major>.<minor>.<patch>` — semver (patch fix, minor fitur, major breaking).
- Pesan tag harus berisi penjelasan detail perubahan.
- Sesudah commit & tag: `git push origin main --follow-tags`.