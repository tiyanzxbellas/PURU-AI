package settings

import (
	"encoding/json"
	"testing"

	"github.com/purujawa06-bot/PURU-AI/internal/config"
)

func s(v string) *string { return &v }

func TestEffectiveMerge(t *testing.T) {
	global := config.AIConfig{BaseURL: "https://default.ai", APIKey: "sk-global", Model: "global-model"}

	got := Effective(global, nil)
	if got != global {
		t.Fatalf("nil override must keep global: %+v", got)
	}

	got = Effective(global, &Config{APIKey: s("sk-user")})
	if got.BaseURL != global.BaseURL || got.Model != global.Model || got.APIKey != "sk-user" {
		t.Fatalf("partial override wrong: %+v", got)
	}

	got = Effective(global, &Config{BaseURL: s("https://x"), APIKey: s("y"), Model: s("z")})
	if got != (config.AIConfig{BaseURL: "https://x", APIKey: "y", Model: "z"}) {
		t.Fatalf("full override wrong: %+v", got)
	}
}

func TestConfigEmptyAndClone(t *testing.T) {
	c := &Config{APIKey: s("sk")}
	if c.IsEmpty() {
		t.Fatal("non-empty config reported empty")
	}
	if (*Config)(nil).IsEmpty() != true {
		t.Fatal("nil config must be empty")
	}

	clone := c.Clone()
	if clone == c || *clone.APIKey != "sk" {
		t.Fatal("clone must be a deep copy")
	}
	*clone.APIKey = "changed"
	if *c.APIKey != "sk" {
		t.Fatal("mutating clone changed source")
	}
}

func TestConfigJSON(t *testing.T) {
	raw := []byte(`{"model":"m","apiKey":"sk"}`)
	var c Config
	if err := jsonUnmarshal(raw, &c); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if c.Model == nil || *c.Model != "m" || c.APIKey == nil || *c.APIKey != "sk" || c.BaseURL != nil {
		t.Fatalf("bad decode: %+v", c)
	}
	if b, err := json.Marshal(&Config{}); err != nil || string(b) != "{}" {
		t.Fatalf("empty config should marshal to {}: %s err=%v", b, err)
	}
}
