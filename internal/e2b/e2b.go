// Package e2b is a Go port of the parts of the @e2b/code-interpreter (and the
// base "e2b" SDK) the bot uses. Everything is plain HTTP:
//   - control plane: POST/DELETE https://api.<domain>/sandboxes (X-API-KEY)
//   - run code:      POST https://49999-<sandboxID>.<domain>/execute (NDJSON)
//   - files:         GET/POST https://49983-<sandboxID>.<domain>/files
//
// Runtime call headers: X-Access-Token (envd) + E2B-Traffic-Access-Token.
package e2b

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"strings"
	"sync"
	"time"
)

const (
	defaultDomain   = "e2b.app"
	defaultTemplate = "code-interpreter-v1"
	jupyterPort     = 49999
	envdPort        = 49983
	sandboxTTL      = 5 * time.Minute
	requestTimeout  = 70 * time.Second
	executeBodyMax  = 8 << 20
)

type sandboxInfo struct {
	sandboxID          string
	domain             string
	envdAccessToken    string
	trafficAccessToken string
	createdAt          time.Time
}

type Manager struct {
	apiKey string
	domain string
	apiURL string
	httpx  *http.Client

	mu    sync.Mutex
	boxes map[int64]*sandboxInfo
}

func NewManager(apiKey, envDomain string, hc *http.Client) *Manager {
	domain := defaultDomain
	if envDomain != "" {
		domain = envDomain
	}
	apiURL := "https://api." + domain
	if u := strings.TrimSpace(getEnv("E2B_API_URL")); u != "" {
		apiURL = strings.TrimSuffix(u, "/")
	}
	if hc == nil {
		hc = &http.Client{Timeout: requestTimeout}
	}
	return &Manager{apiKey: apiKey, domain: domain, apiURL: apiURL, httpx: hc, boxes: map[int64]*sandboxInfo{}}
}

// getSandbox returns the active box for the chat, killing it first when idle
// longer than the TTL.
func (m *Manager) getSandbox(chatID int64) *sandboxInfo {
	m.mu.Lock()
	defer m.mu.Unlock()
	box := m.boxes[chatID]
	if box == nil {
		return nil
	}
	if time.Since(box.createdAt) > sandboxTTL {
		m.killLocked(chatID)
		return nil
	}
	return box
}

func (m *Manager) drop(chatID int64) {
	m.mu.Lock()
	defer m.mu.Unlock()
	delete(m.boxes, chatID)
}

func (m *Manager) killLocked(chatID int64) {
	box := m.boxes[chatID]
	if box != nil {
		ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
		_, _, _ = m.doJSON(ctx, http.MethodDelete, m.apiURL, "/sandboxes/"+box.sandboxID, nil, nil, nil)
		cancel()
	}
	delete(m.boxes, chatID)
}

func (m *Manager) doJSON(ctx context.Context, method, baseURL, path string, query url.Values, body any, headers map[string]string) ([]byte, int, error) {
	var reader io.Reader
	if body != nil {
		b, err := json.Marshal(body)
		if err != nil {
			return nil, 0, err
		}
		reader = bytes.NewReader(b)
	}
	u := baseURL + path
	if len(query) > 0 {
		u += "?" + query.Encode()
	}
	req, err := http.NewRequestWithContext(ctx, method, u, reader)
	if err != nil {
		return nil, 0, err
	}
	req.Header.Set("X-API-KEY", m.apiKey)
	req.Header.Set("User-Agent", "puru-ai/1.0")
	for k, v := range headers {
		req.Header.Set(k, v)
	}
	if body != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	resp, err := m.httpx.Do(req)
	if err != nil {
		return nil, 0, err
	}
	defer resp.Body.Close()
	data, err := io.ReadAll(io.LimitReader(resp.Body, 5<<20))
	if err != nil {
		return nil, resp.StatusCode, err
	}
	return data, resp.StatusCode, nil
}

func (m *Manager) runtimeHost(box *sandboxInfo, port int) string {
	return fmt.Sprintf("https://%d-%s.%s", port, box.sandboxID, box.domain)
}

func (m *Manager) runtimeHeaders(box *sandboxInfo) map[string]string {
	h := map[string]string{"X-Access-Token": box.envdAccessToken}
	if box.trafficAccessToken != "" {
		h["E2B-Traffic-Access-Token"] = box.trafficAccessToken
	}
	return h
}

// ---------------------------------------------------------------------------
// Sandbox lifecycle
// ---------------------------------------------------------------------------

