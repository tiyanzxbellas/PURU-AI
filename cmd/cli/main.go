// Command cli is a debug CLI that talks to the PURU-AI agent directly from the
// terminal, without Telegram. It wires the same services as main.go (config,
// Firebase RTDB, VFS, history, skills, E2B, model, memory) and drives
// ai.Agent.ProcessMessage with the exact same pipeline as the Telegram handler
// (prune → cap → process → persist → memory), so debugging a conversation in a
// terminal mirrors production behaviour 1:1.
//
// Usage:
//
//	go run ./cmd/cli "pesan..."          # one-shot, jawaban ke stdout
//	go run ./cmd/cli                     # REPL interaktif
//	go run ./cmd/cli -reset              # hapus history + VFS untuk chat id
//	go run ./cmd/cli -chat 123 "halo"    # pilih chat id debug (default -777)
//	go run ./cmd/cli -verbose "halo"     # tampilkan tool-call per langkah + token
//	go run ./cmd/cli -save-files ./out "halo" # simpan file hasil send_file ke disk
package main

import (
	"bufio"
	"context"
	"flag"
	"fmt"
	"log"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/joho/godotenv"
	"github.com/tmc/langchaingo/llms"

	"github.com/purujawa06-bot/PURU-AI/internal/ai"
	"github.com/purujawa06-bot/PURU-AI/internal/config"
	"github.com/purujawa06-bot/PURU-AI/internal/e2b"
	"github.com/purujawa06-bot/PURU-AI/internal/firebase"
	"github.com/purujawa06-bot/PURU-AI/internal/history"
	"github.com/purujawa06-bot/PURU-AI/internal/memory"
	"github.com/purujawa06-bot/PURU-AI/internal/messages"
	"github.com/purujawa06-bot/PURU-AI/internal/settings"
	"github.com/purujawa06-bot/PURU-AI/internal/skills"
	"github.com/purujawa06-bot/PURU-AI/internal/vfs"
)

// defaultChatID keeps debug data separate from real Telegram users (negative
// ids are never used by Telegram).
const defaultChatID = -777

// cliApp holds the wired services, mirroring main.go minus Telegram.
type cliApp struct {
	cfg      *config.Config
	hist     *history.Store
	vfs      *vfs.VFS
	agent    *ai.Agent
	mem      *memory.Manager
	chatID   int64
	noMemory bool
	verbose  bool
	filesDir string
}

