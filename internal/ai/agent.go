// Package ai implements the tool-calling agent around langchaingo.
//
// The tool loop, the assistant/tool message push-back and the session layout
// are owned by langchain: agents.Executor iterates and builds the scratchpad,
// and a prompts.ChatPromptTemplate (system + chat_history placeholder + input +
// agent_scratchpad) formats every request. Only the durable Vercel-compatible
// history adapter stays here. The model transport is langchaingo's
// OpenAI-compatible client (streaming, SSE-tolerant).
package ai

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log"
	"net/http"
	"regexp"
	"strconv"
	"strings"
	"time"

	"github.com/tmc/langchaingo/agents"
	"github.com/tmc/langchaingo/chains"
	"github.com/tmc/langchaingo/llms"
	"github.com/tmc/langchaingo/prompts"
	"github.com/tmc/langchaingo/schema"
	"github.com/tmc/langchaingo/tools"

	"github.com/purujawa06-bot/PURU-AI/internal/config"
	"github.com/purujawa06-bot/PURU-AI/internal/e2b"
	"github.com/purujawa06-bot/PURU-AI/internal/messages"
	"github.com/purujawa06-bot/PURU-AI/internal/prompt"
	"github.com/purujawa06-bot/PURU-AI/internal/scheduler"
	"github.com/purujawa06-bot/PURU-AI/internal/settings"
	"github.com/purujawa06-bot/PURU-AI/internal/skills"
	"github.com/purujawa06-bot/PURU-AI/internal/vfs"
)

const (
	toolTimeout    = 120 * time.Second
	totalAgentTime = 300 * time.Second
	maxRetries     = 4
)

// errNoModel is returned when no AI model could be resolved for the chat. It is
// fatal for the request and must not be retried.
var errNoModel = errors.New("no AI model configured")

// reasoningClient is implemented by models that can echo a thinking-mode
// model's reasoning_content back to the provider. Thinking-mode providers
// (deepseek-reasoner and friends) 400 on any follow-up request whose assistant
// history drops the reasoning_content of an earlier turn, so replayed assistant
// messages must carry it. Our OpenAI-compatible client implements this; other
// llms.Model implementations fall back to plain GenerateContent.
type reasoningClient interface {
	llms.Model
	GenerateContentWithReasoning(ctx context.Context, messages []llms.MessageContent, reasonings []string, options ...llms.CallOption) (*llms.ContentResponse, error)
}

const stepLimitHint = "⚠️ Percakapan mencapai batas maksimum langkah. Ketik `lanjut` atau `/ai lanjut` untuk melanjutkan percakapan dengan AI, atau masukkan prompt baru."

// Usage is the token usage of a single model round-trip.
type Usage struct {
	InputTokens  int
	OutputTokens int
	TotalTokens  int
}

func asInt(v any) int {
	switch n := v.(type) {
	case int:
		return n
	case int64:
		return int(n)
	case float64:
		return int(n)
	}
	return 0
}

// Tool is a registered tool implementation for a single request.
type Tool struct {
	Name        string
	Description string
	Parameters  map[string]any
	Run         func(ctx context.Context, args map[string]any) (any, error)
}

// Agent is the bot's agent: it owns the tools/environment and, per request,
// builds a langchaingo executor wired to a user-specific OpenAI-compatible
// model.
type Agent struct {
	Client     llms.Model
	Config     *config.Config
	VFS        *vfs.VFS
	E2B        *e2b.Manager
	Catalog    *skills.Catalog
	Registry   *skills.Registry
	HTTP       *http.Client
	ToolsBuild func(opts *ProcessOptions) (map[string]*Tool, error)
	// ClientFor resolves a model for a chat, allowing users to use their own
	// API settings. Nil means every chat uses Client. Each request builds its
	// own model (from settings) so parallel users never share a mutating
	// struct.
	ClientFor func(ctx context.Context, chatID int64) llms.Model
	// Settings holds per-user API overrides (including SystemPrompt). When set
	// the agent appends a user-defined system prompt to the base prompt.
	Settings *settings.Manager
	// Scheduler hooks for scheduled task execution. Set by app layer.
	ScheduleTask   func(ctx context.Context, userID int64, prompt string, runAt int64, tz string) (*scheduler.Task, error)
	ListSchedules  func(ctx context.Context, userID int64) ([]*scheduler.Task, error)
	CancelSchedule func(ctx context.Context, userID int64, id string) error
}

