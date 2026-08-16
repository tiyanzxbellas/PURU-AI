// Package web serves the mobile-friendly /login settings page and its JSON API.
// The existing JSON health check on "/" is preserved so platform health probes
// keep working.
package web

import (
	"embed"
	"encoding/json"
	"fmt"
	"io/fs"
	"mime"
	"net/http"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"github.com/purujawa06-bot/PURU-AI/internal/auth"
	"github.com/purujawa06-bot/PURU-AI/internal/combos"
	"github.com/purujawa06-bot/PURU-AI/internal/config"
	"github.com/purujawa06-bot/PURU-AI/internal/firebase"
	"github.com/purujawa06-bot/PURU-AI/internal/providers"
	"github.com/purujawa06-bot/PURU-AI/internal/servelog"
	"github.com/purujawa06-bot/PURU-AI/internal/settings"
	"github.com/purujawa06-bot/PURU-AI/internal/skills"
	"github.com/purujawa06-bot/PURU-AI/internal/usage"
	"github.com/purujawa06-bot/PURU-AI/internal/vfs"
)

//go:embed all:dist
var distFS embed.FS

// Serve returns an http.Server that mounts the health check on "/" (preserving
// the existing JSON contract) and the settings page + API under /login/{id}/{pw}.
func Serve(cfg *config.Config, am *auth.Manager, sm *settings.Manager, cat *skills.Catalog, reg *skills.Registry, v *vfs.VFS, um *usage.Manager, lg *servelog.Buffer, cb *combos.Manager, pv *providers.Manager) *http.Server {
	srv := &http.Server{
		Addr:    cfg.Hostname + ":" + itoa(cfg.Port),
		Handler: NewMux(am, sm, cat, reg, v, um, lg, cb, pv, cfg.AI),
	}
	return srv
}

// NewMux builds the HTTP handler with the health check, the login page and the
// settings API. Exposed separately so tests can exercise the routes directly.
// aiCfg is the server-wide AI config: its ProxyURL is the built-in relay URL
// reported to the dashboard (Proxy ON/OFF toggle).
func NewMux(am *auth.Manager, sm *settings.Manager, cat *skills.Catalog, reg *skills.Registry, v *vfs.VFS, um *usage.Manager, lg *servelog.Buffer, cb *combos.Manager, pv *providers.Manager, aiCfg config.AIConfig) http.Handler {
	mux := http.NewServeMux()
	sub, err := fs.Sub(distFS, "dist")
	if err != nil {
		panic(fmt.Sprintf("web: embed dist: %v", err))
	}

	// Health check - identical JSON so existing probes still pass.
	mux.HandleFunc("/", func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/" && r.URL.Path != "/health" {
			http.NotFound(w, r)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]any{
			"status":    "ok",
			"bot":       "PURU-AI",
			"running":   true,
			"timestamp": time.Now().UTC().Format("2006-01-02T15:04:05.000Z"),
		})
	})

	// Serve the settings page and the JSON API under /login/{id}/{pw}/...
	mux.HandleFunc("/login/", func(w http.ResponseWriter, r *http.Request) {
		rest := afterLoginPrefix(r.URL.Path)
		if strings.HasPrefix(rest, "/api/") {
			if _, ok := resolveAuth(r, am); !ok {
				jsonErr(w, http.StatusUnauthorized, "Unauthorized")
				return
			}
		}
		switch rest {
		case "/api/config":
			apiConfigHandler(am, sm, aiCfg)(w, r)
		case "/api/config/clear":
			apiConfigClearHandler(am, sm)(w, r)
		case "/api/skills/list":
			apiSkillsListHandler(am, cat)(w, r)
		case "/api/skills/search":
			apiSkillsSearchHandler(am, reg)(w, r)
		case "/api/skills/install":
			apiSkillsInstallHandler(am, reg)(w, r)
		case "/api/skills/delete":
			apiSkillsDeleteHandler(am, cat)(w, r)
		case "/api/memory":
			apiMemoryHandler(am, v)(w, r)
		case "/api/files/list":
			apiFilesListHandler(am, v)(w, r)
		case "/api/files/read":
			apiFilesReadHandler(am, v)(w, r)
		case "/api/files/write":
			apiFilesWriteHandler(am, v)(w, r)
		case "/api/files/delete":
			apiFilesDeleteHandler(am, v)(w, r)
		case "/api/usage":
			apiUsageHandler(am, um)(w, r)
		case "/api/logs":
			apiLogsHandler(am, lg)(w, r)
		case "/api/combos":
			apiCombosHandler(am, cb)(w, r)
		case "/api/combos/activate":
			apiCombosActivateHandler(am, cb)(w, r)
		case "/api/combos/delete":
			apiCombosDeleteHandler(am, cb)(w, r)
		case "/api/providers":
			apiProvidersHandler(am, pv)(w, r)
		case "/api/providers/check":
			apiProvidersCheckHandler(am, pv)(w, r)
		case "/api/providers/delete":
			apiProvidersDeleteHandler(am, sm, cb, pv)(w, r)
		default:
			loginHandler(am, sub)(w, r)
		}
	})

	return mux
}

