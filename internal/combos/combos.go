// Package combos provides per-user "model combos": a named list of models plus
// a selection strategy (fallback / round-robin / fusion). A combo lets a user
// group several model identifiers under one name and pick how the bot uses
// them, mirroring 9router's Combo & Vision Adapter page. Combos are stored per
// chat at combos/{chat}/{id} so they are independent of settings/, fs/ and
// history/.
package combos

import (
	"bytes"
	"context"
	"encoding/json"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/purujawa06-bot/PURU-AI/internal/firebase"
)

// Available strategies.
const (
	StrategyFallback   = "fallback"
	StrategyRoundRobin = "round-robin"
	StrategyFusion     = "fusion"
)

// Combo is a single named model combo.
type Combo struct {
	ID       string   `json:"id"`
	Name     string   `json:"name"`
	Models   []string `json:"models"`
	Strategy string   `json:"strategy"` // fallback | round-robin | fusion
}

// Rotator keeps per-chat selection state. It is safe for concurrent use
// (parallel users).
type Rotator struct {
	mu   sync.Mutex
	turn map[int64]int // chatID -> next round-robin index
}

// Manager reads/writes combos in RTDB with an in-memory cache.
type Manager struct {
	fb    *firebase.Client
	state *Rotator
	cache map[int64]*cacheEntry
	mu    sync.Mutex
	ttl   time.Duration
}

type cacheEntry struct {
	combos []Combo
	at     time.Time
}

func New(fb *firebase.Client, ttl time.Duration) *Manager {
	return &Manager{
		fb:    fb,
		state: &Rotator{turn: map[int64]int{}},
		cache: map[int64]*cacheEntry{},
		ttl:   ttl,
	}
}

// ModelForActive resolves the effective model for a chat using its active
// combo (empty string when none / when the combo has no models). fallbackIndex
// is the current retry attempt-1 (0 = first model) used by fallback/fusion.
func (m *Manager) ModelForActive(ctx context.Context, chatID int64, fallbackIndex int) string {
	combo := m.ActiveCombo(ctx, chatID)
	if combo == nil {
		return ""
	}
	return m.state.ModelFor(chatID, combo, fallbackIndex)
}

func path(chatID int64) string { return "combos/" + strconv.FormatInt(chatID, 10) }

// listPath is where the combos array is stored. It is a child of the per-chat
// document (combos/{chat}/items) so it never collides with the "/active" marker
// that lives alongside it. Storing the array directly at combos/{chat} made it
// impossible to also store the active marker: every PUT of the array replaced
// the whole node and wiped the child, and every SetActive turned the node into
// an object with numeric-index keys that no longer parsed as a slice (regression:
// combos disappear / active combo lost).
func listPath(chatID int64) string { return path(chatID) + "/items" }

func nextID() string { return "c" + strconv.FormatInt(time.Now().UnixNano()/1e6, 36) }

// List returns the combos for a chat (deep copy).
func (m *Manager) List(ctx context.Context, chatID int64) ([]Combo, error) {
	m.mu.Lock()
	if e, ok := m.cache[chatID]; ok && (m.ttl <= 0 || time.Since(e.at) < m.ttl) {
		m.mu.Unlock()
		return cloneList(e.combos), nil
	}
	m.mu.Unlock()

	raw := m.fb.Get(ctx, listPath(chatID))
	if isNullJSON(raw) && m.migrateLegacy(ctx, chatID) {
		raw = m.fb.Get(ctx, listPath(chatID))
	}
	if isNullJSON(raw) {
		m.setCache(chatID, nil)
		return nil, nil
	}
	var combos []Combo
	if err := json.Unmarshal(raw, &combos); err != nil {
		return nil, err
	}
	sort.SliceStable(combos, func(i, j int) bool { return combos[i].Name < combos[j].Name })
	m.setCache(chatID, combos)
	return cloneList(combos), nil
}