func (m *Manager) CreateSandbox(ctx context.Context, chatID int64) (string, error) {
	if existing := m.getSandbox(chatID); existing != nil {
		return existing.sandboxID, nil
	}
	payload := map[string]any{
		"templateID":            templateID(),
		"timeout":               300,
		"secure":                true,
		"allow_internet_access": true,
		"autoPause":             false,
		"autoResume":            map[string]any{"enabled": false},
		"metadata":              map[string]any{"app": "puru-ai"},
	}
	data, status, err := m.doJSON(ctx, http.MethodPost, m.apiURL, "/sandboxes", nil, payload, nil)
	if err != nil {
		return "", err
	}
	if status >= 400 {
		return "", fmt.Errorf("e2b create sandbox: HTTP %d: %s", status, truncate(string(data), 200))
	}
	var out struct {
		SandboxID          string `json:"sandboxID"`
		Domain             string `json:"domain"`
		EnvdAccessToken    string `json:"envdAccessToken"`
		TrafficAccessToken string `json:"trafficAccessToken"`
	}
	if err := json.Unmarshal(data, &out); err != nil {
		return "", err
	}
	boxDomain := out.Domain
	if boxDomain == "" {
		boxDomain = m.domain
	}
	box := &sandboxInfo{
		sandboxID:          out.SandboxID,
		domain:             boxDomain,
		envdAccessToken:    out.EnvdAccessToken,
		trafficAccessToken: out.TrafficAccessToken,
		createdAt:          time.Now(),
	}
	m.mu.Lock()
	m.boxes[chatID] = box
	m.mu.Unlock()
	return box.sandboxID, nil
}

func (m *Manager) KillSandbox(chatID int64) bool {
	m.mu.Lock()
	box := m.boxes[chatID]
	if box == nil {
		m.mu.Unlock()
		return false
	}
	m.killLocked(chatID)
	m.mu.Unlock()
	return true
}

// ---------------------------------------------------------------------------
// Code execution (jupyter-compatible /execute, NDJSON response)
// ---------------------------------------------------------------------------

type ExecutionResult struct {
	Text     string
	Stdout   []string
	Stderr   []string
	ErrorMsg string
	ErrorVal string
}

func (r *ExecutionResult) Logs() ([]string, []string) { return r.Stdout, r.Stderr }

func (r *ExecutionResult) Error() string {
	if r.ErrorMsg == "" {
		return ""
	}
	if r.ErrorVal != "" {
		return r.ErrorMsg + ": " + r.ErrorVal
	}
	return r.ErrorMsg
}

// normalizeLanguage maps model-friendly language names to the kernel names the
// E2B code-interpreter /execute endpoint actually accepts. The endpoint only
// ships "python" and "javascript" kernels; aliases like "node"/"nodejs"/"js"
// previously produced HTTP 500 (no such kernel), which also wrongly dropped the
// still-alive sandbox.
func normalizeLanguage(lang string) string {
	switch strings.ToLower(strings.TrimSpace(lang)) {
	case "node", "nodejs", "js", "javascript":
		return "javascript"
	case "python", "py", "python3":
		return "python"
	}
	return lang
}

// wrapJavaScript isolates every javascript execution inside a block statement.
// The E2B javascript kernel persists global scope across /execute calls (unlike
// its python kernel where reassignment is legal), so re-running a file that
// does `const chalk = require(...)` blows up with `SyntaxError: Identifier 'x'
// has already been declared` and the agent loops forever. A bare block `{}`
// scopes `const`/`let` to that block (they would otherwise land in the kernel's
// persistent global scope) without adding a top-level expression — an IIFE
// (…)() broke the kernel's result extraction and its first call.
func wrapJavaScript(code string) string {
	return "{\n" + code + "\n}\n"
}

func (m *Manager) RunCode(ctx context.Context, chatID int64, code, language string) (*ExecutionResult, error) {
	box := m.getSandbox(chatID)
	if box == nil {
		return nil, fmt.Errorf("No active sandbox. Create one first with e2b_sandbox_create.")
	}
	lang := normalizeLanguage(language)
	if lang == "javascript" {
		code = wrapJavaScript(code)
	}
	host := m.runtimeHost(box, jupyterPort)
	payload := map[string]any{"code": code, "language": lang}
	data, status, err := m.doJSON(ctx, http.MethodPost, host, "/execute", nil, payload, m.runtimeHeaders(box))
	if err != nil {
		m.drop(chatID)
		return nil, fmt.Errorf("Sandbox mati atau timeout. Buat ulang dengan e2b_sandbox_create.")
	}
	if status >= 400 {
		// 502 = gateway timeout, sandbox benar-benar mati → buang referensi.
		// 4xx/5xx lain (mis. language kernel tidak dikenali) adalah masalah
		// request, bukan kondisi sandbox → sandbox tetap hidup dan bisa dipakai
		// ulang tanpa membuat instans baru.
		if status == 502 {
			m.drop(chatID)
			return nil, fmt.Errorf("Sandbox timeout")
		}
		return nil, fmt.Errorf("e2b execute: HTTP %d", status)
	}
	return parseExecution(data)
}

