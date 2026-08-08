// Package tokens wraps tiktoken (o200k_base, matching js-tiktoken) for the
// /token command and history token estimation.
package tokens

import (
	"sync"

	"github.com/tiktoken-go/tokenizer"

	"github.com/purujawa06-bot/PURU-AI/internal/messages"
)

var (
	once sync.Once
	enc  tokenizer.Codec
)

func initEncoder() {
	enc, _ = tokenizer.Get(tokenizer.O200kBase)
}

// Count returns the number of tokens for a UTF-8 string using o200k_base.
// Falls back to a byte-length estimate if the encoder is unavailable.
func Count(s string) int {
	once.Do(initEncoder)
	if enc == nil {
		return len(s)
	}
	ids, _, _ := enc.Encode(s)
	return len(ids)
}

// CountConvTokens counts tokens for user & assistant text only (system, tool
// results and tool-call args are excluded) — same rule as the TS bot.
func CountConvTokens(msgs []*messages.Message) int {
	total := 0
	for _, m := range msgs {
		if m == nil {
			continue
		}
		if !messages.IsUser(m) && !messages.IsAssistant(m) {
			continue
		}
		if messages.IsParts(m) {
			for _, p := range messages.ContentParts(m) {
				if p.Type() == "text" {
					total += Count(p.Text())
				}
			}
			continue
		}
		if s, ok := messages.ContentString(m); ok {
			total += Count(s)
		}
	}
	return total
}