// afterLoginPrefix returns the path after /login/{id}/{pw}, e.g. "/api/config".
func afterLoginPrefix(path string) string {
	p := strings.TrimPrefix(path, "/login/")
	parts := strings.SplitN(p, "/", 3)
	if len(parts) < 3 {
		return ""
	}
	return "/" + parts[2]
}

// ---------------------------------------------------------------------------
// helpers
// ---------------------------------------------------------------------------

func itoa(n int) string {
	if n == 0 {
		return "0"
	}
	var buf [8]byte
	i := len(buf)
	for n > 0 {
		i--
		buf[i] = byte('0' + n%10)
		n /= 10
	}
	return string(buf[i:])
}

// extractIDPW parses {id}/{pw} from the first two path segments after the
// given prefix (e.g. /login/ or /login/{id}/{pw}/api). Returns 0, "" on
// failure.
func extractIDPW(path string, prefix string) (int64, string) {
	p := strings.TrimPrefix(path, prefix)
	p = strings.TrimPrefix(p, "/")
	parts := strings.SplitN(p, "/", 3)
	if len(parts) < 2 {
		return 0, ""
	}
	id, err := strconv.ParseInt(parts[0], 10, 64)
	if err != nil {
		return 0, ""
	}
	return id, parts[1]
}

func jsonOK(w http.ResponseWriter, data map[string]any) {
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(data)
}

func jsonErr(w http.ResponseWriter, code int, msg string) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(code)
	_ = json.NewEncoder(w).Encode(map[string]any{"ok": false, "error": msg})
}

// resolveAuth verifies the {id}/{pw} pair from the /login/{id}/{pw}/... path.
func resolveAuth(r *http.Request, am *auth.Manager) (int64, bool) {
	id, pw := extractIDPW(r.URL.Path, "/login")
	if id != 0 && pw != "" && am.Verify(r.Context(), id, pw) {
		return id, true
	}
	return 0, false
}

// ---------------------------------------------------------------------------
// login page handler
// ---------------------------------------------------------------------------

func loginHandler(am *auth.Manager, dist fs.FS) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		id, pw := extractIDPW(r.URL.Path, "/login")
		if id == 0 || pw == "" {
			http.NotFound(w, r)
			return
		}
		if !am.Verify(r.Context(), id, pw) {
			w.Header().Set("Content-Type", "text/html; charset=utf-8")
			w.WriteHeader(http.StatusUnauthorized)
			fmt.Fprint(w, `<!DOCTYPE html><html><head><title>Access Denied</title></head><body style="font-family:sans-serif;text-align:center;padding:40px;background:#0f172a;color:#e2e8f0"><h1>401 - Akses Ditolak</h1><p>Password salah atau tidak valid.</p></body></html>`)
			return
		}
		prefix := fmt.Sprintf("/login/%d/%s", id, pw)
		rest := strings.TrimPrefix(r.URL.Path, prefix)
		// The SPA is served at /login/{id}/{pw}/ (trailing slash) so the
		// relative ./assets/ refs in the built index.html resolve to
		// /login/{id}/{pw}/assets/... Without the slash the browser treats the
		// last path segment as a file, drops it and requests
		// /login/{id}/assets/... which fails auth -> blank page (401).
		if rest == "" {
			http.Redirect(w, r, prefix+"/", http.StatusMovedPermanently)
			return
		}
		rel := strings.TrimPrefix(rest, "/")
		if rel == "" || !strings.Contains(filepath.Base(rel), ".") {
			rel = "index.html"
		}
		data, err := fs.ReadFile(dist, rel)
		if err != nil {
			if rel != "index.html" {
				if idx, e2 := fs.ReadFile(dist, "index.html"); e2 == nil {
					w.Header().Set("Content-Type", "text/html; charset=utf-8")
					w.Write(idx)
					return
				}
			}
			http.NotFound(w, r)
			return
		}
		if ct := mime.TypeByExtension(filepath.Ext(rel)); ct != "" {
			w.Header().Set("Content-Type", ct)
		}
		w.Write(data)
	}
}

