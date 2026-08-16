// Package providers implements a per-user provider registry mirroring 9router's
// "OpenAI Compatible" nodes: a named endpoint with a unique prefix
// whose available models are read LIVE from GET {baseUrl}/models (no user-edited
// model column). Model references throughout the bot are "prefix/model-id";
// combos and the model default pick from this online catalog.
//
// Providers are stored per chat at providers/{chatID} so they are independent
// of settings/, fs/ and history/ (mirroring 9router's provider nodes).
package providers

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/url"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/purujawa06-bot/PURU-AI/internal/config"
	"github.com/purujawa06-bot/PURU-AI/internal/firebase"
)

// Provider kind (9router compatibility). Only OpenAI-compatible endpoints are
// supported; Anthropic-compatible was removed by request.
const (
	TypeOpenAI       = "openai-compatible"
	APITypeChat      = "chat"
	APITypeResponses = "responses"
)

// The built-in "puru" provider: the server-default gateway exposed as a normal
// provider so it shows up in the model picker and can be used in combos. It is
// not persisted per chat and cannot be deleted/edited.
const (
	BuiltinProviderID = "builtin-puru"
	BuiltinPrefix     = "puru"
)

// Provider is a single OpenAI-compatible endpoint.
type Provider struct {
	ID       string            `json:"id"`
	Name     string            `json:"name"`
	Prefix   string            `json:"prefix"`
	Type     string            `json:"type"`              // openai-compatible
	APIType  string            `json:"apiType,omitempty"` // chat | responses
	BaseURL  string            `json:"baseUrl"`
	APIKey   string            `json:"apiKey,omitempty"`
	Headers  map[string]string `json:"headers,omitempty"`
	ProxyURL string            `json:"proxyUrl,omitempty"`
	// Builtin marks the server-default provider (never stored / deletable).
	Builtin bool `json:"builtin,omitempty"`
	// Model is only set for the built-in provider: the default gateway model
	// (e.g. "puru"). The built-in provider lists exactly this one model instead
	// of fetching the gateway's full /models catalog.
	Model string `json:"model,omitempty"`
}

// Model is one model id returned by a provider's /models endpoint.
type Model struct {
	ID   string `json:"id"`
	Name string `json:"name"`
}

// ModelsResult is the outcome of fetching a provider's model catalog.
type ModelsResult struct {
	OK        bool    `json:"ok"`
	Online    bool    `json:"online"`
	Status    int     `json:"status,omitempty"`
	LatencyMs int64   `json:"latencyMs,omitempty"`
	Error     string  `json:"error,omitempty"`
	Models    []Model `json:"models,omitempty"`
}

// Resolved is the result of resolving a "prefix/model-id" reference.
type Resolved struct {
	Provider Provider
	Model    string
}

// Manager reads/writes providers in RTDB with an in-memory cache and a short
// model-catalog cache.
type Manager struct {
	fb      *firebase.Client
	hc      *http.Client
	builtin *Provider
	mu      sync.Mutex
	cache   map[int64]*cacheEntry
	ttl     time.Duration
	models  map[int64]map[string]*modelsEntry
	mtx     sync.Mutex
}

type cacheEntry struct {
	providers []Provider
	at        time.Time
}

type modelsEntry struct {
	res *ModelsResult
	at  time.Time
}

// New builds a Manager. hc is the shared browser-UA http client (also used by
// main.go for the model gateway); ttl bounds the provider-list cache.
func New(fb *firebase.Client, hc *http.Client, ttl time.Duration) *Manager {
	return &Manager{
		fb:     fb,
		hc:     hc,
		cache:  map[int64]*cacheEntry{},
		ttl:    ttl,
		models: map[int64]map[string]*modelsEntry{},
	}
}

func path(chatID int64) string { return "providers/" + strconv.FormatInt(chatID, 10) }

func nextID() string { return "p" + strconv.FormatInt(time.Now().UnixNano()/1e6, 36) }

