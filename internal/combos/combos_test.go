package combos

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/purujawa06-bot/PURU-AI/internal/firebase"
)

// fakeRTDB mimics Firebase REST semantics (GET->null, PUT replaces node w/ children).
type fakeRTDB struct {
	db map[string]string
}

func newFakeRTDB() *fakeRTDB { return &fakeRTDB{db: map[string]string{}} }

func (f *fakeRTDB) handler() http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		key := r.URL.Path
		key = key[1 : len(key)-len(".json")]
		switch r.Method {
		case http.MethodGet:
			if v, ok := f.db[key]; ok {
				w.Write([]byte(v))
			} else {
				w.Write([]byte("null"))
			}
		case http.MethodPut:
			var body any
			if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
				w.WriteHeader(http.StatusBadRequest)
				return
			}
			raw, _ := json.Marshal(body)
			f.db[key] = string(raw)
			for k := range f.db {
				if len(k) > len(key) && k[:len(key)] == key && k[len(key)] == '/' {
					delete(f.db, k)
				}
			}
			w.Write(raw)
		case http.MethodDelete:
			for k := range f.db {
				if k == key || (len(k) > len(key) && k[:len(key)+1] == key+"/") {
					delete(f.db, k)
				}
			}
			w.Write([]byte("null"))
		default:
			w.WriteHeader(http.StatusMethodNotAllowed)
		}
	})
}

func newEnv(t *testing.T) *Manager {
	t.Helper()
	rt := httptest.NewServer(newFakeRTDB().handler())
	t.Cleanup(rt.Close)
	fb := firebase.New(rt.URL, rt.Client())
	return New(fb, time.Hour)
}

func combo(id string) Combo {
	return Combo{ID: id, Name: "c-" + id, Models: []string{"oc/m1"}, Strategy: StrategyFallback}
}

func TestCleanModels(t *testing.T) {
	got := cleanModels([]string{" a ", "", "b", "b", "  c\t"})
	if len(got) != 3 || got[0] != "a" || got[1] != "b" || got[2] != "c" {
		t.Fatalf("cleanModels = %q", got)
	}
	if got := cleanModels(nil); got != nil && len(got) != 0 {
		t.Fatalf("nil -> %q", got)
	}
}

func TestCloneList(t *testing.T) {
	orig := []Combo{{ID: "1", Models: []string{"a", "b"}}}
	c := cloneList(orig)
	if c[0].ID != "1" || len(c[0].Models) != 2 {
		t.Fatalf("clone broken: %+v", c)
	}
	c[0].Models[0] = "zz"
	if orig[0].Models[0] != "a" {
		t.Fatal("clone must deep-copy models")
	}
}

func TestRotatorRoundRobin(t *testing.T) {
	r := &Rotator{turn: map[int64]int{}}
	combo := &Combo{Models: []string{"m1", "m2", "m3"}, Strategy: StrategyRoundRobin}
	order := []string{}
	for i := 0; i < 6; i++ {
		order = append(order, r.ModelFor(7, combo, -1))
	}
	want := []string{"m1", "m2", "m3", "m1", "m2", "m3"}
	for i := range want {
		if order[i] != want[i] {
			t.Fatalf("round robin seq wrong: %v", order)
		}
	}
	if r.StrategyCount(7) != 0 {
		t.Fatalf("count = %d (expect wrapped to 0 after 6 calls / 3 models)", r.StrategyCount(7))
	}
}

func TestRotatorFallback(t *testing.T) {
	r := &Rotator{turn: map[int64]int{}}
	combo := &Combo{Models: []string{"m1", "m2"}, Strategy: StrategyFallback}
	if got := r.ModelFor(1, combo, -1); got != "m1" {
		t.Fatalf("first fallback = %s", got)
	}
	if got := r.ModelFor(1, combo, 1); got != "m2" {
		t.Fatalf("fallback index 1 = %s", got)
	}
	if got := r.ModelFor(1, combo, 99); got != "m1" {
		t.Fatalf("fallback overflow = %s", got)
	}
}

func TestRotatorEmpty(t *testing.T) {
	r := &Rotator{turn: map[int64]int{}}
	if got := r.ModelFor(1, nil, -1); got != "" {
		t.Fatalf("nil combo = %q", got)
	}
	if got := r.ModelFor(1, &Combo{Models: nil}, -1); got != "" {
		t.Fatalf("empty combo = %q", got)
	}
}

func TestComboJSON(t *testing.T) {
	raw := `{"id":"c1","name":"My Combo","models":["m1","m2"],"strategy":"round-robin"}`
	var c Combo
	if err := json.Unmarshal([]byte(raw), &c); err != nil {
		t.Fatal(err)
	}
	if c.ID != "c1" || c.Name != "My Combo" || c.Strategy != StrategyRoundRobin || len(c.Models) != 2 {
		t.Fatalf("bad decode: %+v", c)
	}
}