// ---------------------------------------------------------------------------
// API: config
// ---------------------------------------------------------------------------

func apiConfigHandler(am *auth.Manager, sm *settings.Manager, aiCfg config.AIConfig) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		id, ok := resolveAuth(r, am)
		if !ok {
			jsonErr(w, http.StatusUnauthorized, "Unauthorized")
			return
		}
		switch r.Method {
		case http.MethodGet:
			cfg := sm.Get(r.Context(), id)
			// The global config passed here only carries the proxy relay so the
			// effective proxyUrl reflects the built-in relay (ON by default) while
			// the other fields keep hiding the server defaults.
			eff := settings.Effective(config.AIConfig{ProxyURL: aiCfg.ProxyURL}, cfg)
			// API key dikembalikan mentah (permintaan user) — halaman login
			// diproteksi password sehingga hanya pemilik chat yang bisa lihat.
			resp := map[string]any{
				"ok":       true,
				"baseUrl":  eff.BaseURL,
				"apiKey":   eff.APIKey,
				"model":    eff.Model,
				"proxyUrl": eff.ProxyURL,
				// relayUrl = built-in Vercel relay URL (global default) — dipakai
				// dashboard untuk toggle Proxy ON/OFF.
				"relayUrl": aiCfg.ProxyURL,
			}
			if eff.Headers != nil {
				resp["headers"] = eff.Headers
			} else {
				resp["headers"] = map[string]string{}
			}
			if cfg != nil && cfg.SystemPrompt != nil {
				resp["systemPrompt"] = *cfg.SystemPrompt
			} else {
				resp["systemPrompt"] = ""
			}
			jsonOK(w, resp)
		case http.MethodPost:
			var body struct {
				BaseURL      *string           `json:"baseUrl"`
				APIKey       *string           `json:"apiKey"`
				Model        *string           `json:"model"`
				SystemPrompt *string           `json:"systemPrompt"`
				ProxyURL     *string           `json:"proxyUrl"`
				Headers      map[string]string `json:"headers"`
			}
			if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
				jsonErr(w, http.StatusBadRequest, "Invalid JSON")
				return
			}
			cfg := sm.Get(r.Context(), id)
			if cfg == nil {
				cfg = &settings.Config{}
			}
			if body.BaseURL != nil {
				if *body.BaseURL == "" {
					cfg.BaseURL = nil
				} else {
					cfg.BaseURL = body.BaseURL
				}
			}
			if body.APIKey != nil {
				if *body.APIKey == "" {
					cfg.APIKey = nil
				} else {
					cfg.APIKey = body.APIKey
				}
			}
			if body.Model != nil {
				if *body.Model == "" {
					cfg.Model = nil
				} else {
					cfg.Model = body.Model
				}
			}
			// SystemPrompt hanya diubah bila field dikirim (non-nil) — biarkan
			// partial override {model} saja tidak menghapus system prompt.
			// Nilai kosong = hapus.
			if body.SystemPrompt != nil {
				if strings.TrimSpace(*body.SystemPrompt) == "" {
					cfg.SystemPrompt = nil
				} else {
					cfg.SystemPrompt = body.SystemPrompt
				}
			}
			// ProxyURL: nil = jangan sentuh, "" = matikan (langsung ke
			// endpoint — override global relay), terisi = pakai relay.
			if body.ProxyURL != nil {
				if *body.ProxyURL == "" {
					s := ""
					cfg.ProxyURL = &s
				} else {
					cfg.ProxyURL = body.ProxyURL
				}
			}
			// Headers: nil = jangan sentuh (partial update), {} = hapus semua,
			// terisi = ganti seluruh set.
			if body.Headers != nil {
				if len(body.Headers) == 0 {
					cfg.Headers = nil
				} else {
					cfg.Headers = body.Headers
				}
			}
			if err := sm.Set(r.Context(), id, cfg); err != nil {
				jsonErr(w, http.StatusInternalServerError, err.Error())
				return
			}
			jsonOK(w, map[string]any{"ok": true})
		default:
			jsonErr(w, http.StatusMethodNotAllowed, "Method not allowed")
		}
	}
}

