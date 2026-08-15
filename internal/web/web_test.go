package web

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"regexp"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/purujawa06-bot/PURU-AI/internal/auth"
	"github.com/purujawa06-bot/PURU-AI/internal/firebase"
	"github.com/purujawa06-bot/PURU-AI/internal/settings"
	"github.com/purujawa06-bot/PURU-AI/internal/skills"
	"github.com/purujawa06-bot/PURU-AI/internal/vfs"
)

// fakeRTDB mimics the Firebase REST semantics the stores rely on: GET of a
// missing node returns HTTP 200 with "null", PUT replaces the node (wiping any
// descendant keys, like real RTDB), PATCH merges without touching descendants,
// and DELETE removes a node.
type fakeRTDB struct {
	mu sync.Mutex
	db map[string]string
}

func newFakeRTDB() *fakeRTDB { return &fakeRTDB{db: map[string]string{}} }

func (f *fakeRTDB) handler() http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		f.mu.Lock()
		defer f.mu.Unlock()
		key := strings.TrimSuffix(strings.TrimPrefix(r.URL.Path, "/"), ".json")
		if r.URL.Query().Get("shallow") == "true" {
			prefix := key + "/"
			out := map[string]bool{}
			for k := range f.db {
				if k == key {
					continue
				}
				if strings.HasPrefix(k, prefix) {
					rest := k[len(prefix):]
					if i := strings.Index(rest, "/"); i < 0 {
						out[rest] = true
					} else {
						out[rest[:i]] = true
					}
				}
			}
			if len(out) == 0 {
				w.Write([]byte("null"))
				return
			}
			raw, _ := json.Marshal(out)
			w.Write(raw)
			return
		}
		switch r.Method {
		case http.MethodGet:
			if v, ok := f.db[key]; ok {
				w.Write([]byte(v))
			} else {
				w.Write([]byte("null"))
			}
		case http.MethodPut, http.MethodPatch:
			var body any
			if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
				w.WriteHeader(http.StatusBadRequest)
				return
			}
			raw, _ := json.Marshal(body)
			f.db[key] = string(raw)
			if r.Method == http.MethodPut {
				// Real RTDB: PUT replaces the node and removes its children.
				for k := range f.db {
					if strings.HasPrefix(k, key+"/") {
						delete(f.db, k)
					}
				}
			}
			w.Write([]byte(raw))
		case http.MethodDelete:
			delete(f.db, key)
			w.Write([]byte("null"))
		default:
			w.WriteHeader(http.StatusMethodNotAllowed)
		}
	})
}

type webEnv struct {
	am  *auth.Manager
	sm  *settings.Manager
	cat *skills.Catalog
	reg *skills.Registry
	vfs *vfs.VFS
	srv *httptest.Server
}

func newWebEnv(t *testing.T) *webEnv {
	t.Helper()
	rtdb := httptest.NewServer(newFakeRTDB().handler())
	t.Cleanup(rtdb.Close)
	hc := rtdb.Client()
	fb := firebase.New(rtdb.URL, hc)
	am := auth.New(fb)
	sm := settings.New(fb, time.Hour)
	v := vfs.New(fb)
	cat := skills.NewCatalog(v)
	reg := skills.NewRegistry(v, skills.RegistryOptions{})
	mux := NewMux(am, sm, cat, reg, v)
	srv := httptest.NewServer(mux)
	t.Cleanup(srv.Close)
	return &webEnv{am: am, sm: sm, cat: cat, reg: reg, vfs: v, srv: srv}
}

func setupAuth(t *testing.T, env *webEnv, id int64, pw string) {
	t.Helper()
	if err := env.am.Set(context.Background(), id, pw); err != nil {
		t.Fatalf("auth set: %v", err)
	}
}

func TestHealthCheck(t *testing.T) {
	env := newWebEnv(t)
	for _, p := range []string{"/", "/health"} {
		resp, err := http.Get(env.srv.URL + p)
		if err != nil {
			t.Fatalf("get %s: %v", p, err)
		}
		resp.Body.Close()
		if resp.StatusCode != 200 {
			t.Fatalf("%s returned %d, want 200", p, resp.StatusCode)
		}
	}
}

