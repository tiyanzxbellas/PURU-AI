package config

import "testing"

var platformEnvKeys = []string{
	"RENDER_EXTERNAL_URL",
	"KOYEB_PUBLIC_DOMAIN",
	"KOYEB_SERVICE_DOMAIN",
	"KOYEB_APP_DOMAIN",
	"RAILWAY_PUBLIC_DOMAIN",
	"FLY_APP_NAME",
	"HEROKU_APP_NAME",
}

func TestPublicBaseURL(t *testing.T) {
	cases := []struct {
		name     string
		cfg      Config
		setenv   map[string]string
		expected string
	}{
		{"explicit beats everything", Config{PublicBaseURL: "https://bot.example.com/", Hostname: "0.0.0.0", Port: 3000}, map[string]string{"RENDER_EXTERNAL_URL": "https://puru.onrender.com"}, "https://bot.example.com"},
		{"platform env ignored, render", Config{Hostname: "0.0.0.0", Port: 3000}, map[string]string{"RENDER_EXTERNAL_URL": "https://puru.onrender.com"}, "http://localhost:3000"},
		{"platform env ignored, koyeb public domain", Config{Hostname: "0.0.0.0", Port: 3000}, map[string]string{"KOYEB_PUBLIC_DOMAIN": "puru.koyeb.app"}, "http://localhost:3000"},
		{"platform env ignored, koyeb service domain", Config{Hostname: "0.0.0.0", Port: 3000}, map[string]string{"KOYEB_SERVICE_DOMAIN": "puru-svc.koyeb.app"}, "http://localhost:3000"},
		{"platform env ignored, railway", Config{Hostname: "0.0.0.0", Port: 3000}, map[string]string{"RAILWAY_PUBLIC_DOMAIN": "puru.up.railway.app"}, "http://localhost:3000"},
		{"platform env ignored, fly", Config{Hostname: "0.0.0.0", Port: 3000}, map[string]string{"FLY_APP_NAME": "puru"}, "http://localhost:3000"},
		{"platform env ignored, heroku", Config{Hostname: "0.0.0.0", Port: 3000}, map[string]string{"HEROKU_APP_NAME": "puru"}, "http://localhost:3000"},
		{"bind-all host defaults to localhost", Config{Hostname: "0.0.0.0", Port: 3000}, nil, "http://localhost:3000"},
		{"ipv6 bind-all host defaults to localhost", Config{Hostname: "::", Port: 3000}, nil, "http://localhost:3000"},
		{"empty host defaults to localhost", Config{Hostname: "", Port: 3000}, nil, "http://localhost:3000"},
		{"localhost host", Config{Hostname: "localhost", Port: 3000}, nil, "http://localhost:3000"},
		{"real host ignored, defaults to localhost", Config{Hostname: "puru.local", Port: 8080}, nil, "http://localhost:8080"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			for _, k := range platformEnvKeys {
				t.Setenv(k, "")
			}
			for k, v := range tc.setenv {
				t.Setenv(k, v)
			}
			if got := tc.cfg.ResolvePublicBaseURL(); got != tc.expected {
				t.Errorf("ResolvePublicBaseURL() = %q, want %q", got, tc.expected)
			}
		})
	}
}