// InstallPackage installs a package in the sandbox (jupyter magic command).
func (m *Manager) InstallPackage(ctx context.Context, chatID int64, pkg, manager string) *ExecutionResult {
	cmd := "!pip install " + pkg
	if manager == "npm" {
		cmd = "!npm install " + pkg
	}
	res, err := m.RunCode(ctx, chatID, cmd, "python")
	if err != nil {
		return &ExecutionResult{ErrorMsg: err.Error()}
	}
	if res.Error() != "" {
		return res
	}
	return res
}

func parseExecution(data []byte) (*ExecutionResult, error) {
	res := &ExecutionResult{}
	scanner := bufio.NewScanner(bytes.NewReader(data))
	scanner.Buffer(make([]byte, 64*1024), executeBodyMax)
	for scanner.Scan() {
		line := strings.TrimSpace(scanner.Text())
		if line == "" {
			continue
		}
		var msg struct {
			Type         string `json:"type"`
			Text         string `json:"text"`
			Name         string `json:"name"`
			Value        string `json:"value"`
			IsMainResult bool   `json:"is_main_result"`
		}
		if err := json.Unmarshal([]byte(line), &msg); err != nil {
			continue
		}
		switch msg.Type {
		case "stdout":
			res.Stdout = append(res.Stdout, msg.Text)
		case "stderr":
			res.Stderr = append(res.Stderr, msg.Text)
		case "error":
			res.ErrorMsg = msg.Name
			res.ErrorVal = msg.Value
		case "result":
			if msg.IsMainResult && res.Text == "" {
				res.Text = msg.Text
			}
		}
	}
	if err := scanner.Err(); err != nil {
		return nil, err
	}
	return res, nil
}

// ---------------------------------------------------------------------------
// Filesystem
// ---------------------------------------------------------------------------

func (m *Manager) ReadFile(ctx context.Context, chatID int64, path string) ([]byte, error) {
	box := m.getSandbox(chatID)
	if box == nil {
		return nil, fmt.Errorf("No active sandbox. Create one first with e2b_sandbox_create.")
	}
	host := m.runtimeHost(box, envdPort)
	q := url.Values{"path": {path}}
	data, status, err := m.doJSON(ctx, http.MethodGet, host, "/files", q, nil, m.runtimeHeaders(box))
	if err != nil {
		m.drop(chatID)
		return nil, fmt.Errorf("Sandbox mati atau timeout. Buat ulang dengan e2b_sandbox_create.")
	}
	if status >= 400 {
		return nil, fmt.Errorf("e2b read file: HTTP %d", status)
	}
	return data, nil
}

func (m *Manager) WriteFile(ctx context.Context, chatID int64, path, content string) error {
	box := m.getSandbox(chatID)
	if box == nil {
		return fmt.Errorf("No active sandbox. Create one first with e2b_sandbox_create.")
	}
	host := m.runtimeHost(box, envdPort)
	q := url.Values{"path": {path}}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, host+"/files?"+q.Encode(), bytes.NewReader([]byte(content)))
	if err != nil {
		return err
	}
	req.Header.Set("Content-Type", "application/octet-stream")
	req.Header.Set("X-Access-Token", box.envdAccessToken)
	if box.trafficAccessToken != "" {
		req.Header.Set("E2B-Traffic-Access-Token", box.trafficAccessToken)
	}
	resp, err := m.httpx.Do(req)
	if err != nil {
		m.drop(chatID)
		return fmt.Errorf("Sandbox mati atau timeout. Buat ulang dengan e2b_sandbox_create.")
	}
	defer resp.Body.Close()
	io.Copy(io.Discard, resp.Body)
	if resp.StatusCode >= 400 {
		return fmt.Errorf("e2b write file: HTTP %d", resp.StatusCode)
	}
	return nil
}

func getEnv(key string) string {
	return strings.TrimSpace(os.Getenv(key))
}

// templateID returns the E2B sandbox template used for code interpretation,
// overridable via the E2B_TEMPLATE environment variable. The "default"
// template was removed from the E2B platform; the code-interpreter SDK now
// ships code-interpreter-v1 as its default template.
func templateID() string {
	if t := getEnv("E2B_TEMPLATE"); t != "" {
		return t
	}
	return defaultTemplate
}

func truncate(s string, n int) string {
	if len(s) > n {
		return s[:n]
	}
	return s
}