// clientFor picks the model for the request's chat: the per-chat resolver when
// configured, otherwise the shared default model.
func (a *Agent) clientFor(ctx context.Context, chatID int64) llms.Model {
	if a.ClientFor != nil {
		if m := a.ClientFor(ctx, chatID); m != nil {
			return m
		}
	}
	return a.Client
}

func (a *Agent) clientForOpts(ctx context.Context, opts *ProcessOptions, attempt int) llms.Model {
	if opts == nil {
		return a.clientFor(ctx, 0)
	}
	ctx = WithComboAttempt(ctx, attempt)
	return a.clientFor(ctx, opts.ChatID)
}

// comboAttemptKey is the context key carrying the current model-loop retry
// attempt (1-based). Merged into the per-chat model resolution so a fallback
// combo advances to the next model on each retry.
type comboAttemptKey struct{}

// WithComboAttempt stores the current attempt number in ctx.
func WithComboAttempt(ctx context.Context, attempt int) context.Context {
	return context.WithValue(ctx, comboAttemptKey{}, attempt)
}

// ComboAttempt reads the current model-loop attempt (0 when absent).
func ComboAttempt(ctx context.Context) int {
	if v, ok := ctx.Value(comboAttemptKey{}).(int); ok {
		return v
	}
	return 0
}

type ProcessOptions struct {
	ChatID     int64
	SendFile   func(content string, filename string, caption string) error
	SendBuffer func(data []byte, filename string, caption string) error
}

type ProcessResult struct {
	Text             string
	ResponseMessages []*messages.Message
	TotalTokens      int
	LastStepUsage    Usage
	// LastFinishReason is the stop reason of the last model response (e.g.
	// "stop", "tool_calls", "length"). Used for diagnostics: a natural stop
	// ends with "stop", a step-limit abort surfaces agents.ErrNotFinished.
	LastFinishReason string
}

// runResult is the outcome of a single executor run.
type runResult struct {
	finalText        string
	responseMessages []*messages.Message
	totalTokens      int
	lastStepUsage    Usage
	lastFinishReason string
	hitStepLimit     bool
}

func mustJSON(v any) []byte {
	b, _ := json.Marshal(v)
	return b
}

func toolResultText(p *messages.Part) string {
	raw, ok := (*p)["output"]
	if !ok {
		return ""
	}
	var out struct {
		Type  string          `json:"type"`
		Value json.RawMessage `json:"value"`
	}
	if err := json.Unmarshal(raw, &out); err != nil {
		return ""
	}
	switch out.Type {
	case "text", "error-text":
		var s string
		if json.Unmarshal(out.Value, &s) != nil {
			return strings.TrimSpace(string(out.Value))
		}
		return s
	case "json", "error-json":
		var v any
		if err := json.Unmarshal(out.Value, &v); err == nil {
			if b, err := json.Marshal(v); err == nil {
				return string(b)
			}
		}
		return strings.TrimSpace(string(out.Value))
	default:
		var s string
		if json.Unmarshal(out.Value, &s) == nil {
			return s
		}
		return strings.TrimSpace(string(out.Value))
	}
}