func TestLoginPageRequiresAuth(t *testing.T) {
	env := newWebEnv(t)
	setupAuth(t, env, 123, "pw-saya")

	// wrong password -> 401
	resp, err := http.Get(env.srv.URL + "/login/123/salah")
	if err != nil {
		t.Fatal(err)
	}
	resp.Body.Close()
	if resp.StatusCode != http.StatusUnauthorized {
		t.Fatalf("wrong pw got %d, want 401", resp.StatusCode)
	}

	// correct password -> 200 HTML page
	resp, err = http.Get(env.srv.URL + "/login/123/pw-saya")
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("correct pw got %d, want 200", resp.StatusCode)
	}
	if ct := resp.Header.Get("Content-Type"); !strings.Contains(ct, "text/html") {
		t.Fatalf("content-type = %q, want text/html", ct)
	}
}

func TestLoginPageServesBundleAssets(t *testing.T) {
	env := newWebEnv(t)
	setupAuth(t, env, 123, "pw-saya")
	base := env.srv.URL + "/login/123/pw-saya"
	noRedirect := &http.Client{CheckRedirect: func(req *http.Request, via []*http.Request) error { return http.ErrUseLastResponse }}

	// /login/{id}/{pw} without trailing slash must 301 -> /login/{id}/{pw}/ so
	// the relative ./assets/ refs resolve to the authenticated directory.
	resp, err := noRedirect.Get(base)
	if err != nil {
		t.Fatal(err)
	}
	resp.Body.Close()
	if resp.StatusCode != http.StatusMovedPermanently {
		t.Fatalf("expected 301 redirect, got %d", resp.StatusCode)
	}
	if loc := resp.Header.Get("Location"); loc != "/login/123/pw-saya/" {
		t.Fatalf("Location = %q, want /login/123/pw-saya/", loc)
	}

	// The built index.html must reference relative ./assets/... files.
	resp, err = http.Get(base + "/")
	if err != nil {
		t.Fatal(err)
	}
	body, err := io.ReadAll(resp.Body)
	resp.Body.Close()
	if err != nil {
		t.Fatal(err)
	}
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("page got %d, want 200", resp.StatusCode)
	}
	html := string(body)
	if !strings.Contains(html, "id=\"root\"") {
		t.Fatal("index.html does not look like the React bundle (missing #root)")
	}
	m := regexp.MustCompile(`src="\./assets/([^"]+\.js)"`).FindStringSubmatch(html)
	if len(m) != 2 {
		t.Fatalf("no relative ./assets js found in index.html:\n%s", html)
	}
	// The asset must be served (with correct content-type) under the login path.
	aresp, err := http.Get(base + "/assets/" + m[1])
	if err != nil {
		t.Fatal(err)
	}
	defer aresp.Body.Close()
	if aresp.StatusCode != http.StatusOK {
		t.Fatalf("asset %s got %d, want 200", m[1], aresp.StatusCode)
	}
	if ct := aresp.Header.Get("Content-Type"); !strings.Contains(ct, "javascript") {
		t.Fatalf("asset content-type = %q, want javascript", ct)
	}
}

func TestAPIConfigRoundTrip(t *testing.T) {
	env := newWebEnv(t)
	setupAuth(t, env, 42, "rahasia")
	base := env.srv.URL + "/login/42/rahasia/api"

	// initial GET: empty
	resp, err := http.Get(base + "/config")
	if err != nil {
		t.Fatal(err)
	}
	var init struct {
		OK        bool   `json:"ok"`
		BaseURL   string `json:"baseUrl"`
		Model     string `json:"model"`
		APIKey    string `json:"apiKey"`
		SysPrompt string `json:"systemPrompt"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&init); err != nil {
		t.Fatal(err)
	}
	resp.Body.Close()
	if !init.OK || init.APIKey != "" || init.SysPrompt != "" {
		t.Fatalf("unexpected initial state: %+v", init)
	}

	// POST: save config
	body, _ := json.Marshal(map[string]string{
		"baseUrl": "https://api.openai.com/v1", "apiKey": "sk-secret", "model": "gpt-4o",
		"systemPrompt": "Kamu adalah asisten",
	})
	req, _ := http.NewRequest(http.MethodPost, base+"/config", bytes.NewReader(body))
	resp, err = http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	var saved map[string]bool
	if err := json.NewDecoder(resp.Body).Decode(&saved); err != nil {
		t.Fatal(err)
	}
	resp.Body.Close()
	if !saved["ok"] {
		t.Fatal("save failed")
	}

	// GET again: config present, API key masked/hidden
	resp, err = http.Get(base + "/config")
	if err != nil {
		t.Fatal(err)
	}
	var got struct {
		OK        bool   `json:"ok"`
		BaseURL   string `json:"baseUrl"`
		Model     string `json:"model"`
		APIKey    string `json:"apiKey"`
		SysPrompt string `json:"systemPrompt"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&got); err != nil {
		t.Fatal(err)
	}
	resp.Body.Close()
	if got.BaseURL != "https://api.openai.com/v1" || got.Model != "gpt-4o" || got.APIKey != "sk-secret" || got.SysPrompt != "Kamu adalah asisten" {
		t.Fatalf("config not saved: %+v", got)
	}

	// verify store directly (no cache bypass needed; check via API only)
	// clear config
	req, _ = http.NewRequest(http.MethodPost, base+"/config/clear", nil)
	resp, err = http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	var cleared map[string]bool
	json.NewDecoder(resp.Body).Decode(&cleared)
	resp.Body.Close()
	if !cleared["ok"] {
		t.Fatal("clear failed")
	}
	resp, err = http.Get(base + "/config")
	if err != nil {
		t.Fatal(err)
	}
	var after struct {
		APIKey string `json:"apiKey"`
	}
	json.NewDecoder(resp.Body).Decode(&after)
	resp.Body.Close()
	if after.APIKey != "" {
		t.Fatal("key still present after clear")
	}
}

