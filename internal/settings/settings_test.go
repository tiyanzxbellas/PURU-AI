package settings

import (
	"encoding/json"
	"testing"

	"github.com/purujawa06-bot/PURU-AI/internal/config"
)

func s(v string) *string { return &v }

func TestEffectiveMerge(t *testing.T) {
	global := config.AIConfig{
		BaseURL:  "https://default.ai",
		APIKey:   "sk-global",
		Model:    "global-model",
		ProxyURL: "https://relay.example.com",
		Headers:  map[string]string{"User-Agent": "global"},
	}

	got := Effective(global, nil)
	if got.BaseURL != global.BaseURL || got.APIKey != global.APIKey || got.Model != global.Model {
		t.Fatalf("nil override must keep global: %+v", got)
	}
	if got.ProxyURL != global.ProxyURL || got.Headers["User-Agent"] != "global" {
		t.Fatalf("nil override must keep proxy/headers: %+v", got)
	}

	got = Effective(global, &Config{APIKey: s("sk-user")})
	if got.BaseURL != global.BaseURL || got.Model != global.Model || got.APIKey != "sk-user" {
		t.Fatalf("partial override wrong: %+v", got)
	}

	got = Effective(global, &Config{BaseURL: s("https://x"), APIKey: s("y"), Model: s("z")})
	if got.BaseURL != "https://x" || got.APIKey != "y" || got.Model != "z" {
		t.Fatalf("full override wrong: %+v", got)
	}
	if got.ProxyURL != global.ProxyURL || got.Headers["User-Agent"] != "global" {
		t.Fatalf("unset proxy/headers must inherit global: %+v", got)
	}

	// Proxy override: explicit "" disables the relay even when global sets one.
	got = Effective(global, &Config{ProxyURL: s("")})
	if got.ProxyURL != "" {
		t.Fatalf("empty proxy override must force direct: %+v", got)
	}
	got = Effective(global, &Config{ProxyURL: s("https://relay2.example.com")})
	if got.ProxyURL != "https://relay2.example.com" {
		t.Fatalf("proxy override not applied: %+v", got)
	}

	// Headers override replaces the whole set when non-nil.
	got = Effective(global, &Config{Headers: map[string]string{"x-opencode-client": "desktop"}})
	if len(got.Headers) != 1 || got.Headers["x-opencode-client"] != "desktop" || got.Headers["User-Agent"] != "" {
		t.Fatalf("headers override wrong: %+v", got.Headers)
	}
}

func TestConfigEmptyAndClone(t *testing.T) {
	c := &Config{APIKey: s("sk"), SystemPrompt: s("role"), ProxyURL: s("https://relay"),
		Headers: map[string]string{"x-opencode-client": "desktop"}}
	if c.IsEmpty() {
		t.Fatal("non-empty config reported empty")
	}
	if (*Config)(nil).IsEmpty() != true {
		t.Fatal("nil config must be empty")
	}

	clone := c.Clone()
	if clone == c || *clone.APIKey != "sk" || *clone.SystemPrompt != "role" {
		t.Fatal("clone must be a deep copy")
	}
	if clone.ProxyURL == nil || *clone.ProxyURL != "https://relay" {
		t.Fatal("clone must copy proxyUrl")
	}
	*clone.APIKey = "changed"
	*clone.SystemPrompt = "changed"
	*clone.ProxyURL = "changed"
	clone.Headers["x-opencode-client"] = "changed"
	if *c.APIKey != "sk" || *c.SystemPrompt != "role" || *c.ProxyURL != "https://relay" {
		t.Fatal("mutating clone changed source")
	}
	if c.Headers["x-opencode-client"] != "desktop" {
		t.Fatal("mutating clone headers changed source map")
	}
}

func TestKeepPromptOnly(t *testing.T) {
	full := &Config{
		BaseURL:      s("https://api.example.com/v1"),
		APIKey:       s("sk-x"),
		Model:        s("gpt-4o"),
		SystemPrompt: s("Kamu adalah asisten yang ramah"),
		ProxyURL:     s("https://relay.example.com"),
		Headers:      map[string]string{"x-opencode-client": "desktop"},
	}
	got := KeepPromptOnly(full)
	if got == nil {
		t.Fatal("KeepPromptOnly(non-nil) returned nil")
	}
	if got.SystemPrompt == nil || *got.SystemPrompt != "Kamu adalah asisten yang ramah" {
		t.Fatalf("systemPrompt harus dipertahankan: %+v", got)
	}
	if got.BaseURL != nil || got.APIKey != nil || got.Model != nil || got.ProxyURL != nil || got.Headers != nil {
		t.Fatalf("field AI connection harus dikosongkan: %+v", got)
	}
	// Source tidak boleh bermutasi.
	if *full.Model != "gpt-4o" || full.SystemPrompt == nil || *full.SystemPrompt != "Kamu adalah asisten yang ramah" {
		t.Fatalf("source termutasi: %+v", full)
	}
	// Mutasi hasil tidak boleh mengubah source.
	*got.SystemPrompt = "changed"
	if *full.SystemPrompt != "Kamu adalah asisten yang ramah" {
		t.Fatal("mutating result changed source")
	}
	if KeepPromptOnly(nil) != nil {
		t.Fatal("KeepPromptOnly(nil) harus nil")
	}
}

func TestConfigJSON(t *testing.T) {
	raw := []byte(`{"model":"m","apiKey":"sk","systemPrompt":"Kamu adalah asisten","proxyUrl":"https://relay","headers":{"x-opencode-client":"desktop","x-opencode-session":"@session"}}`)
	var c Config
	if err := jsonUnmarshal(raw, &c); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if c.Model == nil || *c.Model != "m" || c.APIKey == nil || *c.APIKey != "sk" || c.BaseURL != nil {
		t.Fatalf("bad decode: %+v", c)
	}
	if c.SystemPrompt == nil || *c.SystemPrompt != "Kamu adalah asisten" {
		t.Fatalf("systemPrompt not decoded: %+v", c)
	}
	if c.ProxyURL == nil || *c.ProxyURL != "https://relay" {
		t.Fatalf("proxyUrl not decoded: %+v", c)
	}
	if c.Headers["x-opencode-client"] != "desktop" || c.Headers["x-opencode-session"] != "@session" {
		t.Fatalf("headers not decoded: %+v", c.Headers)
	}
	if b, err := json.Marshal(&Config{}); err != nil || string(b) != "{}" {
		t.Fatalf("empty config should marshal to {}: %s err=%v", b, err)
	}
}