// responseFromSteps rebuilds the persisted stored messages (assistant tool
// calls + tool results) from the executor's recorded steps. reasoningByStep is
// aligned by absolute step index (see requestAgent.recordReasoning); a
// non-empty reasoning is persisted as a "reasoning" part so thinking-mode
// providers get their reasoning_content echoed on the next turn.
func responseFromSteps(steps []schema.AgentStep, reasoningByStep []string) []*messages.Message {
	if len(steps) == 0 {
		return nil
	}
	out := make([]*messages.Message, 0, 2*len(steps))
	for i := 0; i < len(steps); {
		j := i
		for j < len(steps) && steps[j].Action.Log == steps[i].Action.Log {
			j++
		}
		group := steps[i:j]
		var parts []messages.Part
		if reasoning := reasoningForStep(reasoningByStep, i); strings.TrimSpace(reasoning) != "" {
			parts = append(parts, messages.Part{
				"type": mustJSON("reasoning"),
				"text": mustJSON(reasoning),
			})
		}
		if log := strings.TrimSpace(group[0].Action.Log); log != "" {
			parts = append(parts, messages.Part{
				"type": mustJSON("text"),
				"text": mustJSON(log),
			})
		}
		for _, s := range group {
			var input any
			if err := json.Unmarshal([]byte(s.Action.ToolInput), &input); err != nil {
				input = map[string]any{}
			}
			parts = append(parts, messages.Part{
				"type":       mustJSON("tool-call"),
				"toolCallId": mustJSON(s.Action.ToolID),
				"toolName":   mustJSON(s.Action.Tool),
				"input":      mustJSON(input),
			})
		}
		as := &messages.Message{Role: "assistant"}
		messages.SetContentParts(as, parts)
		if messages.NetLen(as) > 0 {
			out = append(out, as)
		}
		for _, s := range group {
			toolMsg := &messages.Message{Role: "tool"}
			messages.SetContentParts(toolMsg, []messages.Part{{
				"type":       mustJSON("tool-result"),
				"toolCallId": mustJSON(s.Action.ToolID),
				"toolName":   mustJSON(s.Action.Tool),
				"output":     mustJSON(map[string]any{"type": "json", "value": parseObservation(s.Observation)}),
			}})
			out = append(out, toolMsg)
		}
		i = j
	}
	return out
}

func parseObservation(s string) any {
	var v any
	if json.Unmarshal([]byte(s), &v) == nil {
		return v
	}
	return s
}

// reasoningForStep returns the thinking-mode reasoning recorded for the step at
// index (empty when the model emitted none).
func reasoningForStep(reasoningByStep []string, idx int) string {
	if idx < 0 || idx >= len(reasoningByStep) {
		return ""
	}
	return reasoningByStep[idx]
}

func stepsFromValues(vals map[string]any) []schema.AgentStep {
	if raw, ok := vals["intermediateSteps"].([]schema.AgentStep); ok {
		return raw
	}
	return nil
}

func outputFromValues(vals map[string]any) string {
	s, _ := vals["output"].(string)
	return s
}

// ---------------------------------------------------------------------------
// langchaingo tool adapter
// ---------------------------------------------------------------------------

// langTool adapts a *Tool to the langchaingo tools.Tool interface. The executor
// hands us the raw JSON arguments string; we unmarshal it back into the args
// map the existing tool runners expect. Tool errors are returned as
// observations (JSON text), not as Go errors, so the loop keeps going — the
// model can then read the error and recover.
type langTool struct {
	name string
	desc string
	run  func(ctx context.Context, args map[string]any) (any, error)
}

func (t *langTool) Name() string        { return t.name }
func (t *langTool) Description() string { return t.desc }

func (t *langTool) Call(ctx context.Context, input string) (string, error) {
	args := map[string]any{}
	if err := json.Unmarshal([]byte(input), &args); err != nil {
		args["input"] = strings.TrimSpace(input)
	}
	cctx, cancel := context.WithTimeout(ctx, toolTimeout)
	defer cancel()
	val, runErr := t.run(cctx, args)
	if runErr != nil {
		val = map[string]any{"error": runErr.Error()}
	}
	b, err := json.Marshal(val)
	if err != nil {
		return `{"error":"failed to serialize tool output"}`, nil
	}
	return string(b), nil
}

