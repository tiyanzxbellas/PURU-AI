package ai

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
	"time"

	"github.com/purujawa06-bot/PURU-AI/internal/jsrun"
	"github.com/purujawa06-bot/PURU-AI/internal/skills"
)

const (
	maxReadFileChars  = 30_000
	maxCrawlBytes     = 1_500_000
	maxCrawlResultLen = 20_000
	maxSearchRetries  = 5
	searchTimeout     = 20 * time.Second
	crawlTimeout      = 15 * time.Second
)

type toolEnv struct {
	agent *Agent
	opts  *ProcessOptions
}

func BuildTools(a *Agent, opts *ProcessOptions) map[string]*Tool {
	env := &toolEnv{agent: a, opts: opts}
	names := []string{
		"list_directory", "read_file", "write_file", "edit_file", "delete_file",
		"move_file", "send_file", "search_web", "crawl", "get_current_time",
		"calculate_math", "e2b_sandbox_create", "e2b_run_code", "e2b_install_package",
		"e2b_send_file", "e2b_sandbox_kill", "create_skill", "use_skills",
		"delete_skill", "search_skills", "install_skill",
	}
	m := make(map[string]*Tool, len(names))
	for _, n := range names {
		m[n] = &Tool{
			Name:        n,
			Description: toolDescriptions[n],
			Parameters:  toolSchemas[n],
			Run: func(ctx context.Context, args map[string]any) (any, error) {
				return toolRunners[n](ctx, env, args)
			},
		}
	}
	return m
}

func argStr(a map[string]any, k string) string {
	s, _ := a[k].(string)
	return s
}

func argOpt(a map[string]any, k string) (string, bool) {
	s, ok := a[k].(string)
	return s, ok
}

func normalizeURL(raw string) (string, error) {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return "", fmt.Errorf("parameter url kosong — berikan URL lengkap yang ingin di-crawl")
	}
	u, err := url.Parse(raw)
	if err != nil {
		return "", fmt.Errorf("URL tidak valid: %v", err)
	}
	if u.Scheme == "" {
		raw = "https://" + raw
		u, err = url.Parse(raw)
		if err != nil {
			return "", fmt.Errorf("URL tidak valid: %v", err)
		}
	}
	if u.Scheme != "http" && u.Scheme != "https" {
		return "", fmt.Errorf("skema URL tidak didukung: %q (hanya http/https)", u.Scheme)
	}
	if u.Host == "" {
		return "", fmt.Errorf("URL tidak valid: host kosong")
	}
	return raw, nil
}

// ---------------------------------------------------------------------------
// Tool implementations
// ---------------------------------------------------------------------------

