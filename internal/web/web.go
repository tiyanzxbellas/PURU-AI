// Package web serves the mobile-friendly /login settings page and its JSON API.
// The existing JSON health check on "/" is preserved so platform health probes
// keep working.
package web

import (
	"context"
	"embed"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"mime"
	"net/http"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"github.com/purujawa06-bot/PURU-AI/internal/auth"
	"github.com/purujawa06-bot/PURU-AI/internal/config"
	"github.com/purujawa06-bot/PURU-AI/internal/settings"
	"github.com/purujawa06-bot/PURU-AI/internal/skills"
)

//go:embed all:dist
var distFS embed.FS

// Serve returns an http.Server that mounts the health check on "/" (preserving
// the existing JSON contract) and the settings page + API under /login/{id}/{pw}.
func Serve(cfg *config.Config, hc *http.Client, am *auth.Manager, sm *settings.Manager, cat *skills.Catalog, reg *skills.Registry) *http.Server {
	srv := &http.Server{
		Addr:    cfg.Hostname + ":" + itoa(cfg.Port),
		Handler: NewMux(cfg.AI, hc, am, sm, cat, reg),
	}
	return srv
}

// NewMux builds the HTTP handler with the health check, the login page and the
// settings API. Exposed separately so tests can exercise the routes directly.
func NewMux(globalAI config.AIConfig, hc *http.Client, am *auth.Manager, sm *settings.Manager, cat *skills.Catalog, reg *skills.Registry) http.Handler {
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
			apiConfigHandler(am, sm)(w, r)
		case "/api/config/clear":
			apiConfigClearHandler(am, sm)(w, r)
		case "/api/models":
			apiModelsHandler(am, sm, globalAI, hc)(w, r)
		case "/api/skills/list":
			apiSkillsListHandler(am, cat)(w, r)
		case "/api/skills/search":
			apiSkillsSearchHandler(am, reg)(w, r)
		case "/api/skills/install":
			apiSkillsInstallHandler(am, reg)(w, r)
		case "/api/skills/delete":
			apiSkillsDeleteHandler(am, cat)(w, r)
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

func apiConfigHandler(am *auth.Manager, sm *settings.Manager) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		id, ok := resolveAuth(r, am)
		if !ok {
			jsonErr(w, http.StatusUnauthorized, "Unauthorized")
			return
		}
		switch r.Method {
		case http.MethodGet:
			cfg := sm.Get(r.Context(), id)
			eff := settings.Effective(config.AIConfig{}, cfg)
			resp := map[string]any{"ok": true, "baseUrl": eff.BaseURL, "model": eff.Model, "hasApiKey": eff.APIKey != ""}
			if cfg != nil && cfg.SystemPrompt != nil {
				resp["systemPrompt"] = *cfg.SystemPrompt
			} else {
				resp["systemPrompt"] = ""
			}
			jsonOK(w, resp)
		case http.MethodPost:
			var body struct {
				BaseURL      *string `json:"baseUrl"`
				APIKey       *string `json:"apiKey"`
				Model        *string `json:"model"`
				SystemPrompt *string `json:"systemPrompt"`
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
			// SystemPrompt always applied (even if empty = clear).
			cfg.SystemPrompt = body.SystemPrompt
			if cfg.SystemPrompt != nil && strings.TrimSpace(*cfg.SystemPrompt) == "" {
				cfg.SystemPrompt = nil
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
		if err := sm.Delete(r.Context(), id); err != nil {
			jsonErr(w, http.StatusInternalServerError, err.Error())
			return
		}
		jsonOK(w, map[string]any{"ok": true})
	}
}

// ---------------------------------------------------------------------------
// API: models
// ---------------------------------------------------------------------------

func apiModelsHandler(am *auth.Manager, sm *settings.Manager, global config.AIConfig, hc *http.Client) http.HandlerFunc {
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
		eff := settings.Effective(global, sm.Get(r.Context(), id))
		ids, err := fetchModels(r.Context(), eff, hc)
		if err != nil {
			jsonErr(w, http.StatusBadGateway, err.Error())
			return
		}
		jsonOK(w, map[string]any{"ok": true, "models": ids, "baseUrl": eff.BaseURL})
	}
}

// fetchModels queries the provider's OpenAI-compatible GET /models endpoint for
// the effective (global + user override) API config.
func fetchModels(ctx context.Context, aiCfg config.AIConfig, hc *http.Client) ([]string, error) {
	if strings.TrimSpace(aiCfg.BaseURL) == "" {
		return nil, errors.New("Base URL belum diset")
	}
	if hc == nil {
		hc = http.DefaultClient
	}
	ctx, cancel := context.WithTimeout(ctx, 15*time.Second)
	defer cancel()
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, strings.TrimSuffix(aiCfg.BaseURL, "/")+"/models", nil)
	if err != nil {
		return nil, err
	}
	req.Header.Set("Accept", "application/json")
	if aiCfg.APIKey != "" {
		req.Header.Set("Authorization", "Bearer "+aiCfg.APIKey)
	}
	resp, err := hc.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	if resp.StatusCode >= 400 {
		body, _ := io.ReadAll(io.LimitReader(resp.Body, 512))
		return nil, fmt.Errorf("API models HTTP %d: %s", resp.StatusCode, strings.TrimSpace(string(body)))
	}
	var payload struct {
		Data []struct {
			ID string `json:"id"`
		} `json:"data"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&payload); err != nil {
		return nil, fmt.Errorf("decode daftar model: %w", err)
	}
	var ids []string
	for _, m := range payload.Data {
		if m.ID != "" {
			ids = append(ids, m.ID)
		}
	}
	if len(ids) == 0 {
		return nil, errors.New("API tidak mengembalikan daftar model")
	}
	return ids, nil
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