func wrapTools(toolMap map[string]*Tool) []tools.Tool {
	out := make([]tools.Tool, 0, len(toolMap))
	for _, t := range toolMap {
		out = append(out, &langTool{name: t.Name, desc: t.Description, run: t.Run})
	}
	return out
}

func toFunctionDefinitions(toolMap map[string]*Tool) []llms.FunctionDefinition {
	out := make([]llms.FunctionDefinition, 0, len(toolMap))
	for _, t := range toolMap {
		out = append(out, llms.FunctionDefinition{
			Name:        t.Name,
			Description: t.Description,
			Parameters:  t.Parameters,
		})
	}
	return out
}

// ---------------------------------------------------------------------------
// requestAgent implements agents.Agent for one executor run
// ---------------------------------------------------------------------------

// requestAgent is the langchain Agent for one request. The langchaingo
// Executor owns the tool loop; the chat session (system + chat_history + input)
// is assembled by a langchain ChatPromptTemplate and Plan only converts the
// formatted messages, calls the model and parses the result.
type requestAgent struct {
	llm          llms.Model
	system       string
	history      []llms.ChatMessage
	tools        []tools.Tool
	functions    []llms.FunctionDefinition
	temperature  float64
	chatTemplate prompts.ChatPromptTemplate

	// usage observables (per request, single goroutine → plain fields).
	totalTokens int
	lastUsage   Usage
	// lastFinishReason of the last model response (for diagnostics).
	lastFinishReason string
	// nextToolIDSeq mints deterministic tool-call ids for providers that omit
	// the tool id (Gemini-family OpenAI-compatible gateways do). A missing id
	// breaks the tool-call ↔ tool-result pairing and Gemini 400s with "the
	// number of function response parts must equal the number of function call
	// parts".
	nextToolIDSeq int
	// reasoningByStep carries the thinking-mode reasoning_content produced by
	// each Plan, aligned to the executor's intermediate steps by absolute step
	// index (steps created by the same Plan share its reasoning). Replayed on
	// the scratchpad and persisted to history so thinking-mode providers never
	// see a dropped reasoning_content.
	reasoningByStep []string
	// lastReasoning is the reasoning_content of the final (finish) Plan, if
	// any — attached to the persisted final assistant message.
	lastReasoning string
}

func newRequestAgent(model llms.Model, system string, history []*messages.Message, toolMap map[string]*Tool, temperature float64) *requestAgent {
	conv := toChatHistory(history)
	if conv == nil {
		conv = []llms.ChatMessage{}
	}
	return &requestAgent{
		llm:         model,
		system:      system,
		tools:       wrapTools(toolMap),
		functions:   toFunctionDefinitions(toolMap),
		temperature: temperature,
		history:     conv,
		chatTemplate: prompts.NewChatPromptTemplate([]prompts.MessageFormatter{
			systemMessageFormatter(system),
			prompts.MessagesPlaceholder{VariableName: "chat_history"},
			prompts.NewHumanMessagePromptTemplate("{{.input}}", []string{"input"}),
			prompts.MessagesPlaceholder{VariableName: "agent_scratchpad"},
		}),
	}
}