var toolRunners = map[string]func(ctx context.Context, e *toolEnv, a map[string]any) (any, error){
	"list_directory": func(ctx context.Context, e *toolEnv, a map[string]any) (any, error) {
		entries := e.agent.VFS.ListDirectory(ctx, e.opts.ChatID, argStr(a, "path"))
		if len(entries) == 0 {
			return map[string]any{"entries": []any{}, "message": "Directory is empty or does not exist"}, nil
		}
		return map[string]any{"entries": entries}, nil
	},

	"read_file": func(ctx context.Context, e *toolEnv, a map[string]any) (any, error) {
		path := argStr(a, "path")
		content, ok := e.agent.VFS.ReadFile(ctx, e.opts.ChatID, path)
		if !ok {
			return map[string]any{"error": "File not found", "content": nil}, nil
		}
		if len(content) > maxReadFileChars {
			return map[string]any{
				"content":   content[:maxReadFileChars],
				"path":      path,
				"truncated": true,
				"note":      fmt.Sprintf("File lebih dari %d karakter, hanya sebagian yang ditampilkan.", maxReadFileChars),
			}, nil
		}
		return map[string]any{"content": content, "path": path}, nil
	},

	"write_file": func(ctx context.Context, e *toolEnv, a map[string]any) (any, error) {
		path, content := argStr(a, "path"), argStr(a, "content")
		if err := e.agent.VFS.WriteFile(ctx, e.opts.ChatID, path, content); err != nil {
			return map[string]any{"success": false, "error": err.Error()}, nil
		}
		return map[string]any{"success": true, "path": path}, nil
	},

	"edit_file": func(ctx context.Context, e *toolEnv, a map[string]any) (any, error) {
		ok, errMsg := e.agent.VFS.EditFile(ctx, e.opts.ChatID, argStr(a, "path"), argStr(a, "old_string"), argStr(a, "new_string"))
		if !ok {
			return map[string]any{"success": false, "error": errMsg}, nil
		}
		return map[string]any{"success": true, "path": argStr(a, "path")}, nil
	},

	"delete_file": func(ctx context.Context, e *toolEnv, a map[string]any) (any, error) {
		ok, err := e.agent.VFS.DeleteFile(ctx, e.opts.ChatID, argStr(a, "path"))
		if err != nil {
			return map[string]any{"success": false, "error": err.Error()}, nil
		}
		if !ok {
			return map[string]any{"success": false, "error": "File not found"}, nil
		}
		return map[string]any{"success": true}, nil
	},

	"move_file": func(ctx context.Context, e *toolEnv, a map[string]any) (any, error) {
		ok, errMsg := e.agent.VFS.MoveFile(ctx, e.opts.ChatID, argStr(a, "source"), argStr(a, "destination"))
		if !ok {
			return map[string]any{"success": false, "error": errMsg}, nil
		}
		return map[string]any{"success": true}, nil
	},

	"send_file": func(ctx context.Context, e *toolEnv, a map[string]any) (any, error) {
		path := argStr(a, "path")
		content, ok := e.agent.VFS.ReadFile(ctx, e.opts.ChatID, path)
		if !ok {
			return map[string]any{"success": false, "error": "File not found in VFS"}, nil
		}
		if e.opts.SendFile == nil {
			return map[string]any{"success": false, "error": "Cannot send file to chat"}, nil
		}
		caption, _ := argOpt(a, "caption")
		filename := lastPathSegment(path, "file.txt")
		if err := e.opts.SendFile(content, filename, caption); err != nil {
			return map[string]any{"success": false, "error": err.Error()}, nil
		}
		return map[string]any{"success": true, "message": "File berhasil dikirim ke Telegram"}, nil
	},

	"search_web": func(ctx context.Context, e *toolEnv, a map[string]any) (any, error) {
		q := argStr(a, "q")
		var lastErr string
		for attempt := 1; attempt <= maxSearchRetries; attempt++ {
			reqURL := "https://puruboy-api.vercel.app/api/search/yahoo?q=" + url.QueryEscape(q)
			req, err := http.NewRequestWithContext(ctx, http.MethodGet, reqURL, nil)
			if err != nil {
				lastErr = err.Error()
			} else {
				sctx, cancel := context.WithTimeout(ctx, searchTimeout)
				resp, err := e.agent.HTTP.Do(req.WithContext(sctx))
				cancel()
				if err != nil {
					lastErr = err.Error()
				} else {
					body, _ := io.ReadAll(io.LimitReader(resp.Body, 5<<20))
					resp.Body.Close()
					if resp.StatusCode >= 400 {
						lastErr = fmt.Sprintf("HTTP %d", resp.StatusCode)
					} else {
						var data struct {
							Result []any `json:"result"`
						}
						json.Unmarshal(body, &data)
						results := data.Result
						if results == nil {
							results = []any{}
						}
						return map[string]any{"query": q, "results": results}, nil
					}
				}
			}
			if attempt < maxSearchRetries {
				backoff := time.Duration(1000<<uint(attempt-1)) * time.Millisecond
				if backoff > 30*time.Second {
					backoff = 30 * time.Second
				}
				time.Sleep(backoff)
			}
		}
		return map[string]any{
			"query":   q,
			"error":   fmt.Sprintf("Search failed after %d attempts: %s", maxSearchRetries, lastErr),
			"results": []any{},
		}, nil
	},

	"crawl": func(ctx context.Context, e *toolEnv, a map[string]any) (any, error) {
		raw := argStr(a, "url")
		target, uerr := normalizeURL(raw)
		if uerr != nil {
			return map[string]any{"error": "Failed to crawl: " + uerr.Error(), "url": raw}, nil
		}
		req, err := http.NewRequestWithContext(ctx, http.MethodGet, target, nil)
		if err != nil {
			return map[string]any{"error": "Failed to crawl: " + err.Error(), "url": target}, nil
		}
		cctx, cancel := context.WithTimeout(ctx, crawlTimeout)
		defer cancel()
		resp, err := e.agent.HTTP.Do(req.WithContext(cctx))
		if err != nil {
			return map[string]any{"error": "Failed to crawl: " + err.Error(), "url": target}, nil
		}
		defer resp.Body.Close()
		if resp.StatusCode >= 400 {
			return map[string]any{"error": fmt.Sprintf("HTTP %d", resp.StatusCode), "url": target}, nil
		}
		html, _ := io.ReadAll(io.LimitReader(resp.Body, maxCrawlBytes))

		result, consoleOut, serr := jsrun.RunCheerio(string(html), argStr(a, "code"))
		if serr != nil {
			return map[string]any{
				"error":       "Syntax error dalam kode cheerio",
				"syntaxError": serr.Error(),
				"url":         target,
			}, nil
		}
		out := map[string]any{"url": target, "result": truncateStr(result, maxCrawlResultLen)}
		if consoleOut != "" {
			out["consoleOutput"] = truncateStr(consoleOut, 2000)
		}
		return out, nil
	},

	"get_current_time": func(ctx context.Context, e *toolEnv, a map[string]any) (any, error) {
		zone := argStr(a, "zone")
		loc, err := time.LoadLocation(zone)
		if err != nil {
			return map[string]any{"error": "Zona waktu tidak valid: " + err.Error(), "timezone": zone}, nil
		}
		return map[string]any{"dateTime": formatIndonesianTime(time.Now().In(loc)), "timezone": zone}, nil
	},

	"calculate_math": func(ctx context.Context, e *toolEnv, a map[string]any) (any, error) {
		expr := argStr(a, "expression")
		res, err := jsrun.EvalMath(expr)
		if err != nil {
			return map[string]any{"expression": expr, "error": "Ekspresi matematika tidak valid"}, nil
		}
		return map[string]any{"expression": expr, "result": res}, nil
	},

	"e2b_sandbox_create": func(ctx context.Context, e *toolEnv, a map[string]any) (any, error) {
		id, err := e.agent.E2B.CreateSandbox(ctx, e.opts.ChatID)
		if err != nil {
			return map[string]any{"error": err.Error()}, nil
		}
		return map[string]any{"sandboxId": id}, nil
	},

	"e2b_run_code": func(ctx context.Context, e *toolEnv, a map[string]any) (any, error) {
		code, ok := e.agent.VFS.ReadFile(ctx, e.opts.ChatID, argStr(a, "path"))
		if !ok {
			return map[string]any{"error": "File tidak ditemukan di VFS", "path": argStr(a, "path")}, nil
		}
		language := "python"
		if l, ok := argOpt(a, "language"); ok && l != "" {
			language = l
		}
		res, rerr := e.agent.E2B.RunCode(ctx, e.opts.ChatID, code, language)
		if rerr != nil {
			return map[string]any{
				"text":  "",
				"logs":  map[string]any{"stdout": []string{}, "stderr": []string{}},
				"error": rerr.Error(),
			}, nil
		}
		out := map[string]any{"text": res.Text}
		if len(res.Stdout) > 0 || len(res.Stderr) > 0 {
			out["logs"] = map[string]any{"stdout": res.Stdout, "stderr": res.Stderr}
		}
		if res.Error() != "" {
			out["error"] = res.Error()
		}
		return out, nil
	},

	"e2b_install_package": func(ctx context.Context, e *toolEnv, a map[string]any) (any, error) {
		pkg := argStr(a, "package_name")
		manager := "pip"
		if m, ok := argOpt(a, "manager"); ok && m != "" {
			manager = m
		}
		res := e.agent.E2B.InstallPackage(ctx, e.opts.ChatID, pkg, manager)
		out := map[string]any{"success": res.Error() == "", "output": strings.Join(res.Stdout, "\n")}
		if res.Error() != "" {
			out["error"] = res.Error()
		}
		return out, nil
	},

	"e2b_send_file": func(ctx context.Context, e *toolEnv, a map[string]any) (any, error) {
		data, err := e.agent.E2B.ReadFile(ctx, e.opts.ChatID, argStr(a, "path"))
		if err != nil {
			return map[string]any{"success": false, "error": err.Error()}, nil
		}
		if len(data) == 0 {
			return map[string]any{"success": false, "error": "File kosong atau tidak ditemukan"}, nil
		}
		if e.opts.SendBuffer == nil {
			return map[string]any{"success": false, "error": "Tidak dapat mengirim file ke chat"}, nil
		}
		caption, _ := argOpt(a, "caption")
		filename := lastPathSegment(argStr(a, "path"), "sandbox_file")
		if err := e.opts.SendBuffer(data, filename, caption); err != nil {
			return map[string]any{"success": false, "error": err.Error()}, nil
		}
		return map[string]any{"success": true, "message": "File berhasil dikirim ke Telegram"}, nil
	},

	"e2b_sandbox_kill": func(ctx context.Context, e *toolEnv, a map[string]any) (any, error) {
		if e.agent.E2B.KillSandbox(e.opts.ChatID) {
			return map[string]any{"success": true, "message": "Sandbox terminated successfully."}, nil
		}
		return map[string]any{"success": false, "message": "No active sandbox to kill."}, nil
	},

	"create_skill": func(ctx context.Context, e *toolEnv, a map[string]any) (any, error) {
		name, desc := argStr(a, "name"), argStr(a, "description")
		stepsRaw, _ := a["steps"].([]any)
		var body strings.Builder
		body.WriteString("## Steps\n\n")
		for i, stepRaw := range stepsRaw {
			step, ok := stepRaw.(map[string]any)
			if !ok {
				continue
			}
			if i > 0 {
				body.WriteString("\n\n")
			}
			fmt.Fprintf(&body, "### Langkah %d: %s\n%s", i+1, argStr(step, "title"), argStr(step, "instruction"))
		}
		res := e.agent.Registry.InstallFromContent(ctx, e.opts.ChatID, name, desc, body.String())
		return map[string]any{"success": res.Success, "error": res.Error, "name": res.Name, "path": res.Path}, nil
	},

	"use_skills": func(ctx context.Context, e *toolEnv, a map[string]any) (any, error) {
		name := argStr(a, "name")
		content, ok := e.agent.Catalog.LoadSkill(ctx, e.opts.ChatID, name)
		if !ok {
			return map[string]any{"error": "Skill tidak ditemukan", "content": nil}, nil
		}
		return map[string]any{"name": name, "content": content}, nil
	},

	"delete_skill": func(ctx context.Context, e *toolEnv, a map[string]any) (any, error) {
		name := argStr(a, "name")
		ok, err := e.agent.VFS.DeleteFile(ctx, e.opts.ChatID, "skills/"+name+"/SKILL.md")
		if err != nil {
			return map[string]any{"success": false, "error": err.Error()}, nil
		}
		if !ok {
			return map[string]any{"success": false, "error": "Skill tidak ditemukan"}, nil
		}
		return map[string]any{"success": true}, nil
	},

	"search_skills": func(ctx context.Context, e *toolEnv, a map[string]any) (any, error) {
		query := argStr(a, "query")
		results, err := e.agent.Registry.SearchSkills(ctx, query)
		if err != nil {
			return map[string]any{"query": query, "error": err.Error(), "results": []any{}, "count": 0}, nil
		}
		return map[string]any{"query": query, "results": results, "count": len(results)}, nil
	},

	"install_skill": func(ctx context.Context, e *toolEnv, a map[string]any) (any, error) {
		target := argStr(a, "url")
		var res skills.InstallResult
		if strings.HasPrefix(target, "clawhub:") {
			res = e.agent.Registry.InstallFromClawHub(ctx, e.opts.ChatID, strings.TrimPrefix(target, "clawhub:"))
		} else {
			res = e.agent.Registry.InstallFromGitHub(ctx, e.opts.ChatID, target)
		}
		return map[string]any{"success": res.Success, "error": res.Error, "name": res.Name, "path": res.Path, "warning": res.Warning}, nil
	},
}

