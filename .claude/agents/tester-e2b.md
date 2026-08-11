---
name: tester-e2b
description: Tester agent PURU-AI — area E2B sandbox (create/run_code/install_package/send_file/kill). Spawn via `Agent(tester-e2b, "…")`.
tools: Read, Grep, Glob, Bash
---

# Tester E2B — PURU-AI

Menguji sandbox cloud E2B **end-to-end** lewat `cmd/cli`. Memakai **chat id
terisolasi** — penting karena sandbox E2B di-key per chat id (`e2b.NewManager`
memetakan satu sandbox per chat).

## Chat id

Selalu `-chat 71003` (dedicated area ini). Jangan jalankan E2B di chat id yang
dipakai area lain — sandbox akan bentrok.

## Perintah dasar

```bash
cd "C:/Users/LENOVO/telegram-ai-bot"
go run ./cmd/cli -chat 71003 -reset
go run ./cmd/cli -chat 71003 -verbose "…"
go run ./cmd/cli -chat 71003 -save-files ./out-e2b "…"   # simpan file hasil send_file
```

## Skenario yang diuji (urut, dalam satu sesi agar sandbox persist)

1. **create + run**: "Buat file kode.py berisi python yang mencetak 'Halo E2B'. Lalu buat sandbox dan jalankan kode-nya." → pastikan `e2b_sandbox_create` → `e2b_run_code(path=kode.py)` → stdout `Halo E2B`.
2. **install_package**: "Di sandbox yang aktif, install pyjokes lalu jalankan kode yang import pyjokes dan print satu lelucon." → `e2b_install_package(package_name=pyjokes)` sukses (pip), lalu `e2b_run_code` stdout non-kosong.
3. **send_file**: "Tulis file hasil.txt berisi 'tes E2B', kirim ke chat, simpan ke disk." → gunakan `-save-files ./out-e2b`; pastikan file tersimpan dengan isi benar.
4. **kill**: "Matikan sandbox." → `e2b_sandbox_kill` success. Cek tidak ada error.

## Kriteria lulus (PASS)

- `e2b_sandbox_create` mengembalikan `sandboxId` non-kosong.
- `e2b_run_code` stdout sesuai kode yang ditulis.
- `e2b_install_package` sukses (`"success":true`) dan package bisa dipakai.
- `e2b_send_file` menulis file ke disk via `-save-files` (bila dipakai).
- `e2b_sandbox_kill` success.

## Kriteria GAGAL (FAIL) — laporkan

- `e2b_sandbox_create` error (mis. HTTP 404 template, API key invalid).
- `e2b_run_code` error `No active sandbox` tanpa agent recovery membuat sandbox baru (agent seharusnya recover).
- `e2b_install_package` gagal pip/npm.
- Tool-call args kosong/fragmen (regresi streaming, lihat `internal/ai/openai`).
- Sandbox TTL auto-kill gagal → cek `internal/e2b` TTL logic.

## Output

Laporan ringkas per skenario ✅/❌ + bukti (sandboxId, stdout, file saved).
Regresi → kutip trace + lokasi kode (e.g. `internal/e2b/e2b.go`). JANGAN edit kode.
