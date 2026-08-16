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
//	go run ./cmd/cli -verbose "halo"     # trace tool nyata + output penuh + token + finish_reason
//	go run ./cmd/cli -json "halo"        # output JSON machine-readable (text/steps/usage)
//	go run ./cmd/cli -dump ./out "halo"  # simpan transcript JSON per run (run-<unix>.json)
//	go run ./cmd/cli -timeout 5m "halo"  # batas waktu proses sisi klien
//	go run ./cmd/cli -save-files ./out "halo" # simpan file hasil send_file ke disk
package main

import (
	"bufio"
	"context"
	"encoding/json"
	"flag"
	"fmt"
	"log"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"github.com/joho/godotenv"
	"github.com/tmc/langchaingo/llms"

	"github.com/purujawa06-bot/PURU-AI/internal/ai"
	"github.com/purujawa06-bot/PURU-AI/internal/combos"
	"github.com/purujawa06-bot/PURU-AI/internal/config"
	"github.com/purujawa06-bot/PURU-AI/internal/e2b"
	"github.com/purujawa06-bot/PURU-AI/internal/firebase"
	"github.com/purujawa06-bot/PURU-AI/internal/history"
	"github.com/purujawa06-bot/PURU-AI/internal/memory"
	"github.com/purujawa06-bot/PURU-AI/internal/messages"
	"github.com/purujawa06-bot/PURU-AI/internal/providers"
	"github.com/purujawa06-bot/PURU-AI/internal/scheduler"
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
	hc       *http.Client
	hist     *history.Store
	vfs      *vfs.VFS
	agent    *ai.Agent
	mem      *memory.Manager
	chatID   int64
	noMemory bool
	verbose  bool
	filesDir string
	jsonOut  bool
	dumpDir  string
	timeout  time.Duration
	memMu    sync.Mutex     // serializes background memory updates (single chat per process)
	memWG    sync.WaitGroup // one-shot mode waits on pending memory updates before exit
}

