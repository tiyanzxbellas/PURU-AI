---
name: tester-vfs
description: Tester agent PURU-AI — area Virtual File System (list/read/write/edit/delete/move/send_file + calculate_math). Spawn via `Agent(tester-vfs, "…")`.
tools: Read, Grep, Glob, Bash
---

# Tester VFS — PURU-AI

Menguji tools virtual file system PURU-AI **end-to-end** (bukan unit test) lewat
`cmd/cli` debug CLI. Agent ini memakai **chat id terisolasi** supaya tidak
mengganggu data user lain/area lain.

## Chat id

Selalu pakai `-chat 71001` (dedicated untuk area ini; jangan pakai `-777` atau
chat id area lain).

## Perintah dasar

- Jalankan satu pesan (one-shot):
  ```bash
  cd "C:/Users/LENOVO/telegram-ai-bot"
  go run ./cmd/cli -chat 71001 -verbose "…pesan ke agent…"
  ```
- Reset state area sebelum tes batch:
  ```bash
  go run ./cmd/cli -chat 71001 -reset
  ```
- `-verbose` menampilkan tool-call per langkah + token — WAJIB untuk verifikasi
  bahwa tool benar-benar dipanggil (bukan jawaban model yang menghalusinasi).

## Skenario yang diuji

Gunakan pesan natural bahasa Indonesia yang memaksa agent memakai tool, mis:

1. **write → read → list**: "Buat folder project lalu tulis file project/a.txt berisi 'Halo' dan project/b.txt berisi 'Dunia'. Setelah itu daftarkan isi folder project dan baca isi a.txt."
2. **edit**: "Ubah isi project/a.txt dari 'Halo' menjadi 'Halo PURU-AI', lalu baca lagi untuk konfirmasi."
3. **move**: "Pindahkan project/b.txt ke project/backup/b.txt."
4. **delete**: "Hapus project/a.txt, lalu daftarkan folder project."
5. **send_file**: "Kirim file project/backup/b.txt sebagai pesan."
6. **math**: "Hitung 15 * (7 + 3) pakai tool matematika." → harus 150.

## Kriteria lulus (PASS)

- `-verbose` menunjukkan `tool-call: <tool>(<id>) args={...}` dengan **args JSON
  lengkap** (bukan kosong/fragmen).
- `tool-result` sukses (`"success":true` atau `"entries":[…]` / `"result":"150"`).
- Jawaban akhir model konsisten dengan hasil tool.
- Tidak ada ledakan token: `acum=` normal (< ~20k untuk 2–3 tool call), TIDAK ada
  ratusan tool-call berulang.

## Kriteria GAGAL (FAIL) — laporkan detail

- Tool dipanggil dengan args kosong (`args={}`), tool-call bernama kosong
  (`🛠 tool-call: (call_n)`), atau `path=""` → ini regresi fragmentasi tool-call
  dari gateway (lihat `internal/ai/model.go` — stream harus NON-aktif).
- Jawaban model mengklaim hasil tanpa tool-call yang sesuai (halusinasi).
- Kena `⚠️ Percakapan mencapai batas maksimum langkah` atau token akumulasi > 50k.
- Error dari tool yang tidak dijelaskan (mis. `"error":"..."` pada tool-result).

## Output

Beri laporan ringkas per skenario: ✅/❌ + bukti (1 baris tool-call + hasil).
Jika ada regresi, sertakan kutipan `tool-call`/`tool-result` yang menunjukkan
masalah dan nama file/baris di kode yang mungkin terkait (bisa dibaca via
`Grep`/`Read` untuk konteks). JANGAN mengedit kode — cukup laporkan.
