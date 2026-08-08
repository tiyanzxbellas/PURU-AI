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
}

func Load() (*Config, error) {
	required := []struct{ name, val string }{
		{"PUBLIC_RTDB", os.Getenv("PUBLIC_RTDB")},
		{"BOT_TOKEN", os.Getenv("BOT_TOKEN")},
		{"E2B_APIKEY", os.Getenv("E2B_APIKEY")},
		{"OPENAI_BASEURL", os.Getenv("OPENAI_BASEURL")},
		{"OPENAI_APIKEY", os.Getenv("OPENAI_APIKEY")},
		{"OPENAI_MODEL", os.Getenv("OPENAI_MODEL")},
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
		TelegramBotToken:  os.Getenv("BOT_TOKEN"),
		Hostname:          envString("HOSTNAME", "localhost"),
		Port:              port,
		PublicRTDB:        os.Getenv("PUBLIC_RTDB"),
		AI:                AIConfig{BaseURL: os.Getenv("OPENAI_BASEURL"), APIKey: os.Getenv("OPENAI_APIKEY"), Model: os.Getenv("OPENAI_MODEL")},
		E2BApiKey:         os.Getenv("E2B_APIKEY"),
		E2BDomain:         os.Getenv("E2B_DOMAIN"),
		E2BApiURL:         os.Getenv("E2B_API_URL"),
		Temperature:       temperature,
		MaxLoop:           maxLoop,
		HistoryCacheMax:   historyCacheMax,
		HistoryCacheTTL:   int64(historyCacheTTL),
		MemoryUpdateEvery: memoryUpdateEvery,
		MemoryMaxChars:    memoryMaxChars,
	}, nil
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
