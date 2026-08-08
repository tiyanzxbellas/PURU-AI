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

	"github.com/purujawa/puru-ai/internal/ai"
	"github.com/purujawa/puru-ai/internal/app"
	"github.com/purujawa/puru-ai/internal/config"
	"github.com/purujawa/puru-ai/internal/e2b"
	"github.com/purujawa/puru-ai/internal/firebase"
	"github.com/purujawa/puru-ai/internal/health"
	"github.com/purujawa/puru-ai/internal/history"
	"github.com/purujawa/puru-ai/internal/memory"
	"github.com/purujawa/puru-ai/internal/skills"
	"github.com/purujawa/puru-ai/internal/telegram"
	"github.com/purujawa/puru-ai/internal/vfs"
)

func main() {
	_ = godotenv.Load()

	cfg, err := config.Load()
	if err != nil {
		log.Fatalf("config: %v", err)
	}

	hc := &http.Client{}

	fb := firebase.New(cfg.PublicRTDB, hc)
	vfsSvc := vfs.New(fb)
	histStore := history.New(fb, cfg.HistoryCacheMax, cfg.HistoryCacheTTL)
	catalogSvc := skills.NewCatalog(vfsSvc)
	registrySvc := skills.NewRegistry(vfsSvc, hc)
	e2bSvc := e2b.NewManager(cfg.E2BApiKey, cfg.E2BDomain, hc)

	llm := &ai.Client{BaseURL: cfg.AI.BaseURL, APIKey: cfg.AI.APIKey, Model: cfg.AI.Model, HTTP: hc}
	agentSvc := &ai.Agent{
		Client:   llm,
		Config:   cfg,
		VFS:      vfsSvc,
		E2B:      e2bSvc,
		Catalog:  catalogSvc,
		Registry: registrySvc,
		HTTP:     hc,
	}
	memSvc := memory.New(llm, vfsSvc)
	tg := telegram.New(cfg.TelegramBotToken, hc)
	appSvc := app.New(cfg, tg, histStore, vfsSvc, agentSvc, memSvc, catalogSvc, registrySvc)

	// Health server (bind failures are non-fatal for the bot).
	srv := health.Serve(cfg.Hostname, cfg.Port)
	go func() {
		if lerr := srv.ListenAndServe(); lerr != nil && !errors.Is(lerr, http.ErrServerClosed) {
			log.Printf("health server: %v", lerr)
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

		// Process updates one by one (sequential, low RAM).
		for _, u := range updates {
			offset = u.UpdateID + 1
			if err := appSvc.Handle(ctx, &u); err != nil {
				log.Printf("handle update %d failed: %v", u.UpdateID, err)
			}
		}
	}
}