func lastPathSegment(path, fallback string) string {
	if i := strings.LastIndex(path, "/"); i >= 0 {
		path = path[i+1:]
	}
	if strings.TrimSpace(path) == "" {
		return fallback
	}
	return path
}

func truncateStr(s string, n int) string {
	if len(s) > n {
		return s[:n]
	}
	return s
}

func formatIndonesianTime(t time.Time) string {
	idDays := [...]string{"Minggu", "Senin", "Selasa", "Rabu", "Kamis", "Jumat", "Sabtu"}
	idMonths := [...]string{"", "Januari", "Februari", "Maret", "April", "Mei", "Juni", "Juli", "Agustus", "September", "Oktober", "November", "Desember"}
	return fmt.Sprintf("%s, %02d %s %d pukul %02d.%02d.%02d %s",
		idDays[t.Weekday()], t.Day(), idMonths[t.Month()], t.Year(),
		t.Hour(), t.Minute(), t.Second(), t.Format("MST"))
}

// ---------------------------------------------------------------------------
// Schemas / descriptions
// ---------------------------------------------------------------------------

var toolDescriptions = map[string]string{
	"list_directory":      "Membaca dan menampilkan daftar file serta folder di dalam direktori yang ditentukan di virtual file system.",
	"read_file":           "Membaca isi file teks dari virtual file system berdasarkan path yang ditentukan.",
	"write_file":          "Membuat file baru atau menulis ulang isi file teks dengan konten yang diberikan di virtual file system.",
	"edit_file":           "Mengubah bagian teks tertentu di dalam file dengan teks baru (mekanisme find and replace) di virtual file system.",
	"delete_file":         "Menghapus file secara permanen dari virtual file system.",
	"move_file":           "Memindahkan atau mengganti nama file di virtual file system dari satu lokasi ke lokasi lain.",
	"send_file":           "Membaca file dari virtual file system dan mengirimkannya langsung ke chat Telegram pengguna.",
	"search_web":          "Mencari informasi di web menggunakan Yahoo Search. Gunakan untuk mencari berita, artikel, atau informasi terkini.",
	"crawl":               `Mengunjungi URL website dan menjalankan kode cheerio untuk mengekstrak data. Tulis kode cheerio sebagai ekspresi menggunakan $ sebagai selector, contoh: $("h1").text() (boleh juga pakai return, mis. return $("h1").text();).`,
	"get_current_time":    "Mendapatkan informasi tanggal dan waktu saat ini berdasarkan zona waktu tertentu.",
	"calculate_math":      "Mengevaluasi ekspresi matematika untuk menghindari kesalahan hitung manual.",
	"e2b_sandbox_create":  "Membuat instans sandbox cloud E2B baru yang terisolasi dengan akses Linux dan internet. Setiap chat hanya bisa memiliki satu sandbox aktif.",
	"e2b_run_code":        "Membaca file kode dari virtual file system (VFS) lalu mengeksekusinya di dalam sandbox E2B. Output stdout/stderr akan dikembalikan.",
	"e2b_install_package": "Memasang package ke dalam sandbox E2B. Mendukung pip (Python) dan npm (Node.js).",
	"e2b_send_file":       "Mengambil file hasil pemrosesan dari sandbox E2B lalu mengirimkannya langsung ke chat Telegram pengguna.",
	"e2b_sandbox_kill":    "Menutup dan menghapus instans sandbox E2B secara permanen untuk membersihkan resource.",
	"create_skill":        "Membuat skill baru di /skills/ virtual file system dengan metadata dan workflow.",
	"use_skills":          "Membaca dan menggunakan skill dari /skills/ virtual file system.",
	"delete_skill":        "Menghapus skill dari /skills/ virtual file system.",
	"search_skills":       "Mencari skill dari GitHub (code search) dan ClawHub berdasarkan kata kunci.",
	"install_skill":       "Menginstall skill. url bisa berupa slug/URL GitHub (mis. openclaw/openclaw) atau target ClawHub dengan prefix clawhub: (mis. clawhub:weather).",
}

