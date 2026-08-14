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
}

type Config struct {
	TelegramBotToken  string
	Hostname          string
	Port              int
	PublicBaseURL     string
	PublicRTDB        string
	AI                AIConfig
	E2BApiKey         string
	E2BDomain         string
	E2BApiURL         string
	Temperature       float64
	MaxLoop           int
	HistoryCacheMax   int
	HistoryCacheTTL   int64
	MemoryUpdateEvery int
	MemoryMaxChars    int
	GitHubToken       string
	ClawHubToken      string
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

	return &Config{
		TelegramBotToken: os.Getenv("BOT_TOKEN"),
		Hostname:         envString("HOSTNAME", "localhost"),
		Port:             port,
		PublicBaseURL:    envString("PUBLIC_BASE_URL", ""),
		PublicRTDB:       os.Getenv("PUBLIC_RTDB"),
		AI: AIConfig{
			BaseURL: envString("OPENAI_BASEURL", defaultBaseURL),
			APIKey:  envString("OPENAI_APIKEY", defaultAPIKey),
			Model:   envString("OPENAI_MODEL", defaultModel),
		},
		E2BApiKey:         os.Getenv("E2B_APIKEY"),
		E2BDomain:         os.Getenv("E2B_DOMAIN"),
		E2BApiURL:         os.Getenv("E2B_API_URL"),
		Temperature:       temperature,
		MaxLoop:           maxLoop,
		HistoryCacheMax:   historyCacheMax,
		HistoryCacheTTL:   int64(historyCacheTTL),
		MemoryUpdateEvery: memoryUpdateEvery,
		MemoryMaxChars:    memoryMaxChars,
		GitHubToken:       os.Getenv("GITHUB_TOKEN"),
		ClawHubToken:      os.Getenv("CLAWHUB_APIKEY"),
	}, nil
}

// PublicBaseURL resolves the externally reachable base URL used to build
// /login links. Resolution order:
//
//  1. Explicit PUBLIC_BASE_URL.
//  2. Known PaaS-provided URLs (Render, Koyeb, Railway, Fly.io, Heroku) so
//     /login works out of the box without setting PUBLIC_BASE_URL.
//  3. Fallback http://{HOSTNAME}:{PORT} when HOSTNAME is a real address.
//  4. http://localhost:{PORT} as the final default when PUBLIC_BASE_URL is
//     not set (bind-all HOSTNAME values like 0.0.0.0 are not usable as a
//     public host, so it defaults to localhost instead of an empty link).
//
// Never returns "".
func (c *Config) ResolvePublicBaseURL() string {
	if c.PublicBaseURL != "" {
		return strings.TrimRight(c.PublicBaseURL, "/")
	}
	if v := os.Getenv("RENDER_EXTERNAL_URL"); v != "" {
		return strings.TrimRight(v, "/")
	}
	if v := os.Getenv("KOYEB_PUBLIC_DOMAIN"); v != "" {
		return "https://" + v
	}
	if v := os.Getenv("KOYEB_SERVICE_DOMAIN"); v != "" {
		return "https://" + v
	}
	if v := os.Getenv("KOYEB_APP_DOMAIN"); v != "" {
		return "https://" + v
	}
	if v := os.Getenv("RAILWAY_PUBLIC_DOMAIN"); v != "" {
		return "https://" + v
	}
	if v := os.Getenv("FLY_APP_NAME"); v != "" {
		return "https://" + v + ".fly.dev"
	}
	if v := os.Getenv("HEROKU_APP_NAME"); v != "" {
		return "https://" + v + ".herokuapp.com"
	}
	switch c.Hostname {
	case "", "0.0.0.0", "::", "[::]":
		// Hostname bind-all tidak bisa dipakai sebagai host publik; default
		// ke localhost supaya /login selalu menghasilkan link yang sah.
		return fmt.Sprintf("http://localhost:%d", c.Port)
	}
	return fmt.Sprintf("http://%s:%d", c.Hostname, c.Port)
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
