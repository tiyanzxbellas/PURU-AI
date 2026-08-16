// main wires everything together: environment, health server, Telegram
// long-polling loop with conflict retry, and the update handler.
package main

import (
	"context"
	"errors"
	"io"
	"log"
	"net/http"
	"os"
	"time"

	"github.com/joho/godotenv"
	"github.com/tmc/langchaingo/llms"

	"github.com/purujawa06-bot/PURU-AI/internal/ai"
	"github.com/purujawa06-bot/PURU-AI/internal/app"
	"github.com/purujawa06-bot/PURU-AI/internal/auth"
	"github.com/purujawa06-bot/PURU-AI/internal/combos"
	"github.com/purujawa06-bot/PURU-AI/internal/config"
	"github.com/purujawa06-bot/PURU-AI/internal/e2b"
	"github.com/purujawa06-bot/PURU-AI/internal/firebase"
	"github.com/purujawa06-bot/PURU-AI/internal/history"
	"github.com/purujawa06-bot/PURU-AI/internal/memory"
	"github.com/purujawa06-bot/PURU-AI/internal/providers"
	"github.com/purujawa06-bot/PURU-AI/internal/scheduler"
	"github.com/purujawa06-bot/PURU-AI/internal/servelog"
	"github.com/purujawa06-bot/PURU-AI/internal/settings"
	"github.com/purujawa06-bot/PURU-AI/internal/skills"
	"github.com/purujawa06-bot/PURU-AI/internal/telegram"
	"github.com/purujawa06-bot/PURU-AI/internal/usage"
	"github.com/purujawa06-bot/PURU-AI/internal/vfs"
	"github.com/purujawa06-bot/PURU-AI/internal/web"
)