// BuiltinProvider builds the built-in "puru" provider from the server's default
// AI config (endpoint + key of the default gateway) so it appears in the model
// picker and can be used in combos. Its ProxyURL is left empty on purpose — it
// inherits the global/per-user proxy relay setting. Its model catalog shows only
// the default gateway model (Model), not the gateway's full /models list.
func BuiltinProvider(cfg config.AIConfig) Provider {
	return Provider{
		ID:      BuiltinProviderID,
		Name:    "PURU Gateway",
		Prefix:  BuiltinPrefix,
		Type:    TypeOpenAI,
		APIType: APITypeChat,
		BaseURL: cfg.BaseURL,
		APIKey:  cfg.APIKey,
		Model:   cfg.Model,
		Builtin: true,
	}
}

// WithBuiltin registers the built-in provider (see BuiltinProvider). The
// built-in provider is merged into List/Get/ByPrefix per chat and is protected
// from Upsert/Delete. It must be called once before serving.
func (m *Manager) WithBuiltin(p Provider) *Manager {
	if p.ID == "" {
		return m
	}
	m.builtin = &p
	return m
}

// List returns the providers for a chat (deep copy), sorted by name, with the
// built-in provider appended.
func (m *Manager) List(ctx context.Context, chatID int64) ([]Provider, error) {
	m.mu.Lock()
	if e, ok := m.cache[chatID]; ok && (m.ttl <= 0 || time.Since(e.at) < m.ttl) {
		m.mu.Unlock()
		return cloneList(e.providers), nil
	}
	m.mu.Unlock()

	raw := m.fb.Get(ctx, path(chatID))
	var list []Provider
	if !isNullJSON(raw) {
		if err := json.Unmarshal(raw, &list); err != nil {
			return nil, err
		}
	}
	list = m.withBuiltin(list)
	sort.SliceStable(list, func(i, j int) bool { return list[i].Name < list[j].Name })
	m.setCache(chatID, list)
	return cloneList(list), nil
}

// Get returns a single provider by id (nil when absent).
func (m *Manager) Get(ctx context.Context, chatID int64, id string) *Provider {
	list, err := m.List(ctx, chatID)
	if err != nil {
		return nil
	}
	for i := range list {
		if list[i].ID == id {
			out := list[i]
			return &out
		}
	}
	return nil
}

// ByPrefix returns the provider whose prefix matches pfx, or nil.
func (m *Manager) ByPrefix(ctx context.Context, chatID int64, pfx string) *Provider {
	list, err := m.List(ctx, chatID)
	if err != nil {
		return nil
	}
	for i := range list {
		if strings.EqualFold(list[i].Prefix, pfx) {
			out := list[i]
			return &out
		}
	}
	return nil
}

// Upsert creates (empty ID) or updates a provider. prefix must be unique and
// the required fields non-empty.
func (m *Manager) Upsert(ctx context.Context, chatID int64, in Provider) (Provider, error) {
	list, _ := m.List(ctx, chatID)
	in.Name = strings.TrimSpace(in.Name)
	in.Prefix = strings.TrimSpace(in.Prefix)
	in.BaseURL = strings.TrimSpace(in.BaseURL)
	in.Type = strings.TrimSpace(in.Type)
	if in.Prefix == "" {
		return Provider{}, errValidation{msg: "Prefix wajib diisi"}
	}
	if in.Name == "" {
		return Provider{}, errValidation{msg: "Name wajib diisi"}
	}
	if in.BaseURL == "" {
		return Provider{}, errValidation{msg: "Base URL wajib diisi"}
	}
	if in.Type == "" {
		in.Type = TypeOpenAI
	}
	if in.Type == TypeOpenAI && in.APIType == "" {
		in.APIType = APITypeChat
	}

	// The built-in prefix is reserved; the built-in provider itself can not be
	// edited through the normal CRUD path.
	if strings.EqualFold(in.Prefix, BuiltinPrefix) {
		return Provider{}, errValidation{msg: "Prefix \"" + in.Prefix + "\" khusus provider built-in"}
	}
	if m.builtin != nil && in.ID == m.builtin.ID {
		return Provider{}, errValidation{msg: "Provider built-in tidak bisa diubah"}
	}

	// prefix uniqueness (case-insensitive), excluding the provider itself.
	for i := range list {
		if list[i].ID != in.ID && strings.EqualFold(list[i].Prefix, in.Prefix) {
			return Provider{}, errValidation{msg: "Prefix \"" + in.Prefix + "\" sudah dipakai provider lain"}
		}
	}

	if in.ID == "" {
		for {
			in.ID = nextID()
			if m.getByID(list, in.ID) == nil {
				break
			}
		}
		list = append(list, in)
	} else {
		i := -1
		for k := range list {
			if list[k].ID == in.ID {
				i = k
				break
			}
		}
		if i < 0 {
			return Provider{}, errValidation{msg: "Provider tidak ditemukan"}
		}
		list[i] = in
	}
	if err := m.save(ctx, chatID, list); err != nil {
		return Provider{}, err
	}
	return in, nil
}

