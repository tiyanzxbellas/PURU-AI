package config

import (
	"fmt"
	"os"
	"strconv"
	"strings"
)

type AIConfig struct {
	BaseURL string
	APIKey  string
	Model   string
	// ProxyURL, when non-empty, routes every OpenAI-compatible request through a
	// 9router-style edge relay (e.g. a Vercel function that forwards to a target
	// via the x-relay-target / x-relay-path headers). Empty = direct connection.
	ProxyURL string
	// Headers are extra HTTP headers sent on every request to the model
	// endpoint (provider-specific, e.g. the x-opencode-* family). Values
	// "@session" / "@request" are replaced per chat/per request when the model
	// client is built.
	Headers map[string]string
}

type Config struct {
	TelegramBotToken    string
	Hostname            string
	Port                int
	PublicBaseURL       string
	PublicRTDB          string
	AI                  AIConfig
	E2BApiKey           string
	E2BDomain           string
	E2BApiURL           string
	Temperature         float64
	MaxLoop             int
	HistoryCacheMax     int
	HistoryCacheTTL     int64
	MemoryUpdateEvery   int
	MemoryMaxChars      int
	GitHubToken         string
	ClawHubToken        string
	SchedulePollSeconds int
	VisionModelURL      string
}

func Load() (*Config, error) {
	required := []struct{ name, val string }{
		{"PUBLIC_RTDB", os.Getenv("PUBLIC_RTDB")},
		{"BOT_TOKEN", os.Getenv("BOT_TOKEN")},
		{"E2B_APIKEY", os.Getenv("E2B_APIKEY")},
	}
	var missing []string
	for _, r := range required {
		if r.val == "" {
			missing = append(missing, r.name)
		}
	}
	if len(missing) > 0 {
		return nil, fmt.Errorf("missing required environment variables:\n  - %s", strings.Join(missing, "\n  - "))
	}

	// Default AI config: HF Space endpoint with puru model.
	const defaultBaseURL = "https://betatestervueui2-b.hf.space/v1"
	const defaultAPIKey = "sk-843e3f05f05eacfe-55n2je-f2c2b844"
	const defaultModel = "puru"
	// Default vision model: Gemini-style endpoint used to summarise user image
	// uploads (see internal/ai.DescribeImage).
	const defaultVisionModelURL = "https://puruboy-api.vercel.app/api/ai/gemini-v3"
	// Built-in Vercel relay (9router-style edge). Requests are routed through it
	// by default; the web dashboard exposes a simple Proxy ON/OFF toggle and the
	// per-user settings.proxyUrl can override/disable it. Change via
	// PROXY_RELAY_URL to use a different edge.
	const defaultRelayURL = "https://vercel-relay-ijhklxg99-rikipurpur98-dotcoms-projects.vercel.app/"

	port, err := envInt("PORT", 3000)
	if err != nil {
		return nil, err
	}
	if port <= 0 || port > 65535 {
		return nil, fmt.Errorf("PORT must be a valid port number (1-65535), got: %q", os.Getenv("PORT"))
	}

	temperature, err := envFloat("TEMPERATURE", 0)
	if err != nil {
		return nil, err
	}
	maxLoop, err := envInt("MAX_LOOP", 20)
	if err != nil {
		return nil, err
	}
	historyCacheMax, err := envInt("HISTORY_CACHE_MAX", 500)
	if err != nil {
		return nil, err
	}
	historyCacheTTL, err := envInt("HISTORY_CACHE_TTL", 600_000)
	if err != nil {
		return nil, err
	}
	memoryUpdateEvery, err := envInt("MEMORY_UPDATE_EVERY", 3)
	if err != nil {
		return nil, err
	}
	memoryMaxChars, err := envInt("MEMORY_MAX_CHARS", 8000)
	if err != nil {
		return nil, err
	}
	schedulePollSeconds, err := envInt("SCHEDULE_POLL_INTERVAL", 15)
	if err != nil {
		return nil, err
	}

	return &Config{
		TelegramBotToken: os.Getenv("BOT_TOKEN"),
		Hostname:         envString("HOSTNAME", "localhost"),
		Port:             port,
		PublicBaseURL:    envString("PUBLIC_BASE_URL", ""),
		PublicRTDB:       os.Getenv("PUBLIC_RTDB"),
		AI: AIConfig{
			BaseURL:  envString("OPENAI_BASEURL", defaultBaseURL),
			APIKey:   envString("OPENAI_APIKEY", defaultAPIKey),
			Model:    envString("OPENAI_MODEL", defaultModel),
			ProxyURL: envString("PROXY_RELAY_URL", defaultRelayURL),
		},
		E2BApiKey:           os.Getenv("E2B_APIKEY"),
		E2BDomain:           os.Getenv("E2B_DOMAIN"),
		E2BApiURL:           os.Getenv("E2B_API_URL"),
		Temperature:         temperature,
		MaxLoop:             maxLoop,
		HistoryCacheMax:     historyCacheMax,
		HistoryCacheTTL:     int64(historyCacheTTL),
		MemoryUpdateEvery:   memoryUpdateEvery,
		MemoryMaxChars:      memoryMaxChars,
		GitHubToken:         os.Getenv("GITHUB_TOKEN"),
		ClawHubToken:        os.Getenv("CLAWHUB_APIKEY"),
		SchedulePollSeconds: schedulePollSeconds,
		VisionModelURL:      envString("VISION_MODEL_URL", defaultVisionModelURL),
	}, nil
}

// PublicBaseURL resolves the externally reachable base URL used to build
// /login links. Resolution order:
//
//  1. Explicit PUBLIC_BASE_URL.
//  2. http://localhost:{PORT} as the default when PUBLIC_BASE_URL is empty.
//
// Never returns "".
func (c *Config) ResolvePublicBaseURL() string {
	if c.PublicBaseURL != "" {
		return strings.TrimRight(c.PublicBaseURL, "/")
	}
	return fmt.Sprintf("http://localhost:%d", c.Port)
}

func envString(key, def string) string {
	if v := os.Getenv(key); v != "" {
		return v
	}
	return def
}

func envInt(key string, def int) (int, error) {
	raw := os.Getenv(key)
	if raw == "" {
		return def, nil
	}
	v, err := strconv.Atoi(raw)
	if err != nil {
		return 0, fmt.Errorf("%s must be a valid number, got: %q", key, raw)
	}
	return v, nil
}

func envFloat(key string, def float64) (float64, error) {
	raw := os.Getenv(key)
	if raw == "" {
		return def, nil
	}
	v, err := strconv.ParseFloat(raw, 64)
	if err != nil {
		return 0, fmt.Errorf("%s must be a valid number, got: %q", key, raw)
	}
	return v, nil
}
