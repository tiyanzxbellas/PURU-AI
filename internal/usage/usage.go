// Package usage persists per-user per-request token usage so the web dashboard
// can show a 9router-style "Usage" section (overview cards + recent requests).
// Records are stored at usage/{chatID}/logs/{key}; old records are pruned to
// keep the stored set bounded.
package usage

import (
	"context"
	"encoding/json"
	"math"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/purujawa06-bot/PURU-AI/internal/firebase"
)

// MaxRecords caps the number of stored usage records per chat.
const MaxRecords = 500

// Record is a single model request's token usage.
type Record struct {
	At       string `json:"at"`       // RFC3339 UTC
	ChatID   int64  `json:"chatId"`   // chat that triggered the request
	Provider string `json:"provider"` // short label (host of the base URL)
	Model    string `json:"model"`
	Input    int    `json:"input"`
	Output   int    `json:"output"`
}

// Summary aggregates a set of records.
type Summary struct {
	TotalRequests int `json:"totalRequests"`
	TotalInput    int `json:"totalInput"`
	TotalOutput   int `json:"totalOutput"`
}

// Manager reads/writes usage records in RTDB.
type Manager struct {
	fb *firebase.Client
}

func New(fb *firebase.Client) *Manager { return &Manager{fb: fb} }

func path(chatID int64) string { return "usage/" + strconv.FormatInt(chatID, 10) }

func logsPath(chatID int64) string { return path(chatID) + "/logs" }

// key returns a zero-padded reverse-epoch key so a string sort orders newest
// first (a stable, same-length, integer-comparable key for Firebase object
// fields).
func key(at time.Time) string {
	// Reverse-epoch: MaxInt64 - unix-ms → newest timestamps get the smallest
	// keys. Fixed 20-digit padding keeps string ordering == numeric ordering
	// even before epoch 2038-2041 growth.
	return strconv.FormatInt(math.MaxInt64-at.UnixMilli(), 10)
}

// Add stores a new usage record, pruning the oldest ones beyond MaxRecords.
func (m *Manager) Add(ctx context.Context, chatID int64, provider, model string, input, output int) error {
	if provider == "" {
		provider = "default"
	}
	if model == "" {
		model = "-"
	}
	k := key(time.Now().UTC())
	rec := Record{
		At:       time.Now().UTC().Format(time.RFC3339),
		ChatID:   chatID,
		Provider: provider,
		Model:    model,
		Input:    input,
		Output:   output,
	}
	if err := m.fb.Put(ctx, logsPath(chatID)+"/"+k, rec); err != nil {
		return err
	}
	if err := m.prune(ctx, chatID); err != nil {
		return err
	}
	return nil
}

// prune removes records beyond MaxRecords (shallow key scan, keep newest).
func (m *Manager) prune(ctx context.Context, chatID int64) error {
	keys := m.fb.ListKeys(ctx, logsPath(chatID))
	if len(keys) <= MaxRecords {
		return nil
	}
	// Reverse-epoch keys sort by string the same as by int64 (same length).
	sort.Sort(sort.Reverse(sort.StringSlice(keys)))
	for _, k := range keys[MaxRecords:] {
		if err := m.fb.Delete(ctx, logsPath(chatID)+"/"+k); err != nil {
			return err
		}
	}
	return nil
}

// List returns the stored records newest-first. Only records matching the given
// chatID (or all when chatID==0) are returned.
func (m *Manager) List(ctx context.Context, chatID int64, limit int) ([]Record, error) {
	if limit <= 0 || limit > MaxRecords {
		limit = MaxRecords
	}
	keys := m.fb.ListKeys(ctx, logsPath(chatID))
	if len(keys) == 0 {
		return nil, nil
	}
	// Newest first (reverse-epoch keys).
	sort.Sort(sort.Reverse(sort.StringSlice(keys)))
	if len(keys) > limit {
		keys = keys[:limit]
	}
	recs := make([]Record, 0, len(keys))
	for _, k := range keys {
		raw := m.fb.Get(ctx, logsPath(chatID)+"/"+k)
		if len(raw) == 0 {
			continue
		}
		var r Record
		if err := json.Unmarshal(raw, &r); err != nil {
			continue
		}
		recs = append(recs, r)
	}
	return recs, nil
}

// Summarize aggregates the given records into a summary.
func Summarize(recs []Record) Summary {
	var s Summary
	s.TotalRequests = len(recs)
	for _, r := range recs {
		s.TotalInput += r.Input
		s.TotalOutput += r.Output
	}
	return s
}

// Clear deletes all usage records for a chat.
func (m *Manager) Clear(ctx context.Context, chatID int64) error {
	return m.fb.Delete(ctx, path(chatID))
}

// ProviderLabel extracts a short provider label from a base URL (host only).
func ProviderLabel(baseURL string) string {
	if baseURL == "" {
		return "default"
	}
	s := strings.TrimPrefix(baseURL, "https://")
	s = strings.TrimPrefix(s, "http://")
	if i := strings.IndexByte(s, '/'); i >= 0 {
		s = s[:i]
	}
	if s == "" {
		return "default"
	}
	return s
}
