// Package ai: model construction on top of langchaingo.
//
// The bot talks to any OpenAI-compatible endpoint through our own minimal
// client (internal/ai/openai), sharing the http.Client with the browser-like
// User-Agent so models used by the crawl tool keep working. Requests ARE
// streamed: streaming keeps long generations alive past gateway buffering
// timeouts (non-streaming requests that produce a long answer/tool loop get
// cut by the proxy with HTTP 503). Tool-call fragments in the stream are
// assembled correctly by the client (the langchaingo v0.1.14 OpenAI client
// mishandles argument fragments — see internal/ai/openai package doc).
package ai

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"net/http"
	"strconv"

	"github.com/tmc/langchaingo/llms"

	"github.com/purujawa06-bot/PURU-AI/internal/ai/openai"
)

// noopStream forces stream:true in the model client without doing anything
// with the chunks. The response is fully assembled by the client and the
// gateway sees a streaming request.
func noopStream(context.Context, []byte) error { return nil }

// ModelOptions mirrors openai.Options plus per-chat session handling. Headers
// may carry the "@session" / "@request" markers (expanded by the client).
type ModelOptions struct {
	BaseURL string
	APIKey  string
	Model   string
	Headers map[string]string
	Proxy   string
	Session string
}

// NewModelWithOptions builds an OpenAI-compatible langchaingo model for the
// given options using the shared HTTP client. Headers/Proxy/Session are passed
// through to the wire (provider templates that need custom headers or an edge
// relay use this).
func NewModelWithOptions(opts ModelOptions, hc *http.Client) (llms.Model, error) {
	return openai.NewWithOptions(openai.Options{
		BaseURL: opts.BaseURL,
		APIKey:  opts.APIKey,
		Model:   opts.Model,
		Headers: opts.Headers,
		Proxy:   opts.Proxy,
		Session: opts.Session,
	}, hc)
}

// NewModel builds an OpenAI-compatible langchaingo model for the given
// endpoint/key/model using the shared HTTP client.
func NewModel(baseURL, apiKey, model string, hc *http.Client) (llms.Model, error) {
	return openai.New(baseURL, apiKey, model, hc)
}

// ChatSessionID derives a stable, deterministic session id for a chat. Gateway
// providers like the opencode zen endpoint fingerprint a conversation by its
// x-opencode-session header, so the id must stay constant across every request
// of the same chat (hash of the chat id, no storage needed).
func ChatSessionID(chatID int64) string {
	sum := sha256.Sum256([]byte(strconv.FormatInt(chatID, 10)))
	return "ses_" + hex.EncodeToString(sum[:])
}
