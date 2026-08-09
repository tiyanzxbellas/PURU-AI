// Package ai: model construction on top of langchaingo.
//
// The bot talks to any OpenAI-compatible endpoint. All requests go through the
// shared http.Client with the browser-like User-Agent so models used by the
// crawl tool keep working. langchaingo always receives a StreamingFunc so the
// OpenAI client sets stream:true and parses SSE chunks (some proxies reply with
// SSE framing even for non-streaming requests).
package ai

import (
	"context"
	"net/http"

	"github.com/tmc/langchaingo/llms"
	"github.com/tmc/langchaingo/llms/openai"
)

// noopStream forces stream:true in the underlying OpenAI client without doing
// anything with the chunks. The response is fully assembled by the client.
func noopStream(context.Context, []byte) error { return nil }

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