func TestConfigPartialModelUpdateKeepsSystemPrompt(t *testing.T) {
	env := newWebEnv(t)
	setupAuth(t, env, 11, "pw")
	base := env.srv.URL + "/login/11/pw/api"

	// Save full config with a system prompt.
	full, _ := json.Marshal(map[string]string{
		"baseUrl": "https://api.example.com/v1", "apiKey": "sk-x",
		"model": "gpt-4o", "systemPrompt": "Kamu adalah asisten yang ramah",
	})
	req, _ := http.NewRequest(http.MethodPost, base+"/config", bytes.NewReader(full))
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("save got %d, want 200", resp.StatusCode)
	}

	// Apply model only (what the Model section sends on Terapkan/Pilih).
	partial, _ := json.Marshal(map[string]string{"model": "gpt-4o-mini"})
	req, _ = http.NewRequest(http.MethodPost, base+"/config", bytes.NewReader(partial))
	resp, err = http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("partial save got %d, want 200", resp.StatusCode)
	}

	resp, err = http.Get(base + "/config")
	if err != nil {
		t.Fatal(err)
	}
	var got struct {
		Model     string `json:"model"`
		APIKey    string `json:"apiKey"`
		BaseURL   string `json:"baseUrl"`
		SysPrompt string `json:"systemPrompt"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&got); err != nil {
		t.Fatal(err)
	}
	resp.Body.Close()
	if got.Model != "gpt-4o-mini" {
		t.Fatalf("model = %q, want gpt-4o-mini", got.Model)
	}
	if got.SysPrompt != "Kamu adalah asisten yang ramah" {
		t.Fatalf("systemPrompt hilang setelah partial update: %q", got.SysPrompt)
	}
	if got.APIKey != "sk-x" || got.BaseURL != "https://api.example.com/v1" {
		t.Fatalf("field lain hilang: %+v", got)
	}

	// Clearing the model must also keep the system prompt.
	clearModel, _ := json.Marshal(map[string]string{"model": ""})
	req, _ = http.NewRequest(http.MethodPost, base+"/config", bytes.NewReader(clearModel))
	resp, err = http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	resp.Body.Close()
	resp, err = http.Get(base + "/config")
	if err != nil {
		t.Fatal(err)
	}
	var got2 struct {
		Model     string `json:"model"`
		SysPrompt string `json:"systemPrompt"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&got2); err != nil {
		t.Fatal(err)
	}
	resp.Body.Close()
	if got2.Model != "" {
		t.Fatalf("model = %q, want empty", got2.Model)
	}
	if got2.SysPrompt != "Kamu adalah asisten yang ramah" {
		t.Fatalf("systemPrompt hilang setelah clear model: %q", got2.SysPrompt)
	}
}

func TestAPIAuthRequired(t *testing.T) {
	env := newWebEnv(t)
	setupAuth(t, env, 7, "pw")
	// no auth (wrong pw) -> 401
	for _, path := range []string{
		"/login/7/wrong/api/config",
		"/login/7/wrong/api/config/clear",
		"/login/7/wrong/api/skills/list",
		"/login/7/wrong/api/skills/search",
		"/login/7/wrong/api/skills/install",
		"/login/7/wrong/api/skills/delete",
		"/login/7/wrong/api/memory",
		"/login/7/wrong/api/files/list",
		"/login/7/wrong/api/files/read",
		"/login/7/wrong/api/files/write",
		"/login/7/wrong/api/files/delete",
	} {
		resp, err := http.Post(env.srv.URL+path, "application/json", strings.NewReader("{}"))
		if err != nil {
			t.Fatal(err)
		}
		resp.Body.Close()
		if resp.StatusCode != http.StatusUnauthorized {
			t.Fatalf("%s got %d, want 401", path, resp.StatusCode)
		}
	}
}

