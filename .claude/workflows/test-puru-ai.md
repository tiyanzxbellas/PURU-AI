---
name: test-puru-ai
description: Orchestrasi testing lengkap agent PURU-AI — fan-out 5 tester paralel (VFS, Web, E2B, Skills, Memory) ke chat id terisolasi, lalu sintesis hasil. Jalankan via `Workflow(test-puru-ai)`.
---

# Workflow: Testing lengkap PURU-AI

## Tujuan

Menjalankan pengujian agent PURU-AI **end-to-end** secara paralel per area,
dengan isolasi data (tiap tester memakai chat id sendiri di Firebase RTDB),
lalu menyatukan hasil menjadi satu laporan ringkas.

## Struktur

```
┌──────────────────────────────────────────────────────────────┐
│ Workflow: test-puru-ai                                        │
│ 1. Reset semua chat id area (paralel)                        │
│ 2. Fan-out 5 tester (paralel, satu per chat id)              │
│    ├─ tester-vfs    (chat 71001)  VFS + math                │
│    ├─ tester-web    (chat 71002)  search/crawl/time          │
│    ├─ tester-e2b    (chat 71003)  sandbox E2B                │
│    ├─ tester-skills (chat 71004)  search/install/use/create  │
│    └─ tester-memory (chat 71005)  memory/history/anti-halus  │
│ 3. Sintesis hasil → laporan per area + ringkasan status      │
└──────────────────────────────────────────────────────────────┘
```

## Aturan eksekusi

- **Reset dulu**: setiap area di-reset sebelum diuji (`go run ./cmd/cli -chat <id> -reset`)
  agar state Firebase bersih dan hasil tidak terkontaminasi sesi sebelumnya.
- **Jalankan tester via definisi agent** (`.claude/agents/tester-<area>.md`),
  bukan menulis ulang logika di sini. Setiap tester punya skenario + PASS/FAIL
  + cara debug sendiri.
- **Paralel aman**: chat id per area TIDAK boleh diubah (E2B & MEMORY per-user
  di Firebase keyed by chat id; bentrok = hasil palsu).
- **E2B memakan waktu** (sandbox create/install/run) — beri tester-e2b jatah
  waktu lebih.
- **Jangan mengubah kode** selama testing; tester hanya melaporkan.

## Fan-out paralel

Spawarkan kelima tester sekaligus dalam satu langkah (semua independen, chat id
berbeda → tidak saling ganggu):

```js
// Tiap area: spawn tester, minta laporan terstruktur.
// Kembalikan { area, status: "PASS"|"FAIL", ringkasan, bukti, skenario }
```

Setiap tester mengembalikan teks final berupa laporan dengan format:

```
AREA: <nama>
STATUS: PASS|FAIL|WARN
SKENARIO:
- ✅ <nama skenario> — bukti singkat (tool-call/result)
- ❌ <nama skenario> — bukti + dugaan lokasi kode
RINGKASAN: <2-3 kalimat>
```

## Sintesis

Setelah semua tester selesai, gabungkan menjadi laporan akhir:

1. Tabel per area: `| Area | Status | Skenario lulus/gagal | Bukti kunci |`
2. Daftar regresi (jika ada) urut severity, dengan file/baris yang terduga.
3. Status keseluruhan:
   - **ALL PASS** — semua area hijau.
   - **FAIL (n)** — n area gagal; uraikan tiap kegagalan.
   - **WARN** — ada temuan non-blokir (mis. slug skill ambigu, GITHUB_TOKEN belum
     diset).

## Output akhir

Cetak laporan sintesis sebagai teks final (return value workflow). Jangan edit
kode. Jika ditemukan regresi yang jelas, rekomendasikan langkah perbaikan.