func (ra *requestAgent) Plan(
	ctx context.Context,
	intermediateSteps []schema.AgentStep,
	inputs map[string]string,
	options ...chains.ChainCallOption,
) ([]schema.AgentAction, *schema.AgentFinish, error) {
	pv, err := ra.chatTemplate.FormatPrompt(map[string]any{
		"input":            inputs["input"],
		"chat_history":     ra.history,
		"agent_scratchpad": stepsToChatMessages(intermediateSteps, ra.reasoningByStep),
	})
	if err != nil {
		return nil, nil, err
	}

	formatted := pv.Messages()
	msgList := make([]llms.MessageContent, 0, len(formatted))
	reasonings := make([]string, 0, len(formatted))
	for _, m := range formatted {
		reasoning := ""
		if am, ok := m.(llms.AIChatMessage); ok {
			reasoning = am.ReasoningContent
		}
		msgList = append(msgList, chatMessageToContent(m))
		reasonings = append(reasonings, reasoning)
	}

	callOpts := []llms.CallOption{llms.WithStreamingFunc(noopStream)}
	if ra.temperature != 0 {
		callOpts = append(callOpts, llms.WithTemperature(ra.temperature))
	}
	if len(ra.functions) > 0 {
		callOpts = append(callOpts, llms.WithFunctions(ra.functions))
	}

	var resp *llms.ContentResponse
	if rc, ok := ra.llm.(reasoningClient); ok {
		resp, err = rc.GenerateContentWithReasoning(ctx, msgList, reasonings, callOpts...)
	} else {
		resp, err = ra.llm.GenerateContent(ctx, msgList, callOpts...)
	}
	if err != nil {
		log.Printf("[ai] GenerateContent failed: %v", err)
		return nil, nil, err
	}
	if resp == nil || len(resp.Choices) == 0 {
		log.Printf("[ai] model returned %d choices (empty response)", len(resp.Choices))
		return nil, nil, errors.New("empty model response")
	}
	ra.totalTokens += usageFromChoices(resp)
	ra.lastUsage = lastUsageFrom(resp)

	choice := resp.Choices[0]
	ra.lastFinishReason = choice.StopReason
	if len(choice.ToolCalls) > 0 {
		actions := make([]schema.AgentAction, 0, len(choice.ToolCalls))
		for _, tc := range choice.ToolCalls {
			actions = append(actions, schema.AgentAction{
				Tool:      tc.FunctionCall.Name,
				ToolInput: toolArgsJSON(tc.FunctionCall.Arguments),
				ToolID:    ra.toolID(tc.ID),
				Log:       choice.Content,
			})
		}
		ra.recordReasoning(choice.ReasoningContent, len(intermediateSteps), len(actions))
		return actions, nil, nil
	}
	if choice.FuncCall != nil {
		ra.recordReasoning(choice.ReasoningContent, len(intermediateSteps), 1)
		return []schema.AgentAction{{
			Tool:      choice.FuncCall.Name,
			ToolInput: toolArgsJSON(choice.FuncCall.Arguments),
			ToolID:    ra.toolID(""),
			Log:       choice.Content,
		}}, nil, nil
	}
	ra.lastReasoning = choice.ReasoningContent
	return nil, &schema.AgentFinish{
		ReturnValues: map[string]any{"output": choice.Content},
		Log:          choice.Content,
	}, nil
}

// recordReasoning stores the reasoning_content of a Plan that issued tool
// calls: every intermediate step this Plan creates (from startIdx onward) is
// marked with the same reasoning so the scratchpad and persisted history can
// echo it back to thinking-mode providers.
func (ra *requestAgent) recordReasoning(reasoning string, startIdx, nSteps int) {
	if nSteps <= 0 {
		return
	}
	for k := 0; k < nSteps; k++ {
		idx := startIdx + k
		for idx >= len(ra.reasoningByStep) {
			ra.reasoningByStep = append(ra.reasoningByStep, "")
		}
		ra.reasoningByStep[idx] = reasoning
	}
}

func usageFromChoices(resp *llms.ContentResponse) int {
	if resp == nil || len(resp.Choices) == 0 {
		return 0
	}
	return asInt(resp.Choices[0].GenerationInfo["TotalTokens"])
}

func lastUsageFrom(resp *llms.ContentResponse) Usage {
	if resp == nil || len(resp.Choices) == 0 {
		return Usage{}
	}
	return tokenUsageInfo(resp.Choices[0].GenerationInfo)
}

func tokenUsageInfo(gi map[string]any) Usage {
	return Usage{
		InputTokens:  asInt(gi["PromptTokens"]),
		OutputTokens: asInt(gi["CompletionTokens"]),
		TotalTokens:  asInt(gi["TotalTokens"]),
	}
}

func (ra *requestAgent) GetInputKeys() []string  { return []string{"input"} }
func (ra *requestAgent) GetOutputKeys() []string { return []string{"output"} }
func (ra *requestAgent) GetTools() []tools.Tool  { return ra.tools }

