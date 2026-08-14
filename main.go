// main wires everything together: environment, health server, Telegram
// long-polling loop with conflict retry, and the update handler.
package main

import (
	"context"
	"errors"
	"log"
	"net/http"
	"os"
	"time"

	"github.com/joho/godotenv"
	"github.com/tmc/langchaingo/llms"

	"github.com/purujawa06-bot/PURU-AI/internal/ai"
	"github.com/purujawa06-bot/PURU-AI/internal/app"
	"github.com/purujawa06-bot/PURU-AI/internal/auth"
	"github.com/purujawa06-bot/PURU-AI/internal/config"
	"github.com/purujawa06-bot/PURU-AI/internal/e2b"
	"github.com/purujawa06-bot/PURU-AI/internal/firebase"
	"github.com/purujawa06-bot/PURU-AI/internal/history"
	"github.com/purujawa06-bot/PURU-AI/internal/memory"
	"github.com/purujawa06-bot/PURU-AI/internal/settings"
	"github.com/purujawa06-bot/PURU-AI/internal/skills"
	"github.com/purujawa06-bot/PURU-AI/internal/telegram"
	"github.com/purujawa06-bot/PURU-AI/internal/vfs"
	"github.com/purujawa06-bot/PURU-AI/internal/web"
)

func main() {
	_ = godotenv.Load()

	cfg, err := config.Load()
	if err != nil {
		log.Fatalf("config: %v", err)
	}

	// Shared HTTP client. A browser-like User-Agent is injected on outbound
	// requests so sites that block non-browser clients (e.g. Wikipedia HTTP
	// 403 for "Go-http-client/1.1") stay reachable for the crawl tool.
	hc := &http.Client{Transport: browserTransport()}

	fb := firebase.New(cfg.PublicRTDB, hc)
	settingsSvc := settings.New(fb, 60*time.Second)
	vfsSvc := vfs.New(fb)
	histStore := history.New(fb, cfg.HistoryCacheMax, cfg.HistoryCacheTTL)
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
		m, merr := ai.NewModel(aiCfg.BaseURL, aiCfg.APIKey, aiCfg.Model, hc)
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
	appSvc := app.New(cfg, tg, histStore, vfsSvc, agentSvc, memSvc, catalogSvc, registrySvc)
	appSvc.Settings = settingsSvc
	appSvc.Auth = authSvc

	// Health + web settings server (bind failures are non-fatal for the bot).
	srv := web.Serve(cfg, hc, authSvc, settingsSvc, catalogSvc, registrySvc)
	go func() {
		if lerr := srv.ListenAndServe(); lerr != nil && !errors.Is(lerr, http.ErrServerClosed) {
			log.Printf("web server: %v", lerr)
		}
	}()

	ctx := context.Background()
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
