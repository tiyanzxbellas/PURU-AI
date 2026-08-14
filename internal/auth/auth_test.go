package auth

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"

	"github.com/purujawa06-bot/PURU-AI/internal/firebase"
)

// fakeRTDB mimics the Firebase REST semantics the store relies on (missing
// node GET returns literal "null", PUT stores raw JSON, DELETE removes node).
type fakeRTDB struct {
	mu sync.Mutex
	db map[string]string
}

func newFakeRTDB() *fakeRTDB { return &fakeRTDB{db: map[string]string{}} }

func (f *fakeRTDB) handler() http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		f.mu.Lock()
		defer f.mu.Unlock()
		key := strings.TrimSuffix(strings.TrimPrefix(r.URL.Path, "/"), ".json")
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
			w.Write([]byte(raw))
		case http.MethodDelete:
			delete(f.db, key)
			w.Write([]byte("null"))
		default:
			w.WriteHeader(http.StatusMethodNotAllowed)
		}
	})
}

func newManager(t *testing.T) *Manager {
	t.Helper()
	srv := httptest.NewServer(newFakeRTDB().handler())
	t.Cleanup(srv.Close)
	return New(firebase.New(srv.URL, srv.Client()))
}

func TestSetGetVerify(t *testing.T) {
	m := newManager(t)
	ctx := context.Background()

	if m.Has(ctx, 42) {
		t.Fatal("no password should be set initially")
	}
	if m.Verify(ctx, 42, "apa-aja") {
		t.Fatal("verify must fail when no password exists")
	}

	if err := m.Set(ctx, 42, "rahasia123"); err != nil {
		t.Fatalf("set: %v", err)
	}
	if !m.Has(ctx, 42) {
		t.Fatal("password should be set after Set")
	}
	if got := m.Get(ctx, 42); got != "rahasia123" {
		t.Fatalf("Get = %q, want rahasia123", got)
	}
	if !m.Verify(ctx, 42, "rahasia123") {
		t.Fatal("correct password must verify")
	}
	if m.Verify(ctx, 42, "salah") {
		t.Fatal("wrong password must not verify")
	}

	if err := m.Delete(ctx, 42); err != nil {
		t.Fatalf("delete: %v", err)
	}
	if m.Has(ctx, 42) {
		t.Fatal("password should be gone after Delete")
	}
}

func TestPerUserIsolation(t *testing.T) {
	m := newManager(t)
	ctx := context.Background()
	_ = m.Set(ctx, 1, "pw-satu")
	_ = m.Set(ctx, 2, "pw-dua")

	if m.Get(ctx, 1) != "pw-satu" || m.Get(ctx, 2) != "pw-dua" {
		t.Fatal("users must have independent passwords")
	}
}

func TestValidPassword(t *testing.T) {
	cases := []struct {
		pw string
		ok bool
	}{
		{"abcd", true},
		{"ab1_-d", true},
		{"abc", false},
		{"a b c", false},
		{"", false},
		{"with/space", false},
		{"panjang-sangat_aman-99", true},
	}
	for _, c := range cases {
		if got := ValidPassword(c.pw); got != c.ok {
			t.Errorf("ValidPassword(%q) = %v, want %v", c.pw, got, c.ok)
		}
	}
}