func apiConfigClearHandler(am *auth.Manager, sm *settings.Manager) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost {
			jsonErr(w, http.StatusMethodNotAllowed, "Method not allowed")
			return
		}
		id, ok := resolveAuth(r, am)
		if !ok {
			jsonErr(w, http.StatusUnauthorized, "Unauthorized")
			return
		}
		// Reset keeps the user's SystemPrompt (custom role); only the AI
		// connection fields (base URL, key, model, proxy, headers) are cleared.
		if err := sm.DeleteKeepPrompt(r.Context(), id); err != nil {
			jsonErr(w, http.StatusInternalServerError, err.Error())
			return
		}
		jsonOK(w, map[string]any{"ok": true})
	}
}

// ---------------------------------------------------------------------------
// API: skills
// ---------------------------------------------------------------------------

func apiSkillsListHandler(am *auth.Manager, cat *skills.Catalog) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodGet {
			jsonErr(w, http.StatusMethodNotAllowed, "Method not allowed")
			return
		}
		id, ok := resolveAuth(r, am)
		if !ok {
			jsonErr(w, http.StatusUnauthorized, "Unauthorized")
			return
		}
		list := cat.ListSkills(r.Context(), id)
		type skillInfo struct {
			Name        string `json:"name"`
			Description string `json:"description"`
		}
		out := make([]skillInfo, len(list))
		for i, s := range list {
			out[i] = skillInfo{Name: s.Name, Description: s.Description}
		}
		jsonOK(w, map[string]any{"ok": true, "skills": out})
	}
}

func apiSkillsSearchHandler(am *auth.Manager, reg *skills.Registry) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost {
			jsonErr(w, http.StatusMethodNotAllowed, "Method not allowed")
			return
		}
		id, ok := resolveAuth(r, am)
		if !ok {
			jsonErr(w, http.StatusUnauthorized, "Unauthorized")
			return
		}
		var body struct {
			Query string `json:"query"`
		}
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil || body.Query == "" {
			jsonErr(w, http.StatusBadRequest, "Missing query")
			return
		}
		_ = id
		results, err := reg.SearchSkills(r.Context(), body.Query)
		if err != nil {
			jsonErr(w, http.StatusInternalServerError, err.Error())
			return
		}
		type result struct {
			DisplayName string `json:"displayName"`
			Slug        string `json:"slug"`
			Summary     string `json:"summary"`
			Registry    string `json:"registry"`
			Target      string `json:"target"`
		}
		out := make([]result, 0, len(results))
		for _, r := range results {
			target := r.Slug
			if r.RegistryName == "clawhub" {
				target = "clawhub:" + r.Slug
			}
			out = append(out, result{
				DisplayName: r.DisplayName,
				Slug:        r.Slug,
				Summary:     r.Summary,
				Registry:    r.RegistryName,
				Target:      target,
			})
		}
		jsonOK(w, map[string]any{"ok": true, "results": out})
	}
}

func apiSkillsInstallHandler(am *auth.Manager, reg *skills.Registry) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost {
			jsonErr(w, http.StatusMethodNotAllowed, "Method not allowed")
			return
		}
		id, ok := resolveAuth(r, am)
		if !ok {
			jsonErr(w, http.StatusUnauthorized, "Unauthorized")
			return
		}
		var body struct {
			Target string `json:"target"`
		}
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil || body.Target == "" {
			jsonErr(w, http.StatusBadRequest, "Missing target")
			return
		}
		var res skills.InstallResult
		if strings.HasPrefix(body.Target, "clawhub:") {
			res = reg.InstallFromClawHub(r.Context(), id, strings.TrimPrefix(body.Target, "clawhub:"))
		} else {
			res = reg.InstallFromGitHub(r.Context(), id, body.Target)
		}
		if res.Success {
			jsonOK(w, map[string]any{"ok": true, "name": res.Name, "path": res.Path})
			return
		}
		jsonErr(w, http.StatusBadRequest, res.Error)
	}
}