// Get returns a single combo by id (nil when absent — empty result).
func (m *Manager) Get(ctx context.Context, chatID int64, id string) *Combo {
	list, err := m.List(ctx, chatID)
	if err != nil {
		return nil
	}
	c := findCombo(list, id)
	if c == nil {
		return nil
	}
	out := *c
	out.Models = append([]string{}, c.Models...)
	return &out
}

// Upsert creates (when in.ID is empty) or updates a combo. Returns the
// persisted combo.
func (m *Manager) Upsert(ctx context.Context, chatID int64, in Combo) (Combo, error) {
	list, _ := m.List(ctx, chatID)
	in.Name = strings.TrimSpace(in.Name)
	if in.Name == "" {
		in.Name = "untitled"
	}
	if in.Strategy == "" {
		in.Strategy = StrategyFallback
	}
	in.Models = cleanModels(in.Models)

	if in.ID == "" {
		// Create: unique id (retry on collision) and unique name.
		for {
			in.ID = nextID()
			if findCombo(list, in.ID) == nil {
				break
			}
		}
		list = append(list, in)
	} else {
		i := indexOf(list, in.ID)
		if i < 0 {
			return Combo{}, errComboNotFound{id: in.ID}
		}
		list[i] = in
	}
	if err := m.save(ctx, chatID, list); err != nil {
		return Combo{}, err
	}
	return in, nil
}

// Delete removes a combo by id, returning (false, nil) when absent.
func (m *Manager) Delete(ctx context.Context, chatID int64, id string) (bool, error) {
	list, _ := m.List(ctx, chatID)
	out := make([]Combo, 0, len(list))
	removed := false
	for _, c := range list {
		if c.ID == id {
			removed = true
			continue
		}
		out = append(out, c)
	}
	if !removed {
		return false, nil
	}
	if err := m.save(ctx, chatID, out); err != nil {
		return false, err
	}
	if m.activeID(ctx, chatID) == id {
		_ = m.SetActive(ctx, chatID, "")
	}
	return true, nil
}

// activePath is where the id of the activated combo lives per chat.
func activePath(chatID int64) string { return path(chatID) + "/active" }

func (m *Manager) activeID(ctx context.Context, chatID int64) string {
	raw := m.fb.Get(ctx, activePath(chatID))
	if len(raw) == 0 {
		return ""
	}
	var s string
	if err := json.Unmarshal(raw, &s); err != nil {
		return ""
	}
	return s
}

// SetActive activates (id) or deactivates (id=="") a combo for the chat.
// Deletion of the active combo deactivates automatically.
func (m *Manager) SetActive(ctx context.Context, chatID int64, id string) error {
	if id == "" {
		return m.fb.Delete(ctx, activePath(chatID))
	}
	return m.fb.Put(ctx, activePath(chatID), id)
}

// ActiveCombo returns the activated combo for the chat or nil when none.
func (m *Manager) ActiveCombo(ctx context.Context, chatID int64) *Combo {
	id := m.activeID(ctx, chatID)
	if id == "" {
		return nil
	}
	return m.Get(ctx, chatID, id)
}

func (m *Manager) save(ctx context.Context, chatID int64, list []Combo) error {
	m.setCache(chatID, list)
	return m.fb.Put(ctx, listPath(chatID), list)
}

// migrateLegacy upgrades an old-format combos document (array stored directly
// at combos/{chat}) to combos/{chat}/items. The legacy node may still carry an
// "active" sibling (old array+active layout) — those children stay untouched,
// and the numeric-index list keys are simply never read again. Returns true
// when list data was recovered.
func (m *Manager) migrateLegacy(ctx context.Context, chatID int64) bool {
	raw := m.fb.Get(ctx, path(chatID))
	if isNullJSON(raw) {
		return false
	}
	legacy, ok := extractComboList(raw)
	if !ok {
		// Malformed leftover (e.g. an active-only node): clear it so a fresh
		// document can be written under the new layout.
		_ = m.fb.Delete(ctx, path(chatID))
		return false
	}
	if err := m.fb.Put(ctx, listPath(chatID), legacy); err != nil {
		return false
	}
	return true
}