// toolID returns a non-empty tool-call id, minting a deterministic one when the
// provider omitted it. The same id is later replayed in the scratchpad and
// persisted history so the tool-result always pairs with its tool call.
func (ra *requestAgent) toolID(id string) string {
	if strings.TrimSpace(id) != "" {
		return id
	}
	ra.nextToolIDSeq++
	return fmt.Sprintf("call_%d", ra.nextToolIDSeq)
}

// ---------------------------------------------------------------------------
// runOnce: one tool-calling loop driven by langchaingo's executor
// ---------------------------------------------------------------------------

func (a *Agent) runOnce(ctx context.Context, system string, history []*messages.Message, userText string, opts *ProcessOptions, toolMap map[string]*Tool, attempt int) (*runResult, error) {
	ctx, cancel := context.WithTimeout(ctx, totalAgentTime)
	defer cancel()

	client := a.clientForOpts(ctx, opts, attempt)
	if client == nil {
		return nil, errNoModel
	}

	maxSteps := a.Config.MaxLoop
	if maxSteps <= 0 {
		maxSteps = 20
	}

	ra := newRequestAgent(client, system, history, toolMap, a.Config.Temperature)

	executor := agents.NewExecutor(ra,
		agents.WithMaxIterations(maxSteps),
		agents.WithReturnIntermediateSteps(),
	)

	vals, runErr := executor.Call(ctx, map[string]any{"input": userText})

	res := &runResult{
		totalTokens:      ra.totalTokens,
		lastStepUsage:    ra.lastUsage,
		lastFinishReason: ra.lastFinishReason,
	}
	steps := stepsFromValues(vals)
	// Hard guard: executor limits ITERATIONS (Plan calls), not total tool steps.
	// A model can return multiple tool calls per Plan → total steps can exceed maxSteps.
	// If actual steps exceed the configured limit, treat it as step-limit hit so we
	// return the step-limit hint and stop cleanly (tool executions already ran, but
	// we don't feed more context to the model).
	if len(steps) > maxSteps {
		res.hitStepLimit = true
	}
	res.responseMessages = responseFromSteps(steps, ra.reasoningByStep)

	if errors.Is(runErr, agents.ErrNotFinished) {
		res.hitStepLimit = true
		return res, nil
	}
	if runErr != nil {
		return nil, runErr
	}

	res.finalText = strings.TrimSpace(outputFromValues(vals))
	if res.finalText != "" {
		// Persist the final assistant turn, like the old loop did. A
		// thinking-mode model's reasoning_content is kept as a "reasoning" part
		// so the next request can echo it (deepseek-style providers 400
		// otherwise).
		m := &messages.Message{Role: "assistant"}
		if reasoning := strings.TrimSpace(ra.lastReasoning); reasoning != "" {
			messages.SetContentParts(m, []messages.Part{
				{"type": mustJSON("reasoning"), "text": mustJSON(reasoning)},
				{"type": mustJSON("text"), "text": mustJSON(res.finalText)},
			})
		} else {
			messages.SetContentString(m, res.finalText)
		}
		res.responseMessages = append(res.responseMessages, m)
	}
	return res, nil
}

// ---------------------------------------------------------------------------
// ProcessMessage: one request, assembled by langchain
// ---------------------------------------------------------------------------