func apiSkillsDeleteHandler(am *auth.Manager, cat *skills.Catalog) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost {
			jsonErr(w, http.StatusMethodNotAllowed, "Method not allowed")
			return
		}
		id, ok := resolveAuth(r, am)
		if !ok {
			jsonErr(w, http.StatusUnauthorized, "Unauthorized")
			return
		}
		var body struct {
			Name string `json:"name"`
		}
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil || body.Name == "" {
			jsonErr(w, http.StatusBadRequest, "Missing name")
			return
		}
		deleted, err := cat.DeleteSkill(r.Context(), id, body.Name)
		if err != nil {
			jsonErr(w, http.StatusInternalServerError, err.Error())
			return
		}
		if deleted {
			jsonOK(w, map[string]any{"ok": true})
			return
		}
		jsonErr(w, http.StatusNotFound, fmt.Sprintf("Skill %q not found", body.Name))
	}
}

// ---------------------------------------------------------------------------
// API: memory (MEMORY.md)
// ---------------------------------------------------------------------------

const memoryPath = "memory/MEMORY.md"

func apiMemoryHandler(am *auth.Manager, v *vfs.VFS) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		id, ok := resolveAuth(r, am)
		if !ok {
			jsonErr(w, http.StatusUnauthorized, "Unauthorized")
			return
		}
		switch r.Method {
		case http.MethodGet:
			content, exists := v.ReadFile(r.Context(), id, memoryPath)
			jsonOK(w, map[string]any{"ok": true, "exists": exists, "content": content})
		case http.MethodPost:
			var body struct {
				Content string `json:"content"`
			}
			if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
				jsonErr(w, http.StatusBadRequest, "Invalid JSON")
				return
			}
			if strings.TrimSpace(body.Content) == "" {
				// kosong = hapus, konsisten dengan perilaku path lain (null = tak ada).
				if _, err := v.DeleteFile(r.Context(), id, memoryPath); err != nil {
					jsonErr(w, http.StatusInternalServerError, err.Error())
					return
				}
				jsonOK(w, map[string]any{"ok": true})
				return
			}
			if err := v.WriteFile(r.Context(), id, memoryPath, body.Content); err != nil {
				jsonErr(w, http.StatusInternalServerError, err.Error())
				return
			}
			jsonOK(w, map[string]any{"ok": true})
		default:
			jsonErr(w, http.StatusMethodNotAllowed, "Method not allowed")
		}
	}
}

// ---------------------------------------------------------------------------
// API: VFS file browser
// ---------------------------------------------------------------------------

func apiFilesListHandler(am *auth.Manager, v *vfs.VFS) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodGet {
			jsonErr(w, http.StatusMethodNotAllowed, "Method not allowed")
			return
		}
		id, ok := resolveAuth(r, am)
		if !ok {
			jsonErr(w, http.StatusUnauthorized, "Unauthorized")
			return
		}
		path := firebase.NormalizePath(r.URL.Query().Get("path"))
		entries := v.ListDirectory(r.Context(), id, path)
		jsonOK(w, map[string]any{"ok": true, "path": path, "entries": entries})
	}
}

func apiFilesReadHandler(am *auth.Manager, v *vfs.VFS) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodGet {
			jsonErr(w, http.StatusMethodNotAllowed, "Method not allowed")
			return
		}
		id, ok := resolveAuth(r, am)
		if !ok {
			jsonErr(w, http.StatusUnauthorized, "Unauthorized")
			return
		}
		path := firebase.NormalizePath(r.URL.Query().Get("path"))
		if path == "" {
			jsonErr(w, http.StatusBadRequest, "Missing path")
			return
		}
		content, exists := v.ReadFile(r.Context(), id, path)
		if !exists {
			jsonErr(w, http.StatusNotFound, fmt.Sprintf("File %q not found", path))
			return
		}
		jsonOK(w, map[string]any{"ok": true, "path": path, "content": content})
	}
}