func TestFindAndIndex(t *testing.T) {
	list := []Combo{{ID: "a"}, {ID: "b"}}
	if findCombo(list, "b") == nil || findCombo(list, "z") != nil {
		t.Fatal("findCombo wrong")
	}
	if indexOf(list, "a") != 0 || indexOf(list, "z") != -1 {
		t.Fatal("indexOf wrong")
	}
	if (errComboNotFound{id: "x"}).Error() == "" {
		t.Fatal("empty err message")
	}
}

// Activating a combo must not corrupt the list (regression: SetActive turned
// the combos/{chat} array node into an object with an "active" sibling, so the
// next List unable to unmarshal -> combos disappear).
func TestActivateKeepsListIntact(t *testing.T) {
	m := newEnv(t)
	ctx := context.Background()
	c1, err := m.Upsert(ctx, 7, combo(""))
	if err != nil {
		t.Fatal(err)
	}
	_, err = m.Upsert(ctx, 7, combo(""))
	if err != nil {
		t.Fatal(err)
	}
	if err := m.SetActive(ctx, 7, c1.ID); err != nil {
		t.Fatal(err)
	}
	list, err := m.List(ctx, 7)
	if err != nil {
		t.Fatalf("list after activate: %v", err)
	}
	if len(list) != 2 {
		t.Fatalf("list len = %d, want 2", len(list))
	}
	ac := m.ActiveCombo(ctx, 7)
	if ac == nil || ac.ID != c1.ID {
		t.Fatalf("active combo lost after activate: %+v", ac)
	}
}

// Saving (Upsert/Delete) any combo must not wipe the active marker (regression:
// save used PUT on the whole combos/{chat} node, replacing the "active" child).
func TestSaveKeepsActiveCombo(t *testing.T) {
	m := newEnv(t)
	ctx := context.Background()
	c1, err := m.Upsert(ctx, 9, combo(""))
	if err != nil {
		t.Fatal(err)
	}
	if err := m.SetActive(ctx, 9, c1.ID); err != nil {
		t.Fatal(err)
	}
	if _, err := m.Upsert(ctx, 9, combo("")); err != nil {
		t.Fatal(err)
	}
	ac := m.ActiveCombo(ctx, 9)
	if ac == nil || ac.ID != c1.ID {
		t.Fatalf("active combo wiped by upsert: %+v", ac)
	}
	if _, err := m.Delete(ctx, 9, c1.ID); err != nil {
		t.Fatal(err)
	}
	if ac := m.ActiveCombo(ctx, 9); ac != nil {
		t.Fatalf("deleting the active combo should deactivate it, got %+v", ac)
	}
}

// Old-format data (array at combos/{chat}, optionally with an "active" sibling
// from the buggy layout) must migrate to combos/{chat}/items transparently.
func TestMigrateLegacyLayout(t *testing.T) {
	ctx := context.Background()

	// 1. Clean array at the root.
	f := newFakeRTDB()
	f.db["combos/7"] = `[{"id":"c1","name":"A","models":["oc/m1"],"strategy":"fallback"},{"id":"c2","name":"B","models":["oc/m2"],"strategy":"round-robin"}]`
	rt := httptest.NewServer(f.handler())
	t.Cleanup(rt.Close)
	m := New(firebase.New(rt.URL, rt.Client()), time.Hour)
	list, err := m.List(ctx, 7)
	if err != nil {
		t.Fatalf("migrate list: %v", err)
	}
	if len(list) != 2 {
		t.Fatalf("migrated len = %d, want 2", len(list))
	}
	if _, ok := f.db["combos/7/items"]; !ok {
		t.Fatal("legacy data not moved to combos/{chat}/items")
	}

	// 2. Mixed node (array-with-"active" from the old SetActive bug) recovers
	// the combos via numeric-index extraction and keeps the active marker.
	f2 := newFakeRTDB()
	f2.db["combos/8"] = `{"0":{"id":"c1","name":"A","models":["oc/m1"],"strategy":"fallback"},"active":"c1"}`
	f2.db["combos/8/active"] = `"c1"`
	rt2 := httptest.NewServer(f2.handler())
	t.Cleanup(rt2.Close)
	m2 := New(firebase.New(rt2.URL, rt2.Client()), time.Hour)
	list, err = m2.List(ctx, 8)
	if err != nil {
		t.Fatalf("mixed migrate list: %v", err)
	}
	if len(list) != 1 || list[0].ID != "c1" {
		t.Fatalf("mixed migrate recovered = %+v", list)
	}
	if ac := m2.ActiveCombo(ctx, 8); ac == nil || ac.ID != "c1" {
		t.Fatalf("active lost in mixed migrate: %+v", ac)
	}
}

func TestExtractComboList(t *testing.T) {
	if _, ok := extractComboList([]byte(`["not-a-combo"]`)); ok {
		t.Fatal("garbage should fail")
	}
	// Legacy empty array still migrates.
	list, ok := extractComboList([]byte(`[]`))
	if !ok || len(list) != 0 {
		t.Fatalf("empty array = %+v %v", list, ok)
	}
}
