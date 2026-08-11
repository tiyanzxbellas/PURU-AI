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
	"net/http"

	"github.com/tmc/langchaingo/llms"

	"github.com/purujawa06-bot/PURU-AI/internal/ai/openai"
)

// noopStream forces stream:true in the model client without doing anything
// with the chunks. The response is fully assembled by the client and the
// gateway sees a streaming request.
func noopStream(context.Context, []byte) error { return nil }

// NewModel builds an OpenAI-compatible langchaingo model for the given
// endpoint/key/model using the shared HTTP client.
func NewModel(baseURL, apiKey, model string, hc *http.Client) (llms.Model, error) {
	return openai.New(baseURL, apiKey, model, hc)
}