// Delete removes a provider by id, returning (false, nil) when absent. The
// built-in provider is never stored in the list so it can not be removed.
func (m *Manager) Delete(ctx context.Context, chatID int64, id string) (bool, error) {
	if m.builtin != nil && strings.EqualFold(id, m.builtin.ID) {
		return false, nil
	}
	list, _ := m.List(ctx, chatID)
	out := make([]Provider, 0, len(list))
	removed := false
	for _, p := range list {
		if p.ID == id {
			removed = true
			continue
		}
		out = append(out, p)
	}
	if !removed {
		return false, nil
	}
	if err := m.save(ctx, chatID, out); err != nil {
		return false, err
	}
	m.mtx.Lock()
	delete(m.models, chatID)
	m.mtx.Unlock()
	return true, nil
}

// Referencing reports whether deleting the given provider (by prefix) should
// invalidate a model string: "prefix/model-id" or an exact-prefix value.
func Referencing(prefix, model string) bool {
	return strings.HasPrefix(model, prefix+"/")
}

// SplitModelRef splits "prefix/model-id" (first slash). Returns ("", ref) when
// there is no prefix part. A leading slash is ignored.
func SplitModelRef(ref string) (prefix, model string) {
	ref = strings.TrimPrefix(strings.TrimSpace(ref), "/")
	if ref == "" {
		return "", ""
	}
	i := strings.Index(ref, "/")
	if i <= 0 {
		return "", ref
	}
	return ref[:i], ref[i+1:]
}

// Resolve maps a "prefix/model-id" reference to a registered provider. When the
// reference has no prefix, or the prefix is not registered (legacy combo
// entries / plain model ids), it returns nil — callers fall back to the
// settings-based endpoint.
func (m *Manager) Resolve(ctx context.Context, chatID int64, modelRef string) *Resolved {
	prefix, model := SplitModelRef(modelRef)
	if prefix == "" || model == "" {
		return nil
	}
	p := m.ByPrefix(ctx, chatID, prefix)
	if p == nil {
		return nil
	}
	return &Resolved{Provider: *p, Model: model}
}

// Public is the JSON payload sent to the web dashboard: the API key is never
// exposed, only whether one is set.
type Public struct {
	ID       string            `json:"id"`
	Name     string            `json:"name"`
	Prefix   string            `json:"prefix"`
	Type     string            `json:"type"`
	APIType  string            `json:"apiType,omitempty"`
	BaseURL  string            `json:"baseUrl"`
	HasKey   bool              `json:"hasApiKey"`
	Headers  map[string]string `json:"headers,omitempty"`
	ProxyURL string            `json:"proxyUrl,omitempty"`
	Builtin  bool              `json:"builtin,omitempty"`
}

func (p Provider) Public() Public {
	return Public{
		ID:       p.ID,
		Name:     p.Name,
		Prefix:   p.Prefix,
		Type:     p.Type,
		APIType:  p.APIType,
		BaseURL:  p.BaseURL,
		HasKey:   p.APIKey != "",
		Headers:  p.Headers,
		ProxyURL: p.ProxyURL,
		Builtin:  p.Builtin,
	}
}

