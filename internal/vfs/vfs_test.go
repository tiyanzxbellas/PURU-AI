package vfs

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

// fakeRTDB replicates the Firebase REST semantics the VFS relies on: a missing
// node GET returns HTTP 200 with the literal body "null" (not an empty body),
// PUT stores raw JSON, and DELETE removes a node (missing delete → "null").
type fakeRTDB struct {
	mu sync.Mutex
	db map[string]string // key -> raw JSON value
}

func newFakeRTDB() *fakeRTDB {
	return &fakeRTDB{db: map[string]string{}}
}

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
			// The VFS PUTs both string file bodies ("halo") and index objects
			// ({"entries":[...]}) — accept any valid JSON.
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

func testVFS(t *testing.T) *VFS {
	t.Helper()
	srv := httptest.NewServer(newFakeRTDB().handler())
	t.Cleanup(srv.Close)
	return New(firebase.New(srv.URL, srv.Client()))
}

func TestDeleteFileMissing(t *testing.T) {
	v := testVFS(t)
	ok, err := v.DeleteFile(context.Background(), 1, "tidak-ada.txt")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	// Regression: RTDB returns literal "null" for a missing node; DeleteFile
	// must treat it as absent and report not-found (false, nil), NOT a phantom
	// success (true).
	if ok {
		t.Fatal("DeleteFile of a missing file reported success=true (phantom delete)")
	}
}

func TestWriteReadDeleteRoundtrip(t *testing.T) {
	v := testVFS(t)
	ctx := context.Background()
	if err := v.WriteFile(ctx, 1, "a.txt", "halo"); err != nil {
		t.Fatalf("write: %v", err)
	}
	content, ok := v.ReadFile(ctx, 1, "a.txt")
	if !ok || content != "halo" {
		t.Fatalf("read after write: ok=%v content=%q", ok, content)
	}
	deleted, err := v.DeleteFile(ctx, 1, "a.txt")
	if err != nil || !deleted {
		t.Fatalf("delete existing: ok=%v err=%v", deleted, err)
	}
	if _, ok := v.ReadFile(ctx, 1, "a.txt"); ok {
		t.Fatal("file still readable after delete")
	}
	if ok, _ := v.DeleteFile(ctx, 1, "a.txt"); ok {
		t.Fatal("second delete of missing file reported success")
	}
}
