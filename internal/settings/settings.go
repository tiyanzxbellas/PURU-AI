// Package settings persists per-user AI API configuration in Firebase RTDB.
// Each user may override any subset of the global AI settings (base URL, API
// key, model); empty fields inherit the server-wide default. Stored at
// settings/{chatID} (numeric key) so it is independent of the vfs and history
// trees — /reset chat and /reset memory must not wipe it.
package settings

import (
	"context"
	"encoding/json"
	"strconv"
	"sync"
	"time"

	"github.com/purujawa06-bot/PURU-AI/internal/config"
	"github.com/purujawa06-bot/PURU-AI/internal/firebase"
)

// Config is a per-user partial override of the AI settings. A nil field means
// "inherit the server default"; omitempty keeps unset fields from cluttering
// the RTDB document.
type Config struct {
	BaseURL *string `json:"baseUrl,omitempty"`
	APIKey  *string `json:"apiKey,omitempty"`
	Model   *string `json:"model,omitempty"`
	// SystemPrompt is an optional custom role/instructions appended to the
	// system prompt for this user (managed via the /login web page).
	SystemPrompt *string `json:"systemPrompt,omitempty"`
}

// Effective merges the server-wide AI config with a user override. Non-nil
// override fields win; anything empty is inherited from global.
func Effective(global config.AIConfig, user *Config) config.AIConfig {
	if user == nil {
		return global
	}
	if user.BaseURL != nil {
		global.BaseURL = *user.BaseURL
	}
	if user.APIKey != nil {
		global.APIKey = *user.APIKey
	}
	if user.Model != nil {
		global.Model = *user.Model
	}
	return global
}

// IsEmpty reports whether none of the fields are set (i.e. the user is fully
// on the server default).
func (c *Config) IsEmpty() bool {
	return c == nil || (c.BaseURL == nil && c.APIKey == nil && c.Model == nil && c.SystemPrompt == nil)
}

// Clone returns a deep copy so callers can mutate freely.
func (c *Config) Clone() *Config {
	if c == nil {
		return nil
	}
	out := &Config{}
	if c.BaseURL != nil {
		v := *c.BaseURL
		out.BaseURL = &v
	}
	if c.APIKey != nil {
		v := *c.APIKey
		out.APIKey = &v
	}
	if c.Model != nil {
		v := *c.Model
		out.Model = &v
	}
	if c.SystemPrompt != nil {
		v := *c.SystemPrompt
		out.SystemPrompt = &v
	}
	return out
}

// Manager reads and writes per-user AI settings. Reads are cached briefly in
// memory so a GET against RTDB is not needed for every message; a fetched
// config is a copy and two parallel requests from the same user never happen
// (the app layer busy-guards that).
type Manager struct {
	fb    *firebase.Client
	mu    sync.Mutex
	cache map[int64]*cacheEntry
	ttl   time.Duration
}

type cacheEntry struct {
	cfg *Config
	at  time.Time
}

func New(fb *firebase.Client, ttl time.Duration) *Manager {
	return &Manager{fb: fb, cache: map[int64]*cacheEntry{}, ttl: ttl}
}

func path(chatID int64) string { return "settings/" + idKey(chatID) }

func idKey(chatID int64) string { return strconv.FormatInt(chatID, 10) }

// jsonUnmarshal decodes a document, tolerating a plain null body.
func jsonUnmarshal(raw []byte, v any) error {
	return json.Unmarshal(raw, v)
}

// Get returns the stored override for the chat, or nil when unset.
func (m *Manager) Get(ctx context.Context, chatID int64) *Config {
	c := m.cached(chatID)
	if c != nil {
		return c.Clone()
	}
	raw := m.fb.Get(ctx, path(chatID))
	if len(raw) == 0 {
		m.setCache(chatID, nil)
		return nil
	}
	var cfg Config
	if err := jsonUnmarshal(raw, &cfg); err != nil {
		m.setCache(chatID, nil)
		return nil
	}
	if cfg.IsEmpty() {
		m.setCache(chatID, nil)
		return nil
	}
	m.setCache(chatID, &cfg)
	return cfg.Clone()
}

// Set stores (or completely replaces) the override for the chat.
func (m *Manager) Set(ctx context.Context, chatID int64, cfg *Config) error {
	if cfg == nil {
		return nil
	}
	if err := m.fb.Put(ctx, path(chatID), cfg); err != nil {
		return err
	}
	m.setCache(chatID, cfg.Clone())
	return nil
}

// ClearField removes a single field of the override. When nothing is left the
// whole document is deleted.
func (m *Manager) ClearField(ctx context.Context, chatID int64, field string) error {
	cfg := m.Get(ctx, chatID)
	if cfg == nil {
		return nil
	}
	switch field {
	case "api", "key", "apiKey":
		cfg.APIKey = nil
	case "model":
		cfg.Model = nil
	case "base", "baseUrl", "base_url":
		cfg.BaseURL = nil
	case "prompt", "systemPrompt", "system_prompt", "role":
		cfg.SystemPrompt = nil
	default:
		return nil
	}
	if cfg.IsEmpty() {
		return m.Delete(ctx, chatID)
	}
	return m.Set(ctx, chatID, cfg)
}

// Delete removes the override, reverting the user to the server default.
func (m *Manager) Delete(ctx context.Context, chatID int64) error {
	m.delCache(chatID)
	return m.fb.Delete(ctx, path(chatID))
}

// ---------------------------------------------------------------------------
// cache helpers
// ---------------------------------------------------------------------------

func (m *Manager) cached(chatID int64) *Config {
	m.mu.Lock()
	defer m.mu.Unlock()
	e, ok := m.cache[chatID]
	if !ok {
		return nil
	}
	if m.ttl > 0 && time.Since(e.at) > m.ttl {
		delete(m.cache, chatID)
		return nil
	}
	return e.cfg
}

func (m *Manager) setCache(chatID int64, cfg *Config) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.cache[chatID] = &cacheEntry{cfg: cfg, at: time.Now()}
}

func (m *Manager) delCache(chatID int64) {
	m.mu.Lock()
	defer m.mu.Unlock()
	delete(m.cache, chatID)
}