// PublicList converts a provider slice to its public repr (nil-safe).
func PublicList(list []Provider) []Public {
	out := make([]Public, 0, len(list))
	for _, p := range list {
		out = append(out, p.Public())
	}
	return out
}

func (m *Manager) save(ctx context.Context, chatID int64, list []Provider) error {
	m.setCache(chatID, list)
	m.mtx.Lock()
	delete(m.models, chatID)
	m.mtx.Unlock()
	return m.fb.Put(ctx, path(chatID), list)
}

func (m *Manager) setCache(chatID int64, list []Provider) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.cache[chatID] = &cacheEntry{providers: list, at: time.Now()}
}

func (m *Manager) getByID(list []Provider, id string) *Provider {
	for i := range list {
		if list[i].ID == id {
			return &list[i]
		}
	}
	return nil
}

func cloneList(in []Provider) []Provider {
	out := make([]Provider, len(in))
	for i := range in {
		out[i] = in[i]
		if in[i].Headers != nil {
			out[i].Headers = make(map[string]string, len(in[i].Headers))
			for k, v := range in[i].Headers {
				out[i].Headers[k] = v
			}
		}
	}
	return out
}

// withBuiltin appends the built-in provider to a per-chat list, dropping any
// stored entry that would clash with it (id or prefix) so it appears exactly
// once.
func (m *Manager) withBuiltin(list []Provider) []Provider {
	if m.builtin == nil {
		return list
	}
	out := list[:0]
	for _, p := range list {
		if p.ID == m.builtin.ID || strings.EqualFold(p.Prefix, m.builtin.Prefix) {
			continue
		}
		out = append(out, p)
	}
	return append(out, *m.builtin)
}

// isNullJSON reports whether a raw firebase document is empty or the literal
// JSON null (RTDB returns "null" for absent nodes).
func isNullJSON(raw []byte) bool {
	t := strings.TrimSpace(string(raw))
	return len(t) == 0 || t == "null"
}

// ---------------------------------------------------------------------------
// Live model catalog (GET {baseUrl}/models)
// ---------------------------------------------------------------------------

// ModelCacheTTL bounds how freshly-fetched provider catalogs are reused.
const ModelCacheTTL = 60 * time.Second

// CheckStored fetches the live model catalog for a stored provider. Results are
// cached in-memory for ModelCacheTTL (force bypasses the cache).
func (m *Manager) CheckStored(ctx context.Context, chatID int64, id string, force bool) *ModelsResult {
	p := m.Get(ctx, chatID, id)
	if p == nil {
		return &ModelsResult{OK: false, Online: false, Error: "Provider tidak ditemukan"}
	}
	if !force {
		m.mtx.Lock()
		if e, ok := m.models[chatID][id]; ok && (ModelCacheTTL <= 0 || time.Since(e.at) < ModelCacheTTL) {
			m.mtx.Unlock()
			return e.res
		}
		m.mtx.Unlock()
	}
	res := m.fetch(ctx, *p)
	m.mtx.Lock()
	if m.models[chatID] == nil {
		m.models[chatID] = map[string]*modelsEntry{}
	}
	m.models[chatID][id] = &modelsEntry{res: res, at: time.Now()}
	m.mtx.Unlock()
	return res
}

// CheckInline validates ad-hoc provider values (used by the Add Provider form
// before saving). apiKey may be empty for no-auth endpoints.
func (m *Manager) CheckInline(ctx context.Context, p Provider) *ModelsResult {
	return m.fetch(ctx, p)
}

// modelsEndpoint derives the /models URL from a provider base URL (trailing
// slash stripped).
func modelsEndpoint(p Provider) string {
	base := strings.TrimSuffix(strings.TrimSpace(p.BaseURL), "/")
	return base + "/models"
}