func TestSkillListDelete(t *testing.T) {
	env := newWebEnv(t)
	setupAuth(t, env, 99, "skillpw")
	base := env.srv.URL + "/login/99/skillpw/api"

	ctx := context.Background()
	if err := env.vfs.WriteFile(ctx, 99, "skills/pdf/SKILL.md", "---\nname: pdf\ndescription: PDF tools\n---\n# PDF\nbody"); err != nil {
		t.Fatalf("write skill: %v", err)
	}

	resp, err := http.Get(base + "/skills/list")
	if err != nil {
		t.Fatal(err)
	}
	var list struct {
		OK     bool `json:"ok"`
		Skills []struct {
			Name string `json:"name"`
		} `json:"skills"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&list); err != nil {
		t.Fatal(err)
	}
	resp.Body.Close()
	if !list.OK || len(list.Skills) != 1 || list.Skills[0].Name != "pdf" {
		t.Fatalf("unexpected list: %+v", list)
	}

	// delete skill
	body, _ := json.Marshal(map[string]string{"name": "pdf"})
	req, _ := http.NewRequest(http.MethodPost, base+"/skills/delete", bytes.NewReader(body))
	resp, err = http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	var del map[string]bool
	json.NewDecoder(resp.Body).Decode(&del)
	resp.Body.Close()
	if !del["ok"] {
		t.Fatal("delete failed")
	}

	// list empty now
	resp, err = http.Get(base + "/skills/list")
	if err != nil {
		t.Fatal(err)
	}
	var empty struct {
		Skills []any `json:"skills"`
	}
	json.NewDecoder(resp.Body).Decode(&empty)
	resp.Body.Close()
	if len(empty.Skills) != 0 {
		t.Fatalf("skills not empty after delete: %+v", empty)
	}
}

func TestSkillSearchValidation(t *testing.T) {
	env := newWebEnv(t)
	setupAuth(t, env, 5, "pw")
	// empty query -> 400
	resp, err := http.Post(env.srv.URL+"/login/5/pw/api/skills/search", "application/json", strings.NewReader(`{"query":""}`))
	if err != nil {
		t.Fatal(err)
	}
	resp.Body.Close()
	if resp.StatusCode != http.StatusBadRequest {
		t.Fatalf("empty query got %d, want 400", resp.StatusCode)
	}
}

func TestMemoryGetPut(t *testing.T) {
	env := newWebEnv(t)
	setupAuth(t, env, 21, "mempw")
	base := env.srv.URL + "/login/21/mempw/api"

	// initial: not exists
	resp, err := http.Get(base + "/memory")
	if err != nil {
		t.Fatal(err)
	}
	var init struct {
		OK      bool   `json:"ok"`
		Exists  bool   `json:"exists"`
		Content string `json:"content"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&init); err != nil {
		t.Fatal(err)
	}
	resp.Body.Close()
	if !init.OK || init.Exists || init.Content != "" {
		t.Fatalf("unexpected initial memory: %+v", init)
	}

	// save
	body, _ := json.Marshal(map[string]string{"content": "1. User suka kopi\n2. Topik: proyek X"})
	req, _ := http.NewRequest(http.MethodPost, base+"/memory", bytes.NewReader(body))
	resp, err = http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("save got %d, want 200", resp.StatusCode)
	}

	// read back via API
	resp, err = http.Get(base + "/memory")
	if err != nil {
		t.Fatal(err)
	}
	var got struct {
		OK      bool   `json:"ok"`
		Exists  bool   `json:"exists"`
		Content string `json:"content"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&got); err != nil {
		t.Fatal(err)
	}
	resp.Body.Close()
	if !got.Exists || got.Content != "1. User suka kopi\n2. Topik: proyek X" {
		t.Fatalf("memory not saved: %+v", got)
	}

	// empty content = delete (null -> not exists)
	emptyBody, _ := json.Marshal(map[string]string{"content": ""})
	req, _ = http.NewRequest(http.MethodPost, base+"/memory", bytes.NewReader(emptyBody))
	resp, err = http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("clear got %d, want 200", resp.StatusCode)
	}
	resp, err = http.Get(base + "/memory")
	if err != nil {
		t.Fatal(err)
	}
	var after struct {
		Exists bool `json:"exists"`
	}
	json.NewDecoder(resp.Body).Decode(&after)
	resp.Body.Close()
	if after.Exists {
		t.Fatal("memory should be gone after empty save")
	}
}

func TestFilesListReadWriteDelete(t *testing.T) {
	env := newWebEnv(t)
	setupAuth(t, env, 33, "filepw")
	base := env.srv.URL + "/login/33/filepw/api"

	ctx := context.Background()
	if err := env.vfs.WriteFile(ctx, 33, "notes/halo.txt", "isi catatan"); err != nil {
		t.Fatalf("write: %v", err)
	}
	if err := env.vfs.WriteFile(ctx, 33, "skills/pdf/SKILL.md", "---\nname: pdf\n---\n# PDF"); err != nil {
		t.Fatalf("write skill: %v", err)
	}

	// list root
	resp, err := http.Get(base + "/files/list?path=")
	if err != nil {
		t.Fatal(err)
	}
	var root struct {
		OK      bool `json:"ok"`
		Entries []struct {
			Name string `json:"name"`
			Type string `json:"type"`
		} `json:"entries"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&root); err != nil {
		t.Fatal(err)
	}
	resp.Body.Close()
	if !root.OK {
		t.Fatal("list failed")
	}
	names := map[string]string{}
	for _, e := range root.Entries {
		names[e.Name] = e.Type
	}
	if names["notes"] != "dir" || names["skills"] != "dir" {
		t.Fatalf("root entries missing dirs: %+v", names)
	}

	// list subdir
	resp, err = http.Get(base + "/files/list?path=notes")
	if err != nil {
		t.Fatal(err)
	}
	var sub struct {
		Entries []struct {
			Name string `json:"name"`
			Type string `json:"type"`
		} `json:"entries"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&sub); err != nil {
		t.Fatal(err)
	}
	resp.Body.Close()
	if len(sub.Entries) != 1 || sub.Entries[0].Name != "halo.txt" || sub.Entries[0].Type != "file" {
		t.Fatalf("unexpected subdir listing: %+v", sub.Entries)
	}

	// read file
	resp, err = http.Get(base + "/files/read?path=notes/halo.txt")
	if err != nil {
		t.Fatal(err)
	}
	var rd struct {
		OK      bool   `json:"ok"`
		Content string `json:"content"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&rd); err != nil {
		t.Fatal(err)
	}
	resp.Body.Close()
	if !rd.OK || rd.Content != "isi catatan" {
		t.Fatalf("read failed: %+v", rd)
	}

	// read missing -> 404
	resp, err = http.Get(base + "/files/read?path=tak/ada.txt")
	if err != nil {
		t.Fatal(err)
	}
	resp.Body.Close()
	if resp.StatusCode != http.StatusNotFound {
		t.Fatalf("missing file got %d, want 404", resp.StatusCode)
	}

	// write new file
	body, _ := json.Marshal(map[string]string{"path": "notes/baru.txt", "content": "konten baru"})
	req, _ := http.NewRequest(http.MethodPost, base+"/files/write", bytes.NewReader(body))
	resp, err = http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("write got %d, want 200", resp.StatusCode)
	}
	if content, ok := env.vfs.ReadFile(ctx, 33, "notes/baru.txt"); !ok || content != "konten baru" {
		t.Fatalf("file not written via API: ok=%v content=%q", ok, content)
	}

	// delete file
	delBody, _ := json.Marshal(map[string]string{"path": "notes/baru.txt", "type": "file"})
	req, _ = http.NewRequest(http.MethodPost, base+"/files/delete", bytes.NewReader(delBody))
	resp, err = http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("delete got %d, want 200", resp.StatusCode)
	}
	if _, ok := env.vfs.ReadFile(ctx, 33, "notes/baru.txt"); ok {
		t.Fatal("file still exists after delete")
	}

	// delete dir recursively
	delDir, _ := json.Marshal(map[string]string{"path": "skills", "type": "dir"})
	req, _ = http.NewRequest(http.MethodPost, base+"/files/delete", bytes.NewReader(delDir))
	resp, err = http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("delete dir got %d, want 200", resp.StatusCode)
	}
	if _, ok := env.vfs.ReadFile(ctx, 33, "skills/pdf/SKILL.md"); ok {
		t.Fatal("skill file still exists after DeleteDir")
	}
}