func apiFilesWriteHandler(am *auth.Manager, v *vfs.VFS) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost {
			jsonErr(w, http.StatusMethodNotAllowed, "Method not allowed")
			return
		}
		id, ok := resolveAuth(r, am)
		if !ok {
			jsonErr(w, http.StatusUnauthorized, "Unauthorized")
			return
		}
		var body struct {
			Path    string `json:"path"`
			Content string `json:"content"`
		}
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
			jsonErr(w, http.StatusBadRequest, "Invalid JSON")
			return
		}
		path := firebase.NormalizePath(body.Path)
		if path == "" {
			jsonErr(w, http.StatusBadRequest, "Missing path")
			return
		}
		if err := v.WriteFile(r.Context(), id, path, body.Content); err != nil {
			jsonErr(w, http.StatusInternalServerError, err.Error())
			return
		}
		jsonOK(w, map[string]any{"ok": true, "path": path})
	}
}

func apiFilesDeleteHandler(am *auth.Manager, v *vfs.VFS) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost {
			jsonErr(w, http.StatusMethodNotAllowed, "Method not allowed")
			return
		}
		id, ok := resolveAuth(r, am)
		if !ok {
			jsonErr(w, http.StatusUnauthorized, "Unauthorized")
			return
		}
		var body struct {
			Path string `json:"path"`
			Type string `json:"type"`
		}
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
			jsonErr(w, http.StatusBadRequest, "Invalid JSON")
			return
		}
		path := firebase.NormalizePath(body.Path)
		if path == "" {
			jsonErr(w, http.StatusBadRequest, "Missing path")
			return
		}
		var deleted bool
		var err error
		if body.Type == "dir" {
			deleted, err = v.DeleteDir(r.Context(), id, path)
		} else {
			deleted, err = v.DeleteFile(r.Context(), id, path)
		}
		if err != nil {
			jsonErr(w, http.StatusInternalServerError, err.Error())
			return
		}
		if !deleted {
			jsonErr(w, http.StatusNotFound, fmt.Sprintf("Path %q not found", path))
			return
		}
		jsonOK(w, map[string]any{"ok": true, "path": path})
	}
}

// ---------------------------------------------------------------------------
// API: usage (token usage per user)
// ---------------------------------------------------------------------------

func apiUsageHandler(am *auth.Manager, um *usage.Manager) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		id, ok := resolveAuth(r, am)
		if !ok {
			jsonErr(w, http.StatusUnauthorized, "Unauthorized")
			return
		}
		switch r.Method {
		case http.MethodGet:
			recs, err := um.List(r.Context(), id, usage.MaxRecords)
			if err != nil {
				jsonErr(w, http.StatusInternalServerError, err.Error())
				return
			}
			if recs == nil {
				recs = []usage.Record{}
			}
			jsonOK(w, map[string]any{
				"ok":      true,
				"summary": usage.Summarize(recs),
				"records": recs,
			})
		case http.MethodPost:
			// POST = clear (empty body)
			if err := um.Clear(r.Context(), id); err != nil {
				jsonErr(w, http.StatusInternalServerError, err.Error())
				return
			}
			jsonOK(w, map[string]any{"ok": true})
		default:
			jsonErr(w, http.StatusMethodNotAllowed, "Method not allowed")
		}
	}
}

// ---------------------------------------------------------------------------
// API: server logs (live console tail)
// ---------------------------------------------------------------------------

func apiLogsHandler(am *auth.Manager, lg *servelog.Buffer) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if _, ok := resolveAuth(r, am); !ok {
			jsonErr(w, http.StatusUnauthorized, "Unauthorized")
			return
		}
		switch r.Method {
		case http.MethodGet:
			lines := lg.Tail(0)
			if lines == nil {
				lines = []string{}
			}
			jsonOK(w, map[string]any{"ok": true, "lines": lines})
		case http.MethodPost:
			lg.Clear()
			jsonOK(w, map[string]any{"ok": true})
		default:
			jsonErr(w, http.StatusMethodNotAllowed, "Method not allowed")
		}
	}
}

// ---------------------------------------------------------------------------
// API: combos (model groups per user)
// ---------------------------------------------------------------------------