func (a *Agent) ProcessMessage(ctx context.Context, userMessage string, history []*messages.Message, opts *ProcessOptions) *ProcessResult {
	// Strip leading non-user messages so the history always starts with a user.
	i := 0
	for i < len(history) && history[i].Role == "system" {
		i++
	}
	for i < len(history) && history[i].Role != "user" {
		history = append(history[:i], history[i+1:]...)
	}

	memoryContent := ""
	if m, ok := a.VFS.ReadFile(ctx, opts.ChatID, "memory/MEMORY.md"); ok {
		memoryContent = m
	}
	if len(memoryContent) > a.Config.MemoryMaxChars {
		memoryContent = memoryContent[:a.Config.MemoryMaxChars] + "\n...[truncated]"
	}

	skillsSummary := a.Catalog.BuildSkillsSummary(ctx, opts.ChatID)
	systemPrompt, err := prompt.Get(memoryContent, skillsSummary)
	if err != nil {
		log.Printf("[ai] prompt.Get failed, using empty system prompt: %v", err)
		systemPrompt = ""
	}

	// Append user-defined system prompt / role from web settings.
	if a.Settings != nil && opts != nil {
		if user := a.Settings.Get(ctx, opts.ChatID); user != nil && user.SystemPrompt != nil {
			sp := strings.TrimSpace(*user.SystemPrompt)
			if sp != "" {
				systemPrompt += "\n\n# User-defined instructions\n" + sp
			}
		}
	}

	tools, terr := a.ToolsBuild(opts)
	if terr != nil || len(tools) == 0 {
		log.Printf("[ai] ToolsBuild failed (err=%v, tools=%d): cannot process request", terr, len(tools))
		return errResult()
	}

	var lastErr error
	var attempts int
retryLoop:
	for attempt := 1; attempt <= maxRetries; attempt++ {
		attempts = attempt
		run, rerr := a.runOnce(ctx, systemPrompt, history, userMessage, opts, tools, attempt)
		switch {
		case rerr != nil:
			lastErr = rerr
			log.Printf("[ai] attempt %d/%d failed: %v", attempt, maxRetries, rerr)
			if isNonRetryableError(rerr) || errors.Is(rerr, errNoModel) {
				break retryLoop // fatal: retrying will not help
			}
		case run.hitStepLimit:
			return makeResult(stepLimitHint, run.responseMessages, run.totalTokens, run.lastStepUsage, run.lastFinishReason)
		case strings.TrimSpace(run.finalText) != "":
			return makeResult(run.finalText, run.responseMessages, run.totalTokens, run.lastStepUsage, run.lastFinishReason)
		default:
			lastErr = errors.New("empty final message from AI")
			log.Printf("[ai] attempt %d/%d empty final text (finish_reason=%q)", attempt, maxRetries, run.lastFinishReason)
		}

		if attempt < maxRetries {
			backoff := time.Duration(1000<<uint(attempt-1)) * time.Millisecond
			if backoff > 30*time.Second {
				backoff = 30 * time.Second
			}
			sleep(ctx, backoff)
			continue
		}
		break
	}

	log.Printf("[ai] reply failed after %d/%d attempts: %v", attempts, maxRetries, lastErr)
	return errResult()
}

func sleep(ctx context.Context, d time.Duration) {
	t := time.NewTimer(d)
	defer t.Stop()
	select {
	case <-ctx.Done():
	case <-t.C:
	}
}

func makeResult(text string, resp []*messages.Message, total int, usage Usage, finishReason string) *ProcessResult {
	if strings.TrimSpace(text) == "" {
		text = "Maaf, saya tidak bisa merespons saat ini."
	}
	return &ProcessResult{Text: text, ResponseMessages: resp, TotalTokens: total, LastStepUsage: usage, LastFinishReason: finishReason}
}

func errResult() *ProcessResult {
	return &ProcessResult{Text: "Maaf, saya tidak bisa merespons saat ini."}
}

// isNonRetryableError classification for the langchaingo OpenAI client: 4xx
// (except 408/429) are permanent and must not be retried.
var nonRetryableRe = regexp.MustCompile(`status code: (\d\d\d)`)

func isNonRetryableError(err error) bool {
	if errors.Is(err, agents.ErrNotFinished) {
		return false
	}
	m := nonRetryableRe.FindStringSubmatch(err.Error())
	if len(m) != 2 {
		return false
	}
	code, convErr := strconv.Atoi(m[1])
	if convErr != nil {
		return false
	}
	return code >= 400 && code < 500 && code != 408 && code != 429
}
