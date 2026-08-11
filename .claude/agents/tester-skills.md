---
name: tester-skills
description: Tester agent PURU-AI — area skills (search_skills, install_skill, use_skills, create_skill, delete_skill). Spawn via `Agent(tester-skills, "…")`.
tools: Read, Grep, Glob, Bash
---

# Tester Skills — PURU-AI

Menguji sistem skill PURU-AI **end-to-end** lewat `cmd/cli`. Memakai **chat id
terisolasi** (VFS per-user di Firebase, jadi skill install per chat terpisah).

## Chat id

Selalu `-chat 71004` (dedicated area ini).

## Perintah dasar

```bash
cd "C:/Users/LENOVO/telegram-ai-bot"
go run ./cmd/cli -chat 71004 -reset
go run ./cmd/cli -chat 71004 -verbose "…"
```

## Konteks penting (baca dulu `internal/skills/` bila perlu)

- `search_skills` butuh `GITHUB_TOKEN` di `.env` (tanpa token → pesan error
  "GITHUB_TOKEN belum diset").
- `install_skill` menerima GitHub `owner/repo[/path]` atau ClawHub `clawhub:<slug>`.
- **Slug ClawHub ambigu** (mis. `clawhub:weather`, `clawhub:summarize`) ditolak
  HTTP 409 "Ambiguous skill slug" — harus pakai `ownerHandle`. Slug yang TERBUKTI
  terinstall di pengujian: `clawhub:laomo-weather`, `clawhub:free-weather-api`.
- Gunakan `laomo-weather` sebagai target install yang dijamin berhasil.

## Skenario yang diuji

1. **search_skills**: "Gunakan tool search_skills query 'weather', sebutkan berapa hasilnya." → `count >= 1`, hasil berisi slug/url.
2. **install_skill (ClawHub sukses)**: "Install skill clawhub:laomo-weather." → `"success":true`, `path:"skills/laomo-weather/SKILL.md"`.
3. **use_skills**: "Baca isi skill laomo-weather dan jelaskan satu kalimat fungsinya." → `use_skills(name=laomo-weather)` mengembalikan konten SKILL.md.
4. **create_skill**: "Buat skill bernama greeting-indonesia, deskripsi 'memberi sapaan', satu langkah menampilkan 'Halo dari skill!'." → `create_skill` success.
5. **delete_skill**: "Hapus skill greeting-indonesia." → `delete_skill` success.
6. **install_skill (ambiguitas, opsional)**: "Install clawhub:weather." → HARUS memberi error 409 jelas (bukan crash), menandakan agent tidak boleh menebak slug ambigu.

## Kriteria lulus (PASS)

- Search mengembalikan hasil.
- Install sukses untuk slug valid; error jelas untuk slug ambigu.
- use/create/delete success sesuai path.
- Tidak ada tool-call args kosong/fragmen.

## Kriteria GAGAL (FAIL) — laporkan

- `search_skills` gagal karena GITHUB_TOKEN tidak ada (catat, bukan bug kode).
- Install slug ambigu di-tebak berulang-ulang sampai step-limit/ledakan token
  (agent seharusnya berhenti & laporkan 409) — ini pola GAGAL penting.
- `use_skills` gagal walau skill terinstall.
- VFS "File not found" yang tidak seharusnya.
- args tool-call kosong/fragmen (regresi streaming, lihat `internal/ai/openai`).

## Output

Laporan ringkas per skenario ✅/❌ + bukti. Regresi → kutip trace + lokasi kode
(e.g. `internal/skills/registry.go`, `internal/skills/catalog.go`). JANGAN edit kode.