func main() {
	chatID := flag.Int64("chat", defaultChatID, "chat/user id untuk konteks debug")
	reset := flag.Bool("reset", false, "hapus history + VFS untuk chat id lalu exit")
	interactive := flag.Bool("interactive", false, "paksa mode REPL")
	verbose := flag.Bool("verbose", false, "tampilkan tool-call per langkah + token usage")
	noMemory := flag.Bool("no-memory", false, "nonaktifkan auto-update MEMORY.md")
	saveFiles := flag.String("save-files", "", "direktori untuk menyimpan file hasil send_file (default: hanya di-print)")
	flag.Parse()

	_ = godotenv.Load()
	cfg, err := config.Load()
	if err != nil {
		log.Fatalf("config: %v", err)
	}
	hc := &http.Client{Transport: &uaTransport{base: http.DefaultTransport.(*http.Transport).Clone()}}

	fb := firebase.New(cfg.PublicRTDB, hc)
	settingsSvc := settings.New(fb, 60*time.Second)
	vfsSvc := vfs.New(fb)
	histStore := history.New(fb, cfg.HistoryCacheMax, cfg.HistoryCacheTTL)
	catalogSvc := skills.NewCatalog(vfsSvc)
	registrySvc := skills.NewRegistry(vfsSvc, hc)
	e2bSvc := e2b.NewManager(cfg.E2BApiKey, cfg.E2BDomain, hc)

	llm, err := ai.NewModel(cfg.AI.BaseURL, cfg.AI.APIKey, cfg.AI.Model, hc)
	if err != nil {
		log.Fatalf("ai model: %v", err)
	}
	clientFor := func(ctx context.Context, cid int64) llms.Model {
		aiCfg := cfg.AI
		if u := settingsSvc.Get(ctx, cid); u != nil {
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
	}
	agentSvc.ToolsBuild = func(opts *ai.ProcessOptions) (map[string]*ai.Tool, error) {
		return ai.BuildTools(agentSvc, opts), nil
	}
	memSvc := memory.New(llm, vfsSvc)
	memSvc.ClientFor = clientFor

	app := &cliApp{
		cfg:      cfg,
		hist:     histStore,
		vfs:      vfsSvc,
		agent:    agentSvc,
		mem:      memSvc,
		chatID:   *chatID,
		noMemory: *noMemory,
		verbose:  *verbose,
		filesDir: *saveFiles,
	}

	ctx := context.Background()
	if *reset {
		app.reset(ctx)
		return
	}

	if *interactive || flag.NArg() == 0 {
		app.repl(ctx)
		return
	}
	app.oneShot(ctx, strings.Join(flag.Args(), " "))
}

func (a *cliApp) oneShot(ctx context.Context, prompt string) {
	res := a.process(ctx, prompt)
	if a.verbose {
		printSteps(res)
		printUsage(res)
	}
	fmt.Println(strings.TrimSpace(res.Text))
}

func (a *cliApp) repl(ctx context.Context) {
	fmt.Printf("CLI PURU-AI — chat=%d — ketik /exit untuk keluar\n", a.chatID)
	sc := bufio.NewScanner(os.Stdin)
	for {
		fmt.Print("Anda > ")
		if !sc.Scan() {
			break
		}
		line := strings.TrimSpace(sc.Text())
		switch line {
		case "/exit", "/quit", "/q":
			return
		case "/reset":
			a.reset(ctx)
			continue
		case "/verbose":
			a.verbose = !a.verbose
			fmt.Printf("verbose: %v\n", a.verbose)
			continue
		}
		if line == "" {
			continue
		}
		res := a.process(ctx, line)
		if a.verbose {
			printSteps(res)
			printUsage(res)
		}
		fmt.Printf("\nPURU-AI > %s\n\n", strings.TrimSpace(res.Text))
	}
}

// process mirrors internal/app/generate.go processMessage: prune → cap →
// ProcessMessage → persist tokens/history → auto-update memory.
func (a *cliApp) process(ctx context.Context, prompt string) *ai.ProcessResult {
	stored, err := a.hist.GetHistory(ctx, a.chatID)
	if err != nil {
		log.Printf("[cli] getHistory: %v", err)
	}
	hs := messages.CapUserTurns(messages.PruneMessages(stored))

	sink := &fileSink{dir: a.filesDir}
	res := a.agent.ProcessMessage(ctx, prompt, hs, &ai.ProcessOptions{
		ChatID: a.chatID,
		SendFile: func(content, filename, caption string) error {
			return sink.write(filename, caption, []byte(content))
		},
		SendBuffer: func(data []byte, filename, caption string) error {
			return sink.write(filename, caption, data)
		},
	})

	saved := make([]*messages.Message, 0, len(hs)+1+len(res.ResponseMessages))
	saved = append(saved, hs...)
	user := &messages.Message{Role: "user"}
	messages.SetContentString(user, prompt)
	saved = append(saved, user)
	saved = append(saved, messages.SanitizeHistoryMessages(res.ResponseMessages)...)

	_ = a.hist.SetTokens(ctx, a.chatID, &history.Tokens{
		Total:  res.LastStepUsage.TotalTokens,
		Input:  res.LastStepUsage.InputTokens,
		Output: res.LastStepUsage.OutputTokens,
	})
	_ = a.hist.SetHistory(ctx, a.chatID, saved)

	if !a.noMemory {
		a.maybeUpdateMemory(ctx, saved)
	}
	return res
}

// maybeUpdateMemory replicates internal/app maybeUpdateMemory (non-fatal).
func (a *cliApp) maybeUpdateMemory(ctx context.Context, msgs []*messages.Message) {
	if a.mem == nil || a.cfg.MemoryUpdateEvery <= 0 {
		return
	}
	meta := a.hist.GetMeta(ctx, a.chatID)
	turns := meta.UserTurns + 1
	_ = a.hist.SetMeta(ctx, a.chatID, history.Meta{UserTurns: turns})
	if turns%a.cfg.MemoryUpdateEvery != 0 {
		return
	}
	if updated, err := a.mem.UpdateMemory(ctx, a.chatID, msgs); err != nil {
		log.Printf("[cli] memory update failed: %v", err)
	} else if updated != "" {
		log.Printf("[cli] memory updated for chat %d (turn %d)", a.chatID, turns)
	}
}

func (a *cliApp) reset(ctx context.Context) {
	if err := a.hist.DeleteHistory(ctx, a.chatID); err != nil {
		log.Printf("[cli] delete history failed: %v", err)
	}
	if err := a.vfs.DeleteAll(ctx, a.chatID); err != nil {
		log.Printf("[cli] delete vfs failed: %v", err)
	}
	fmt.Printf("Reset selesai untuk chat=%d (history + VFS dihapus).\n", a.chatID)
}

// ---------------------------------------------------------------------------
// send_file / send_buffer output
// ---------------------------------------------------------------------------

// fileSink writes files produced by the send_file tool either to disk or to
// stdout, depending on dir.
type fileSink struct{ dir string }

func (s *fileSink) write(filename, caption string, data []byte) error {
	name := filepath.Base(filename)
	if s.dir != "" {
		if err := os.MkdirAll(s.dir, 0o755); err != nil {
			return err
		}
		path := filepath.Join(s.dir, name)
		if err := os.WriteFile(path, data, 0o644); err != nil {
			return err
		}
		fmt.Printf("📎 FILE disimpan: %s\n", path)
		return nil
	}
	fmt.Printf("📎 FILE: %s (%d bytes) caption=%q\n", name, len(data), caption)
	return nil
}

// ---------------------------------------------------------------------------
// verbose diagnostics
// ---------------------------------------------------------------------------

func printSteps(res *ai.ProcessResult) {
	for _, m := range res.ResponseMessages {
		if m == nil {
			continue
		}
		if m.Role == "assistant" && messages.IsParts(m) {
			for _, p := range messages.ContentParts(m) {
				if p.Type() != "tool-call" {
					continue
				}
				fmt.Printf("  🛠 tool-call: %s(%s) args=%s\n", p.Str("toolName"), p.Str("toolCallId"), shortText(string(p["input"])))
			}
		}
		if m.Role == "tool" && messages.IsParts(m) {
			for _, p := range messages.ContentParts(m) {
				if p.Type() != "tool-result" {
					continue
				}
				fmt.Printf("  ↩ tool-result: %s → %s\n", p.Str("toolName"), shortText(string(p["output"])))
			}
		}
	}
}

func printUsage(res *ai.ProcessResult) {
	u := res.LastStepUsage
	fmt.Printf("  📊 tokens: input=%d output=%d total=%d (acum=%d)\n", u.InputTokens, u.OutputTokens, u.TotalTokens, res.TotalTokens)
}

func shortText(raw string) string {
	raw = strings.TrimSpace(raw)
	if len(raw) > 220 {
		return raw[:220] + "…"
	}
	return raw
}

// ---------------------------------------------------------------------------
// browser User-Agent transport (same as main.go)
// ---------------------------------------------------------------------------

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