func apiCombosHandler(am *auth.Manager, cb *combos.Manager) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		id, ok := resolveAuth(r, am)
		if !ok {
			jsonErr(w, http.StatusUnauthorized, "Unauthorized")
			return
		}
		switch r.Method {
		case http.MethodGet:
			list, err := cb.List(r.Context(), id)
			if err != nil {
				jsonErr(w, http.StatusInternalServerError, err.Error())
				return
			}
			if list == nil {
				list = []combos.Combo{}
			}
			jsonOK(w, map[string]any{"ok": true, "combos": list, "active": comboActiveID(cb, r, id)})
		case http.MethodPost:
			var body combos.Combo
			if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
				jsonErr(w, http.StatusBadRequest, "Invalid JSON")
				return
			}
			saved, err := cb.Upsert(r.Context(), id, body)
			if err != nil {
				jsonErr(w, http.StatusBadRequest, err.Error())
				return
			}
			jsonOK(w, map[string]any{"ok": true, "combo": saved})
		default:
			jsonErr(w, http.StatusMethodNotAllowed, "Method not allowed")
		}
	}
}

func comboActiveID(cb *combos.Manager, r *http.Request, id int64) string {
	ac := cb.ActiveCombo(r.Context(), id)
	if ac == nil {
		return ""
	}
	return ac.ID
}

func apiCombosDeleteHandler(am *auth.Manager, cb *combos.Manager) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost {
			jsonErr(w, http.StatusMethodNotAllowed, "Method not allowed")
			return
		}
		id, ok := resolveAuth(r, am)
		if !ok {
			jsonErr(w, http.StatusUnauthorized, "Unauthorized")
			return
		}
		var body struct {
			ID string `json:"id"`
		}
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
			jsonErr(w, http.StatusBadRequest, "Invalid JSON")
			return
		}
		if body.ID == "" {
			jsonErr(w, http.StatusBadRequest, "Missing id")
			return
		}
		removed, err := cb.Delete(r.Context(), id, body.ID)
		if err != nil {
			jsonErr(w, http.StatusInternalServerError, err.Error())
			return
		}
		if !removed {
			jsonErr(w, http.StatusNotFound, "Combo not found")
			return
		}
		jsonOK(w, map[string]any{"ok": true, "deleted": body.ID})
	}
}

func apiCombosActivateHandler(am *auth.Manager, cb *combos.Manager) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost {
			jsonErr(w, http.StatusMethodNotAllowed, "Method not allowed")
			return
		}
		id, ok := resolveAuth(r, am)
		if !ok {
			jsonErr(w, http.StatusUnauthorized, "Unauthorized")
			return
		}
		var body struct {
			ID string `json:"id"`
		}
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
			jsonErr(w, http.StatusBadRequest, "Invalid JSON")
			return
		}
		if body.ID == "" {
			if err := cb.SetActive(r.Context(), id, ""); err != nil {
				jsonErr(w, http.StatusInternalServerError, err.Error())
				return
			}
			jsonOK(w, map[string]any{"ok": true, "active": ""})
			return
		}
		if cb.Get(r.Context(), id, body.ID) == nil {
			jsonErr(w, http.StatusBadRequest, "Combo not found")
			return
		}
		if err := cb.SetActive(r.Context(), id, body.ID); err != nil {
			jsonErr(w, http.StatusInternalServerError, err.Error())
			return
		}
		jsonOK(w, map[string]any{"ok": true, "active": body.ID})
	}
}

// ---------------------------------------------------------------------------
// API: providers (9router-style provider registry + live model catalog)
// ---------------------------------------------------------------------------

func readProviderBody(r *http.Request, p *providers.Provider) error {
	return json.NewDecoder(r.Body).Decode(p)
}

func apiProvidersHandler(am *auth.Manager, pv *providers.Manager) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		id, ok := resolveAuth(r, am)
		if !ok {
			jsonErr(w, http.StatusUnauthorized, "Unauthorized")
			return
		}
		switch r.Method {
		case http.MethodGet:
			list, err := pv.List(r.Context(), id)
			if err != nil {
				jsonErr(w, http.StatusInternalServerError, err.Error())
				return
			}
			if list == nil {
				list = []providers.Provider{}
			}
			jsonOK(w, map[string]any{"ok": true, "providers": providers.PublicList(list)})
		case http.MethodPost:
			var body providers.Provider
			if err := readProviderBody(r, &body); err != nil {
				jsonErr(w, http.StatusBadRequest, "Invalid JSON")
				return
			}
			stored, err := pv.Upsert(r.Context(), id, body)
			if err != nil {
				jsonErr(w, http.StatusBadRequest, err.Error())
				return
			}
			jsonOK(w, map[string]any{"ok": true, "provider": stored.Public()})
		default:
			jsonErr(w, http.StatusMethodNotAllowed, "Method not allowed")
		}
	}
}