func main() {
	chatID := flag.Int64("chat", defaultChatID, "chat/user id untuk konteks debug")
	reset := flag.Bool("reset", false, "hapus history + VFS untuk chat id lalu exit")
	interactive := flag.Bool("interactive", false, "paksa mode REPL")
	verbose := flag.Bool("verbose", false, "tampilkan tool-call per langkah + token usage")
	noMemory := flag.Bool("no-memory", false, "nonaktifkan auto-update MEMORY.md")
	saveFiles := flag.String("save-files", "", "direktori untuk menyimpan file hasil send_file (default: hanya di-print)")
	jsonOut := flag.Bool("json", false, "output satu objek JSON machine-readable ke stdout (text, steps, usage)")
	dumpDir := flag.String("dump", "", "direktori untuk menyimpan transcript JSON per run (run-<unix>.json)")
	timeoutDur := flag.Duration("timeout", 0, "batas waktu proses (klien), mis. 5m / 90s; default 0 = tidak dibatasi oleh CLI")
	imagePath := flag.String("image", "", "kirim file gambar ke model dengan prompt dari argumen (tes dukungan visi model)")
	flag.Parse()

	_ = godotenv.Load()
	cfg, err := config.Load()
	if err != nil {
		log.Fatalf("config: %v", err)
	}
	hc := &http.Client{Transport: &uaTransport{base: http.DefaultTransport.(*http.Transport).Clone()}}

	fb := firebase.New(cfg.PublicRTDB, hc)
	settingsSvc := settings.New(fb, 60*time.Second)
	combosSvc := combos.New(fb, 60*time.Second)
	providersSvc := providers.New(fb, hc, 60*time.Second)
	providersSvc.WithBuiltin(providers.BuiltinProvider(cfg.AI))
	vfsSvc := vfs.New(fb)
	histStore := history.New(fb, cfg.HistoryCacheMax, cfg.HistoryCacheTTL)
	catalogSvc := skills.NewCatalog(vfsSvc)
	registrySvc := skills.NewRegistry(vfsSvc, skills.RegistryOptions{
		GitHubToken:  cfg.GitHubToken,
		ClawHubToken: cfg.ClawHubToken,
	})
	e2bSvc := e2b.NewManager(cfg.E2BApiKey, cfg.E2BDomain, hc)
	schedSvc := scheduler.New(fb, cfg.SchedulePollSeconds)

	llm, err := ai.NewModel(cfg.AI.BaseURL, cfg.AI.APIKey, cfg.AI.Model, hc)
	if err != nil {
		log.Fatalf("ai model: %v", err)
	}
	clientFor := func(ctx context.Context, cid int64) llms.Model {
		aiCfg := cfg.AI
		if u := settingsSvc.Get(ctx, cid); u != nil {
			aiCfg = settings.Effective(aiCfg, u)
		}
		if comboModel := combosSvc.ModelForActive(ctx, cid, ai.ComboAttempt(ctx)-1); comboModel != "" {
			aiCfg.Model = comboModel
		}
		if resolved := providersSvc.Resolve(ctx, cid, aiCfg.Model); resolved != nil {
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
			Session: ai.ChatSessionID(cid),
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
	agentSvc.ToolsBuild = func(opts *ai.ProcessOptions) (map[string]*ai.Tool, error) {
		return ai.BuildTools(agentSvc, opts), nil
	}
	// Wire scheduler hooks (mirrors internal/app.New) so schedule_task /
	// list_schedules / cancel_schedule are usable from the CLI debug session.
	agentSvc.ScheduleTask = func(ctx context.Context, userID int64, prompt string, runAt int64, tz string) (*scheduler.Task, error) {
		return schedSvc.Schedule(ctx, userID, prompt, runAt, tz)
	}
	agentSvc.ListSchedules = func(ctx context.Context, userID int64) ([]*scheduler.Task, error) {
		return schedSvc.List(ctx, userID)
	}
	agentSvc.CancelSchedule = func(ctx context.Context, userID int64, id string) error {
		return schedSvc.Cancel(ctx, userID, id)
	}
	memSvc := memory.New(llm, vfsSvc)
	memSvc.ClientFor = clientFor

	app := &cliApp{
		cfg:      cfg,
		hc:       hc,
		hist:     histStore,
		vfs:      vfsSvc,
		agent:    agentSvc,
		mem:      memSvc,
		chatID:   *chatID,
		noMemory: *noMemory,
		verbose:  *verbose,
		filesDir: *saveFiles,
		jsonOut:  *jsonOut,
		dumpDir:  *dumpDir,
		timeout:  *timeoutDur,
	}

	ctx := context.Background()
	if *imagePath != "" {
		app.probeImage(ctx, *imagePath, strings.Join(flag.Args(), " "))
		return
	}
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

func (a *cliApp) withTimeout(ctx context.Context) (context.Context, context.CancelFunc) {
	if a.timeout <= 0 {
		return ctx, func() {}
	}
	return context.WithTimeout(ctx, a.timeout)
}

// probeImage sends one image file to the configured vision model (Gemini-style
// endpoint, see VISION_MODEL_URL) with a prompt and prints the raw answer.
// Debug helper to verify a vision endpoint works before relying on image uploads.
func (a *cliApp) probeImage(ctx context.Context, path, prompt string) {
	data, err := os.ReadFile(path)
	if err != nil {
		log.Fatalf("read image: %v", err)
	}
	text, err := ai.DescribeImage(ctx, a.hc, a.cfg.VisionModelURL, prompt, data, "")
	if err != nil {
		log.Fatalf("[vision] error: %v", err)
	}
	fmt.Println(text)
}

func (a *cliApp) oneShot(ctx context.Context, prompt string) {
	cctx, cancel := a.withTimeout(ctx)
	defer cancel()
	res := a.process(cctx, prompt)
	a.printResult(res)
	if !a.jsonOut {
		fmt.Println(strings.TrimSpace(res.Text))
	}
	// One-shot exits after this call; wait so a pending background memory
	// update (fire-and-forget) is persisted before the process terminates.
	a.memWG.Wait()
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
		cctx, cancel := a.withTimeout(ctx)
		res := a.process(cctx, line)
		cancel()
		a.printResult(res)
		if !a.jsonOut {
			fmt.Printf("\nPURU-AI > %s\n\n", strings.TrimSpace(res.Text))
		}
	}
}

// printResult emits the result in the requested mode: -json writes a single
// machine-readable object to stdout; otherwise -verbose prints the step trace
// and usage. -dump always writes run-<unix>.json to disk.
func (a *cliApp) printResult(res *ai.ProcessResult) {
	if a.dumpDir != "" {
		if err := a.dumpRun(res); err != nil {
			log.Printf("[cli] dump failed: %v", err)
		}
	}
	if a.jsonOut {
		out := struct {
			Text         string    `json:"text"`
			FinishReason string    `json:"finish_reason"`
			Steps        []runStep `json:"steps,omitempty"`
			InputTokens  int       `json:"input_tokens"`
			OutputTokens int       `json:"output_tokens"`
			TotalTokens  int       `json:"total_tokens"`
			AccumTokens  int       `json:"accum_tokens"`
		}{
			Text:         res.Text,
			FinishReason: res.LastFinishReason,
			Steps:        collectSteps(res),
			InputTokens:  res.LastStepUsage.InputTokens,
			OutputTokens: res.LastStepUsage.OutputTokens,
			TotalTokens:  res.LastStepUsage.TotalTokens,
			AccumTokens:  res.TotalTokens,
		}
		b, _ := json.Marshal(out)
		fmt.Println(string(b))
		return
	}
	if a.verbose {
		printSteps(res)
		printUsage(res)
	}
}

// dumpRun persists the full step trace of one run as run-<unix>.json for
// external/factual comparison of what the agent actually executed.
func (a *cliApp) dumpRun(res *ai.ProcessResult) error {
	if err := os.MkdirAll(a.dumpDir, 0o755); err != nil {
		return err
	}
	out := struct {
		ChatID       int64     `json:"chat_id"`
		Text         string    `json:"text"`
		FinishReason string    `json:"finish_reason"`
		Steps        []runStep `json:"steps"`
		Usage        struct {
			Input  int `json:"input"`
			Output int `json:"output"`
			Total  int `json:"total"`
		} `json:"usage"`
	}{ChatID: a.chatID, Text: res.Text, FinishReason: res.LastFinishReason, Steps: collectSteps(res)}
	out.Usage.Input = res.LastStepUsage.InputTokens
	out.Usage.Output = res.LastStepUsage.OutputTokens
	out.Usage.Total = res.TotalTokens
	b, err := json.MarshalIndent(out, "", "  ")
	if err != nil {
		return err
	}
	// NOTE: Date.now()/time.Now() are fine here — this is CLI code running on a
	// real machine (not a workflow script), so wall-clock time is available.
	path := filepath.Join(a.dumpDir, fmt.Sprintf("run-%d.json", time.Now().Unix()))
	return os.WriteFile(path, b, 0o644)
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
		a.maybeUpdateMemory(saved)
	}
	return res
}

// memoryUpdateTimeout bounds one background MEMORY.md rewrite (mirrors
// internal/app). A slow memory model must not block the CLI prompt.
const memoryUpdateTimeout = 60 * time.Second

// maybeUpdateMemory kicks off a background MEMORY.md refresh. Fire-and-forget so
// the next REPL prompt is never delayed by the memory model call. Mirrors
// internal/app maybeUpdateMemory; errors are non-fatal.
func (a *cliApp) maybeUpdateMemory(msgs []*messages.Message) {
	if a.mem == nil || a.cfg.MemoryUpdateEvery <= 0 {
		return
	}
	a.memWG.Add(1)
	go func() {
		defer a.memWG.Done()
		a.updateMemoryAsync(context.Background(), msgs)
	}()
}

func (a *cliApp) updateMemoryAsync(ctx context.Context, msgs []*messages.Message) {
	a.memMu.Lock()
	defer a.memMu.Unlock()

	meta := a.hist.GetMeta(ctx, a.chatID)
	turns := meta.UserTurns + 1
	_ = a.hist.SetMeta(ctx, a.chatID, history.Meta{UserTurns: turns})
	if turns%a.cfg.MemoryUpdateEvery != 0 {
		return
	}
	mctx, cancel := context.WithTimeout(ctx, memoryUpdateTimeout)
	defer cancel()
	if updated, err := a.mem.UpdateMemory(mctx, a.chatID, msgs); err != nil {
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

// maxResultLen bounds how much of a tool result is echoed to the terminal.
// 4000 chars keeps long SSE/JSON results readable while stopping a 1MB crawl
// output from flooding the console.
const maxResultLen = 4000

// runStep is a single recorded tool execution for -json / -dump output.
type runStep struct {
	Tool        string `json:"tool"`
	ToolCallID  string `json:"tool_call_id,omitempty"`
	Args        any    `json:"args"`
	Result      string `json:"result"`
	ResultError bool   `json:"result_error,omitempty"`
}

// collectSteps extracts the actually-executed tool calls (assistant parts of
// type "tool-call") paired with their results (tool parts of type
// "tool-result") from the persisted ResponseMessages. This is the ground truth
// for what the agent really ran — distinct from any tool the model only claims
// to have used in its reply text.
func collectSteps(res *ai.ProcessResult) []runStep {
	if res == nil {
		return nil
	}
	var steps []runStep
	results := map[string]string{}
	errs := map[string]bool{}
	for _, m := range res.ResponseMessages {
		if m == nil || !messages.IsParts(m) {
			continue
		}
		for _, p := range messages.ContentParts(m) {
			switch p.Type() {
			case "tool-call":
				steps = append(steps, runStep{
					Tool:       p.Str("toolName"),
					ToolCallID: p.Str("toolCallId"),
					Args:       rawArgs(p["input"]),
				})
			case "tool-result":
				id := p.Str("toolCallId")
				results[id] = trimTo(string(p["output"]), maxResultLen)
				errs[id] = strings.Contains(p.Str("output"), `"error"`)
			}
		}
	}
	// Pair each step with its result (tool-result parts follow their tool-call
	// in responseMessages, so the last write wins; missing id → empty).
	for i := range steps {
		if r, ok := results[steps[i].ToolCallID]; ok {
			steps[i].Result = r
			steps[i].ResultError = errs[steps[i].ToolCallID]
		}
	}
	return steps
}

// rawArgs decodes a tool-call input JSON into an any (map) for stable JSON
// output; falls back to the raw string when it is not valid JSON.
func rawArgs(raw json.RawMessage) any {
	if len(raw) == 0 {
		return nil
	}
	var v any
	if json.Unmarshal(raw, &v) == nil {
		return v
	}
	return string(raw)
}

func printSteps(res *ai.ProcessResult) {
	steps := collectSteps(res)
	for i, s := range steps {
		mark := "✅"
		if s.ResultError {
			mark = "⚠️"
		}
		argsShort := s.Args
		if m, ok := s.Args.(map[string]any); ok {
			// compact long values inside args for the terminal trace
			for k, v := range m {
				if str, ok := v.(string); ok && len(str) > 120 {
					m[k] = str[:120] + "…"
				}
			}
			argsShort = m
		}
		ab, _ := json.Marshal(argsShort)
		fmt.Printf("  %s step %d: %s(%s) args=%s\n", mark, i+1, s.Tool, s.ToolCallID, string(ab))
		if s.Result != "" {
			fmt.Printf("    → %s\n", s.Result)
		}
	}
}

func printUsage(res *ai.ProcessResult) {
	u := res.LastStepUsage
	fmt.Printf("  📊 tokens: input=%d output=%d total=%d (acum=%d) finish_reason=%q\n", u.InputTokens, u.OutputTokens, u.TotalTokens, res.TotalTokens, res.LastFinishReason)
}

func trimTo(s string, max int) string {
	s = strings.TrimSpace(s)
	if len(s) > max {
		return s[:max] + "…(truncated)"
	}
	return s
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