func (m *Manager) fetch(ctx context.Context, p Provider) *ModelsResult {
	start := time.Now()

	// The built-in provider lists exactly its default model ("puru") instead of
	// hitting the gateway's /models endpoint — the catalog is the single default
	// model, always online (the real endpoint is checked at request time).
	if p.Builtin {
		model := strings.TrimSpace(p.Model)
		if model == "" {
			model = BuiltinPrefix
		}
		return &ModelsResult{
			OK:     true,
			Online: true,
			Status: http.StatusOK,
			Models: []Model{{ID: model, Name: model}},
		}
	}

	modelURL := modelsEndpoint(p)
	u, err := url.Parse(modelURL)
	if err != nil || u.Scheme == "" || u.Host == "" {
		return &ModelsResult{OK: false, Online: false, Error: "Base URL tidak valid"}
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodGet, modelURL, nil)
	if err != nil {
		return &ModelsResult{OK: false, Online: false, Error: err.Error()}
	}
	req.Header.Set("Content-Type", "application/json")
	// Provider headers first (may set auth), provider-standard auth after so the
	// provider's own auth wins.
	for k, v := range p.Headers {
		req.Header.Set(k, v)
	}
	if p.APIKey != "" {
		req.Header.Set("Authorization", "Bearer "+p.APIKey)
	}

	// 9router relay: send to the relay root, describe the target via
	// x-relay-target / x-relay-path (protocol identical to proxyFetch.js).
	if p.ProxyURL != "" {
		req.URL, err = url.Parse(p.ProxyURL)
		if err != nil {
			return &ModelsResult{OK: false, Online: false, Error: "Proxy URL tidak valid"}
		}
		req.Header.Set("x-relay-target", u.Scheme+"://"+u.Host)
		req.Header.Set("x-relay-path", u.RequestURI())
	}

	resp, err := m.hc.Do(req)
	latency := time.Since(start).Milliseconds()
	if err != nil {
		return &ModelsResult{OK: false, Online: false, Error: err.Error()}
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(io.LimitReader(resp.Body, 2048))
		msg := strings.TrimSpace(string(body))
		if len(msg) > 256 {
			msg = msg[:256]
		}
		return &ModelsResult{
			OK:        false,
			Online:    false,
			Status:    resp.StatusCode,
			LatencyMs: latency,
			Error:     msg,
		}
	}

	raw, err := io.ReadAll(io.LimitReader(resp.Body, 8<<20))
	if err != nil {
		return &ModelsResult{OK: false, Online: false, Error: err.Error()}
	}

	models := parseModels(raw)
	return &ModelsResult{
		OK:        true,
		Online:    true,
		Status:    http.StatusOK,
		LatencyMs: latency,
		Models:    models,
	}
}

// parseModels parses the common /models response shapes into []Model:
// a plain array, or an object with data[] / models[] (OpenAI style). Items are
// read from id / model / name.
func parseModels(raw []byte) []Model {
	// Fast path: top-level array.
	var arr []json.RawMessage
	if json.Unmarshal(raw, &arr) == nil {
		return pickModels(arr)
	}
	var obj struct {
		Data   []json.RawMessage `json:"data"`
		Models []json.RawMessage `json:"models"`
	}
	if err := json.Unmarshal(raw, &obj); err != nil {
		return nil
	}
	items := obj.Data
	if items == nil {
		items = obj.Models
	}
	return pickModels(items)
}

func pickModels(items []json.RawMessage) []Model {
	out := make([]Model, 0, len(items))
	seen := map[string]bool{}
	for _, it := range items {
		// Some providers return a bare array of id strings.
		var str string
		if json.Unmarshal(it, &str) == nil && str != "" {
			if seen[str] {
				continue
			}
			seen[str] = true
			out = append(out, Model{ID: str, Name: str})
			continue
		}
		var ent struct {
			ID    string `json:"id"`
			Model string `json:"model"`
			Name  string `json:"name"`
		}
		if err := json.Unmarshal(it, &ent); err != nil {
			continue
		}
		id := ent.ID
		if id == "" {
			id = ent.Model
		}
		if id == "" {
			continue
		}
		name := ent.Name
		if name == "" {
			name = id
		}
		if seen[id] {
			continue
		}
		seen[id] = true
		out = append(out, Model{ID: id, Name: name})
	}
	return out
}

type errValidation struct{ msg string }

func (e errValidation) Error() string { return e.msg }