func apiProvidersCheckHandler(am *auth.Manager, pv *providers.Manager) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost {
			jsonErr(w, http.StatusMethodNotAllowed, "Method not allowed")
			return
		}
		id, ok := resolveAuth(r, am)
		if !ok {
			jsonErr(w, http.StatusUnauthorized, "Unauthorized")
			return
		}
		var body struct {
			ProviderID string            `json:"providerId"`
			BaseURL    string            `json:"baseUrl"`
			Type       string            `json:"type"`
			APIType    string            `json:"apiType"`
			APIKey     string            `json:"apiKey"`
			Headers    map[string]string `json:"headers"`
			ProxyURL   string            `json:"proxyUrl"`
			Force      bool              `json:"force"`
		}
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
			jsonErr(w, http.StatusBadRequest, "Invalid JSON")
			return
		}
		var res *providers.ModelsResult
		if body.ProviderID != "" {
			res = pv.CheckStored(r.Context(), id, body.ProviderID, body.Force)
		} else {
			if strings.TrimSpace(body.BaseURL) == "" {
				jsonErr(w, http.StatusBadRequest, "Missing baseUrl")
				return
			}
			p := providers.Provider{
				BaseURL:  body.BaseURL,
				Type:     body.Type,
				APIType:  body.APIType,
				APIKey:   body.APIKey,
				Headers:  body.Headers,
				ProxyURL: body.ProxyURL,
			}
			if p.Type == "" {
				p.Type = providers.TypeOpenAI
			}
			res = pv.CheckInline(r.Context(), p)
		}
		jsonOK(w, map[string]any{
			"ok":        true,
			"online":    res.Online,
			"status":    res.Status,
			"error":     res.Error,
			"latencyMs": res.LatencyMs,
			"models":    res.Models,
		})
	}
}

func apiProvidersDeleteHandler(am *auth.Manager, sm *settings.Manager, cb *combos.Manager, pv *providers.Manager) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost {
			jsonErr(w, http.StatusMethodNotAllowed, "Method not allowed")
			return
		}
		id, ok := resolveAuth(r, am)
		if !ok {
			jsonErr(w, http.StatusUnauthorized, "Unauthorized")
			return
		}
		var body struct {
			ID string `json:"id"`
		}
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil || body.ID == "" {
			jsonErr(w, http.StatusBadRequest, "Missing id")
			return
		}
		p := pv.Get(r.Context(), id, body.ID)
		if p != nil && p.Builtin {
			jsonErr(w, http.StatusBadRequest, "Provider built-in tidak bisa dihapus")
			return
		}
		removed, err := pv.Delete(r.Context(), id, body.ID)
		if err != nil {
			jsonErr(w, http.StatusInternalServerError, err.Error())
			return
		}
		if !removed {
			jsonErr(w, http.StatusNotFound, "Provider not found")
			return
		}
		// Clean up references: settings.defaultModel & combo models that point at
		// this provider's prefix (prefix/model-id).
		if p != nil && p.Prefix != "" {
			if cfg := sm.Get(r.Context(), id); cfg != nil {
				changed := false
				if cfg.Model != nil && providers.Referencing(p.Prefix, *cfg.Model) {
					cfg.Model = nil
					changed = true
				}
				if changed {
					if cfg.IsEmpty() {
						_ = sm.Delete(r.Context(), id)
					} else {
						_ = sm.Set(r.Context(), id, cfg)
					}
				}
			}
			cleanComboModels(r, cb, id, p.Prefix)
		}
		jsonOK(w, map[string]any{"ok": true, "deleted": body.ID})
	}
}

// cleanComboModels strips models referencing the given prefix from every combo
// of the chat.
func cleanComboModels(r *http.Request, cb *combos.Manager, chatID int64, prefix string) {
	list, err := cb.List(r.Context(), chatID)
	if err != nil {
		return
	}
	for _, c := range list {
		out := c.Models[:0]
		changed := false
		for _, mo := range c.Models {
			if providers.Referencing(prefix, mo) {
				changed = true
				continue
			}
			out = append(out, mo)
		}
		if changed {
			c.Models = out
			_, _ = cb.Upsert(r.Context(), chatID, c)
		}
	}
}
