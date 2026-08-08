// Package history persists per-user chat history in Firebase RTDB with an
// in-memory LRU+TTL cache, replicating the behaviour of the TS module (max
// entries, TTL, empty arrays not cached, getHistory returns a copy).
package history

import (
	"container/list"
	"context"
	"encoding/json"
	"fmt"
	"sync"
	"time"

	"github.com/purujawa/puru-ai/internal/firebase"
	"github.com/purujawa/puru-ai/internal/messages"
)

type Store struct {
	fb           *firebase.Client
	historyCache *lruCache[[]*messages.Message]
	tokensCache  *lruCache[*Tokens]
}

// Tokens mirrors the SDK usage counters persisted at history/{id}/tokens.
type Tokens struct {
	Total  int `json:"total"`
	Input  int `json:"input"`
	Output int `json:"output"`
}

// Meta mirrors history/{id}/meta (internal turn counter used by memory).
type Meta struct {
	UserTurns int `json:"userTurns"`
}

func New(fb *firebase.Client, max int, ttlMS int64) *Store {
	return &Store{
		fb:           fb,
		historyCache: newLRU[[]*messages.Message](max, ttlMS),
		tokensCache:  newLRU[*Tokens](max, ttlMS),
	}
}

// GetHistory returns a copy of the stored history. A cache miss loads from
// Firebase; empty arrays are not cached.
func (s *Store) GetHistory(ctx context.Context, chatID int64) ([]*messages.Message, error) {
	key := strKey(chatID)
	if cached, ok := s.historyCache.Get(key); ok {
		return cloneMessages(cached), nil
	}
	raw := s.fb.Get(ctx, "history/"+key+"/messages")
	if len(raw) == 0 {
		return nil, nil
	}
	parsed, err := MessagesFromRaw(raw)
	if err != nil {
		return nil, err
	}
	if len(parsed) > 0 {
		s.historyCache.Set(key, parsed)
	}
	return parsed, nil
}

// SetHistory stores history in cache and Firebase (synchronous write).
func (s *Store) SetHistory(ctx context.Context, chatID int64, msgs []*messages.Message) error {
	key := strKey(chatID)
	s.historyCache.Set(key, cloneMessages(msgs))
	return s.fb.Put(ctx, "history/"+key+"/messages", msgs)
}

func (s *Store) DeleteHistory(ctx context.Context, chatID int64) error {
	key := strKey(chatID)
	s.historyCache.Delete(key)
	s.tokensCache.Delete(key)
	return s.fb.Delete(ctx, "history/"+key)
}

func (s *Store) GetTokens(ctx context.Context, chatID int64) *Tokens {
	key := strKey(chatID)
	if cached, ok := s.tokensCache.Get(key); ok {
		return cached
	}
	raw := s.fb.Get(ctx, "history/"+key+"/tokens")
	if len(raw) == 0 {
		return nil
	}
	var t Tokens
	if json.Unmarshal(raw, &t) != nil {
		return nil
	}
	s.tokensCache.Set(key, &t)
	return &t
}

func (s *Store) SetTokens(ctx context.Context, chatID int64, t *Tokens) error {
	key := strKey(chatID)
	s.tokensCache.Set(key, t)
	return s.fb.Put(ctx, "history/"+key+"/tokens", t)
}

func (s *Store) GetMeta(ctx context.Context, chatID int64) Meta {
	raw := s.fb.Get(ctx, "history/"+strKey(chatID)+"/meta")
	if len(raw) == 0 {
		return Meta{}
	}
	var meta Meta
	_ = json.Unmarshal(raw, &meta)
	if meta.UserTurns < 0 {
		meta.UserTurns = 0
	}
	return meta
}

func (s *Store) SetMeta(ctx context.Context, chatID int64, meta Meta) error {
	return s.fb.Put(ctx, "history/"+strKey(chatID)+"/meta", meta)
}

func strKey(chatID int64) string { return fmt.Sprint(chatID) }

func cloneMessages(in []*messages.Message) []*messages.Message {
	out := append([]*messages.Message{}, in...)
	return out
}

// MessagesFromRaw decodes a stored JSON array of model messages.
func MessagesFromRaw(raw []byte) ([]*messages.Message, error) {
	if len(raw) == 0 {
		return nil, nil
	}
	var msgs []*messages.Message
	if err := json.Unmarshal(raw, &msgs); err != nil {
		return nil, err
	}
	return msgs, nil
}

// ---------------------------------------------------------------------------
// LRU + TTL cache (replicates lru-cache with ttl and access-time refresh)
// ---------------------------------------------------------------------------

type lruEntry[V any] struct {
	key   string
	value V
	at    int64
}

type lruCache[V any] struct {
	mu    sync.Mutex
	max   int
	ttlMS int64
	ll    *list.List
	m     map[string]*list.Element
}

func newLRU[V any](max int, ttlMS int64) *lruCache[V] {
	return &lruCache[V]{max: max, ttlMS: ttlMS, m: map[string]*list.Element{}, ll: list.New()}
}

func now() int64 { return time.Now().UnixMilli() }

func (c *lruCache[V]) Get(key string) (V, bool) {
	c.mu.Lock()
	defer c.mu.Unlock()
	var zero V
	el, ok := c.m[key]
	if !ok {
		return zero, false
	}
	e := el.Value.(*lruEntry[V])
	if c.ttlMS > 0 && now()-e.at > c.ttlMS {
		c.ll.Remove(el)
		delete(c.m, key)
		return zero, false
	}
	c.ll.MoveToFront(el)
	return e.value, true
}

func (c *lruCache[V]) Set(key string, v V) {
	c.mu.Lock()
	defer c.mu.Unlock()
	if el, ok := c.m[key]; ok {
		e := el.Value.(*lruEntry[V])
		e.value = v
		e.at = now()
		c.ll.MoveToFront(el)
		return
	}
	el := c.ll.PushFront(&lruEntry[V]{key: key, value: v, at: now()})
	c.m[key] = el
	for c.max > 0 && len(c.m) > c.max {
		back := c.ll.Back()
		if back == nil {
			break
		}
		bk := back.Value.(*lruEntry[V])
		c.ll.Remove(back)
		delete(c.m, bk.key)
	}
}

func (c *lruCache[V]) Delete(key string) {
	c.mu.Lock()
	defer c.mu.Unlock()
	if el, ok := c.m[key]; ok {
		c.ll.Remove(el)
		delete(c.m, key)
	}
}