// isNullJSON reports whether a raw firebase document is empty or the literal
// JSON null (RTDB returns "null" for absent nodes, and firebase.Client.Get
// passes that through untouched).
func isNullJSON(raw []byte) bool {
	t := bytes.TrimSpace(raw)
	return len(t) == 0 || string(t) == "null"
}

// extractComboList decodes a combos node into a slice. Old data written by the
// array-at-root layout (possibly shared with an "active" child) does not
// unmarshal as []Combo; in that case the numeric-index keys are extracted and
// sorted, ignoring non-numeric children like "active".
func extractComboList(raw []byte) ([]Combo, bool) {
	var list []Combo
	if json.Unmarshal(raw, &list) == nil {
		return list, true
	}
	var obj map[string]json.RawMessage
	if err := json.Unmarshal(raw, &obj); err != nil {
		return nil, false
	}
	keys := make([]int, 0, len(obj))
	for k := range obj {
		if k == "active" {
			continue
		}
		n, err := strconv.Atoi(k)
		if err != nil {
			continue
		}
		keys = append(keys, n)
	}
	if len(keys) == 0 {
		return nil, false
	}
	sort.Ints(keys)
	list = make([]Combo, 0, len(keys))
	for _, n := range keys {
		var c Combo
		if err := json.Unmarshal(obj[strconv.Itoa(n)], &c); err != nil {
			continue
		}
		list = append(list, c)
	}
	return list, true
}

func (m *Manager) setCache(chatID int64, list []Combo) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.cache[chatID] = &cacheEntry{combos: list, at: time.Now()}
}

func findCombo(list []Combo, id string) *Combo {
	for i := range list {
		if list[i].ID == id {
			return &list[i]
		}
	}
	return nil
}

func indexOf(list []Combo, id string) int {
	for i := range list {
		if list[i].ID == id {
			return i
		}
	}
	return -1
}

func cleanModels(models []string) []string {
	out := make([]string, 0, len(models))
	seen := map[string]bool{}
	for _, mo := range models {
		mo = strings.TrimSpace(mo)
		if mo == "" || seen[mo] {
			continue
		}
		seen[mo] = true
		out = append(out, mo)
	}
	return out
}

func cloneList(in []Combo) []Combo {
	out := make([]Combo, len(in))
	for i := range in {
		out[i] = in[i]
		out[i].Models = append([]string{}, in[i].Models...)
	}
	return out
}

// ---------------------------------------------------------------------------
// Runtime selection (round-robin / fallback / fusion)
// ---------------------------------------------------------------------------

// ModelFor resolves the effective model for a chat under a combo strategy.
// fallbackIndex selects the model for fallback/fusion (negative = first).
// Returns "" when the combo has no models or is nil.
func (r *Rotator) ModelFor(chatID int64, combo *Combo, fallbackIndex int) string {
	if combo == nil || len(combo.Models) == 0 {
		return ""
	}
	switch combo.Strategy {
	case StrategyRoundRobin:
		r.mu.Lock()
		i := r.turn[chatID]
		if i < 0 || i >= len(combo.Models) {
			i = 0
		}
		model := combo.Models[i]
		r.turn[chatID] = (i + 1) % len(combo.Models)
		r.mu.Unlock()
		return model
	default: // fallback & fusion (fusion falls back to first model)
		if fallbackIndex < 0 {
			fallbackIndex = 0
		}
		if fallbackIndex >= len(combo.Models) {
			fallbackIndex = 0
		}
		return combo.Models[fallbackIndex]
	}
}

// StrategyCount returns the per-chat round-robin usage count (diagnostics).
func (r *Rotator) StrategyCount(chatID int64) int {
	r.mu.Lock()
	defer r.mu.Unlock()
	return r.turn[chatID]
}

type errComboNotFound struct{ id string }

func (e errComboNotFound) Error() string { return "combo tidak ditemukan: " + e.id }
