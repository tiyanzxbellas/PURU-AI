// Package ai: model construction on top of langchaingo.
//
// The bot talks to any OpenAI-compatible endpoint. All requests go through the
// shared http.Client with the browser-like User-Agent so models used by the
// crawl tool keep working. Requests are NOT streamed: langchaingo v0.1.14's
// streaming client only concatenates tool-call argument fragments when the
// delta has an empty `type`. Gateways that tag every streamed fragment with
// `type:"function"` (observed on the HF Spaces gateway serving the `puru`
// model) would then turn each argument chunk into a separate, empty-named
// tool call, so tools were invoked with empty args and the loop exploded into
// fake tool-calls until hitting the step limit. Non-streaming responses carry
// the full JSON arguments on a single tool call and are assembled reliably.
package ai

import (
	"net/http"

	"github.com/tmc/langchaingo/llms"
	"github.com/tmc/langchaingo/llms/openai"
)

// NewModel builds an OpenAI-compatible langchaingo model for the given
// endpoint/key/model using the shared HTTP client.
func NewModel(baseURL, apiKey, model string, hc *http.Client) (llms.Model, error) {
	opts := []openai.Option{
		openai.WithBaseURL(baseURL),
		openai.WithHTTPClient(hc),
	}
	if apiKey != "" {
		opts = append(opts, openai.WithToken(apiKey))
	}
	if model != "" {
		opts = append(opts, openai.WithModel(model))
	}
	return openai.New(opts...)
}