var toolSchemas = map[string]map[string]any{}

func init() {
	obj := func(required []string, props map[string]any) map[string]any {
		schema := map[string]any{"type": "object", "properties": props}
		if len(required) > 0 {
			schema["required"] = required
		}
		return schema
	}
	sp := func(desc string) map[string]any { return map[string]any{"type": "string", "description": desc} }

	toolSchemas["list_directory"] = obj([]string{"path"}, map[string]any{
		"path": sp(`Jalur direktori yang ingin dilihat (contoh: "project" atau "src/components"). Gunakan string kosong untuk root.`),
	})
	toolSchemas["read_file"] = obj([]string{"path"}, map[string]any{
		"path": sp(`Jalur lengkap ke file yang ingin dibaca (contoh: "project/src/index.js").`),
	})
	toolSchemas["write_file"] = obj([]string{"path", "content"}, map[string]any{
		"path":    sp(`Jalur file yang akan dibuat/ditulis (contoh: "project/src/index.js").`),
		"content": sp("Isi teks yang ingin dimasukkan ke dalam file."),
	})
	toolSchemas["edit_file"] = obj([]string{"path", "old_string", "new_string"}, map[string]any{
		"path":       sp("Jalur lengkap ke file yang ingin diedit."),
		"old_string": sp("Teks lama di dalam file yang ingin diganti. Harus sama persis agar ditemukan."),
		"new_string": sp("Teks baru yang akan menggantikan teks lama."),
	})
	toolSchemas["delete_file"] = obj([]string{"path"}, map[string]any{
		"path": sp("Jalur lengkap ke file yang ingin dihapus."),
	})
	toolSchemas["move_file"] = obj([]string{"source", "destination"}, map[string]any{
		"source":      sp("Path sumber file yang ingin dipindahkan."),
		"destination": sp("Path tujuan baru untuk file tersebut."),
	})
	toolSchemas["send_file"] = obj([]string{"path"}, map[string]any{
		"path":    sp("Jalur lengkap ke file di virtual file system yang ingin dikirim ke Telegram."),
		"caption": sp("Pesan atau deskripsi singkat yang menyertai file."),
	})
	toolSchemas["search_web"] = obj([]string{"q"}, map[string]any{
		"q": sp(`Kata kunci pencarian (contoh: "berita terkini", "cara membuat website").`),
	})
	toolSchemas["crawl"] = obj([]string{"url", "code"}, map[string]any{
		"url":  sp(`URL website yang ingin di-crawl (contoh: "https://example.com/article").`),
		"code": sp(`Kode cheerio untuk mengekstrak data dari halaman. Gunakan $ sebagai root cheerio instance. Contoh: $("h1").text() — boleh juga pakai return, mis. return {title: $("h1").text()};.`),
	})
	toolSchemas["get_current_time"] = obj([]string{"zone"}, map[string]any{
		"zone": sp(`Kode identifier zona waktu IANA (contoh: "Asia/Jakarta", "UTC").`),
	})
	toolSchemas["calculate_math"] = obj([]string{"expression"}, map[string]any{
		"expression": sp(`Rumus matematika yang ingin dihitung (contoh: "sqrt(144) * (25 + 5)").`),
	})
	toolSchemas["e2b_sandbox_create"] = obj(nil, map[string]any{})
	toolSchemas["e2b_run_code"] = obj([]string{"path"}, map[string]any{
		"path":     sp(`Jalur lengkap file kode di VFS yang ingin dijalankan (contoh: "scripts/analisis.py").`),
		"language": sp("Bahasa pemrograman (default: python)."),
	})
	toolSchemas["e2b_install_package"] = obj([]string{"package_name"}, map[string]any{
		"package_name": sp(`Nama package yang ingin diinstal (contoh: "pandas", "axios", "express").`),
		"manager":      sp(`Package manager: "pip" untuk Python (default), "npm" untuk Node.js.`),
	})
	toolSchemas["e2b_send_file"] = obj([]string{"path"}, map[string]any{
		"path":    sp(`Jalur file di sandbox E2B yang ingin dikirim (contoh: "/tmp/chart.png").`),
		"caption": sp("Deskripsi atau keterangan singkat yang menyertai file saat dikirim ke Telegram."),
	})
	toolSchemas["e2b_sandbox_kill"] = obj(nil, map[string]any{})
	toolSchemas["create_skill"] = obj([]string{"name", "description", "steps"}, map[string]any{
		"name":        sp(`Nama skill. Hanya boleh berisi huruf, angka, dan hyphen (contoh: "soundcloud-downloader").`),
		"description": sp("Deskripsi singkat tentang apa yang dilakukan skill ini."),
		"steps": map[string]any{
			"type": "array",
			"items": map[string]any{
				"type": "object",
				"properties": map[string]any{
					"title":       sp("Judul langkah (contoh: Install library)."),
					"instruction": sp("Instruksi detail untuk langkah ini."),
				},
				"required": []string{"title", "instruction"},
			},
			"description": "Array langkah-langkah dalam workflow skill.",
		},
	})
	toolSchemas["use_skills"] = obj([]string{"name"}, map[string]any{
		"name": sp("Nama skill yang ingin digunakan (tanpa ekstensi .md)."),
	})
	toolSchemas["delete_skill"] = obj([]string{"name"}, map[string]any{
		"name": sp("Nama skill yang ingin dihapus."),
	})
	toolSchemas["search_skills"] = obj([]string{"query"}, map[string]any{
		"query": sp(`Kata kunci pencarian (contoh: "weather", "web scraping").`),
	})
	toolSchemas["install_skill"] = obj([]string{"url"}, map[string]any{
		"url": sp(`Target install: slug/URL GitHub berisi skill (contoh: "user/repo", "https://github.com/user/repo") atau target ClawHub dengan prefix clawhub: (contoh: "clawhub:weather").`),
	})
}