func main() {
	_ = godotenv.Load()

	cfg, err := config.Load()
	if err != nil {
		log.Fatalf("config: %v", err)
	}

	// Server log buffer: the standard log package writes to both stderr and an
	// in-memory ring so the web dashboard can stream a live console tail.
	serverLog := servelog.New(1000)
	log.SetOutput(io.MultiWriter(os.Stderr, serverLog))

	// Shared HTTP client. A browser-like User-Agent is injected on outbound
	// requests so sites that block non-browser clients (e.g. Wikipedia HTTP
	// 403 for "Go-http-client/1.1") stay reachable for the crawl tool.
	hc := &http.Client{Transport: browserTransport()}

	fb := firebase.New(cfg.PublicRTDB, hc)
	settingsSvc := settings.New(fb, 60*time.Second)
	vfsSvc := vfs.New(fb)
	histStore := history.New(fb, cfg.HistoryCacheMax, cfg.HistoryCacheTTL)
	usageSvc := usage.New(fb)
	combosSvc := combos.New(fb, 60*time.Second)
	providersSvc := providers.New(fb, hc, 60*time.Second)
	providersSvc.WithBuiltin(providers.BuiltinProvider(cfg.AI))
	catalogSvc := skills.NewCatalog(vfsSvc)
	registrySvc := skills.NewRegistry(vfsSvc, skills.RegistryOptions{
		GitHubToken:  cfg.GitHubToken,
		ClawHubToken: cfg.ClawHubToken,
	})
	e2bSvc := e2b.NewManager(cfg.E2BApiKey, cfg.E2BDomain, hc)

	llm, err := ai.NewModel(cfg.AI.BaseURL, cfg.AI.APIKey, cfg.AI.Model, hc)
	if err != nil {
		log.Fatalf("ai model: %v", err)
	}
	// clientFor resolves the model per chat: users with their own API settings
	// (settings/{chat} in RTDB) get their own endpoint/key/model, everything
	// else inherits the server default. Each request builds a fresh model over
	// the shared http.Client, so parallel users never share a mutating struct.
	clientFor := func(ctx context.Context, chatID int64) llms.Model {
		aiCfg := cfg.AI
		if u := settingsSvc.Get(ctx, chatID); u != nil {
			aiCfg = settings.Effective(aiCfg, u)
		}
		// An active per-user combo overrides the model name: fallback picks the
		// model matching the current retry attempt, round-robin rotates.
		if comboModel := combosSvc.ModelForActive(ctx, chatID, ai.ComboAttempt(ctx)-1); comboModel != "" {
			aiCfg.Model = comboModel
		}
		// A "prefix/model-id" reference (registered provider) overrides the
		// endpoint/key/headers — mirroring 9router provider nodes. Provider wins
		// over the settings-based endpoint when the prefix is registered. The
		// proxy relay is only overridden when the provider sets one explicitly,
		// so the built-in "puru" provider inherits the global/per-user proxy
		// ON/OFF toggle.
		if resolved := providersSvc.Resolve(ctx, chatID, aiCfg.Model); resolved != nil {
			aiCfg.BaseURL = resolved.Provider.BaseURL
			aiCfg.APIKey = resolved.Provider.APIKey
			aiCfg.Model = resolved.Model
			aiCfg.Headers = resolved.Provider.Headers
			if resolved.Provider.ProxyURL != "" {
				aiCfg.ProxyURL = resolved.Provider.ProxyURL
			}
		}
		m, merr := ai.NewModelWithOptions(ai.ModelOptions{
			BaseURL: aiCfg.BaseURL,
			APIKey:  aiCfg.APIKey,
			Model:   aiCfg.Model,
			Headers: aiCfg.Headers,
			Proxy:   aiCfg.ProxyURL,
			Session: ai.ChatSessionID(chatID),
		}, hc)
		if merr != nil {
			return llm
		}
		return m
	}
	agentSvc := &ai.Agent{
		Client:    llm,
		Config:    cfg,
		VFS:       vfsSvc,
		E2B:       e2bSvc,
		Catalog:   catalogSvc,
		Registry:  registrySvc,
		HTTP:      hc,
		ClientFor: clientFor,
		Settings:  settingsSvc,
	}
	memSvc := memory.New(llm, vfsSvc)
	memSvc.ClientFor = clientFor
	tg := telegram.New(cfg.TelegramBotToken, hc)
	authSvc := auth.New(fb)
	schedSvc := scheduler.New(fb, cfg.SchedulePollSeconds)
	appSvc := app.New(cfg, tg, histStore, vfsSvc, agentSvc, memSvc, catalogSvc, registrySvc, schedSvc)
	appSvc.Settings = settingsSvc
	appSvc.Auth = authSvc
	appSvc.Usage = usageSvc

	// Health + web settings server (bind failures are non-fatal for the bot).
	srv := web.Serve(cfg, authSvc, settingsSvc, catalogSvc, registrySvc, vfsSvc, usageSvc, serverLog, combosSvc, providersSvc)
	go func() {
		if lerr := srv.ListenAndServe(); lerr != nil && !errors.Is(lerr, http.ErrServerClosed) {
			log.Printf("web server: %v", lerr)
		}
	}()

	ctx := context.Background()

	// Scheduled tasks (one-shot) — result delivered to the user's private chat.
	appSvc.StartScheduler(ctx)

	me, err := tg.GetMe(ctx)
	if err != nil {
		log.Fatalf("Cannot reach Telegram API: %v", err)
	}
	log.Printf("Telegram API connected: @%s", me.Username)

	if err := tg.DeleteWebhook(ctx, true); err != nil {
		log.Printf("deleteWebhook: %v", err)
	}

	var offset int64
	conflictCount := 0
	for {
		updates, err := tg.GetUpdates(ctx, offset, 40)
		if err != nil {
			var te *telegram.TelegramError
			if errors.As(err, &te) && te.IsConflict() {
				conflictCount++
				if conflictCount >= 5 {
					log.Printf("Conflict %dx berturut-turut — kemungkinan instance lain memakai token yang sama. Keluar agar platform restart bersih.", conflictCount)
					os.Exit(1)
				}
				log.Printf("Conflict terdeteksi (%d/5), mencoba lagi dalam 10s...", conflictCount)
				time.Sleep(10 * time.Second)
				_ = tg.DeleteWebhook(ctx, true)
				offset = 0
				continue
			}
			log.Printf("getUpdates error: %v", err)
			time.Sleep(5 * time.Second)
			continue
		}
		conflictCount = 0

		// Advance offset and dispatch. Handle returns immediately: each user's
		// work runs in its own goroutine (parallel across users, same user is
		// busy-guarded in the app layer).
		for _, u := range updates {
			offset = u.UpdateID + 1
			if err := appSvc.Handle(ctx, &u); err != nil {
				log.Printf("handle update %d failed: %v", u.UpdateID, err)
			}
		}
	}
}

// browserUA is a common Chrome user agent string; sites like Wikipedia reject
// the default Go HTTP client identifier ("Go-http-client/1.1") with 403.
const browserUA = "Mozilla/5.0 (Windows NT 10.0; Win64; x64) AppleWebKit/537.36 (KHTML, like Gecko) Chrome/151.0.0.0 Safari/537.36"

type uaTransport struct {
	base http.RoundTripper
}

func (t *uaTransport) RoundTrip(req *http.Request) (*http.Response, error) {
	if req.Header.Get("User-Agent") == "" {
		req.Header.Set("User-Agent", browserUA)
	}
	return t.base.RoundTrip(req)
}

func browserTransport() http.RoundTripper {
	return &uaTransport{base: http.DefaultTransport.(*http.Transport).Clone()}
}
