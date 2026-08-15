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
// PUT stores raw JSON and (like real RTDB) REPLACES the whole node — deleting
// any descendant keys, PATCH merges the given object into the node WITHOUT
// touching descendants, and DELETE removes a node (missing delete → "null").
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
		if r.URL.Query().Get("shallow") == "true" {
			// shallow=true: respond with the direct child keys of the node.
			prefix := key + "/"
			out := map[string]bool{}
			for k := range f.db {
				if k == key {
					continue
				}
				if strings.HasPrefix(k, prefix) {
					rest := k[len(prefix):]
					if i := strings.Index(rest, "/"); i < 0 {
						out[rest] = true
					} else {
						out[rest[:i]] = true
					}
				}
			}
			if len(out) == 0 {
				w.Write([]byte("null"))
				return
			}
			raw, _ := json.Marshal(out)
			w.Write(raw)
			return
		}
		switch r.Method {
		case http.MethodGet:
			if v, ok := f.db[key]; ok {
				w.Write([]byte(v))
			} else {
				w.Write([]byte("null"))
			}
		case http.MethodPut, http.MethodPatch:
			// The VFS writes both string file bodies ("halo") and index objects
			// ({"entries":[...]}) — accept any valid JSON.
			var body any
			if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
				w.WriteHeader(http.StatusBadRequest)
				return
			}
			raw, _ := json.Marshal(body)
			f.db[key] = string(raw)
			if r.Method == http.MethodPut {
				// Real RTDB: a PUT replaces the node and removes its children.
				for k := range f.db {
					if strings.HasPrefix(k, key+"/") {
						delete(f.db, k)
					}
				}
			}
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

func TestDeleteDirRecursive(t *testing.T) {
	v := testVFS(t)
	ctx := context.Background()
	files := []string{
		"skills/pdf/SKILL.md",
		"skills/pdf/scripts/a.py",
		"skills/pdf/scripts/sub/b.py",
		"skills/pdf/.skill-origin.json",
		"skills/other/SKILL.md",
	}
	for _, f := range files {
		if err := v.WriteFile(ctx, 1, f, "x"); err != nil {
			t.Fatalf("write %s: %v", f, err)
		}
	}

	// Regression: deleting a skill directory must remove the whole subtree
	// (nested dirs + files + index nodes) and the parent entry, leaving no
	// empty-directory residue like the old non-recursive cleanup did.
	deleted, err := v.DeleteDir(ctx, 1, "skills/pdf")
	if err != nil {
		t.Fatalf("DeleteDir: %v", err)
	}
	if !deleted {
		t.Fatal("DeleteDir of an existing directory reported false")
	}
	for _, f := range []string{
		"skills/pdf/SKILL.md",
		"skills/pdf/scripts/a.py",
		"skills/pdf/scripts/sub/b.py",
		"skills/pdf/.skill-origin.json",
	} {
		if _, ok := v.ReadFile(ctx, 1, f); ok {
			t.Errorf("file %q still readable after DeleteDir", f)
		}
	}
	for _, dir := range []string{"skills/pdf", "skills/pdf/scripts", "skills/pdf/scripts/sub"} {
		if entries := v.ListDirectory(ctx, 1, dir); len(entries) != 0 {
			t.Errorf("dir %q still listed after DeleteDir: %v", dir, entries)
		}
	}
	if entries := v.ListDirectory(ctx, 1, "skills"); len(entries) != 1 || entries[0].Name != "other" {
		t.Errorf("sibling skill affected: skills = %v", entries)
	}

	// Deleting again reports not-found (no phantom success).
	if ok, _ := v.DeleteDir(ctx, 1, "skills/pdf"); ok {
		t.Fatal("second DeleteDir of missing dir reported success")
	}
}

func TestDeleteDirRemovesOrphanedContent(t *testing.T) {
	v := testVFS(t)
	ctx := context.Background()
	if err := v.WriteFile(ctx, 1, "skills/pdf/SKILL.md", "konten skill"); err != nil {
		t.Fatalf("write SKILL.md: %v", err)
	}
	if err := v.WriteFile(ctx, 1, "skills/pdf/scripts/a.py", "print(1)"); err != nil {
		t.Fatalf("write a.py: %v", err)
	}
	// Simulate the install-time index race: the skills/pdf index node loses its
	// SKILL.md entry while the content node survives (orphaned file).
	ip := indexPath(1, "skills/pdf")
	doc := readIndex(ctx, v.fb, ip)
	filtered := doc.Entries[:0]
	for _, e := range doc.Entries {
		if e.Name != "SKILL.md" {
			filtered = append(filtered, e)
		}
	}
	doc.Entries = filtered
	if err := writeIndex(ctx, v.fb, ip, doc); err != nil {
		t.Fatalf("simulate lost index entry: %v", err)
	}

	// Regression: DeleteDir must delete the orphaned SKILL.md content even
	// though it is absent from the directory index (scan of the content store).
	deleted, err := v.DeleteDir(ctx, 1, "skills/pdf")
	if err != nil {
		t.Fatalf("DeleteDir: %v", err)
	}
	if !deleted {
		t.Fatal("DeleteDir reported false for a directory with content")
	}
	if _, ok := v.ReadFile(ctx, 1, "skills/pdf/SKILL.md"); ok {
		t.Error("orphaned SKILL.md content still readable after DeleteDir")
	}
	if _, ok := v.ReadFile(ctx, 1, "skills/pdf/scripts/a.py"); ok {
		t.Error("a.py still readable after DeleteDir")
	}
	if entries := v.ListDirectory(ctx, 1, "skills"); len(entries) != 0 {
		t.Errorf("skills dir still listed after DeleteDir: %v", entries)
	}
}

func TestWriteIndexPatchKeepsSubdirIndices(t *testing.T) {
	v := testVFS(t)
	ctx := context.Background()

	// Install a nested skill, then write an unrelated root-level path. Both go
	// through writeIndex on the ROOT index node (via ensureAncestors). With a
	// plain PUT the root write would replace fs/{id}/index and wipe every
	// per-directory index node below it — making every folder list empty.
	// Regression: writeIndex must PATCH (merge) so the children survive.
	if err := v.WriteFile(ctx, 1, "skills/pdf/SKILL.md", "konten skill"); err != nil {
		t.Fatalf("write skill: %v", err)
	}
	if err := v.WriteFile(ctx, 1, "memory/MEMORY.md", "1. ingatan"); err != nil {
		t.Fatalf("write memory: %v", err)
	}

	if entries := v.ListDirectory(ctx, 1, "skills/pdf"); len(entries) != 1 || entries[0].Name != "SKILL.md" {
		t.Fatalf("subdir listing wiped by later root write: skills/pdf = %v", entries)
	}
	if entries := v.ListDirectory(ctx, 1, "skills"); len(entries) != 1 || entries[0].Name != "pdf" {
		t.Fatalf("skills dir broken: %v", entries)
	}
	root := map[string]bool{}
	for _, e := range v.ListDirectory(ctx, 1, "") {
		root[e.Name] = true
	}
	if !root["skills"] || !root["memory"] {
		t.Fatalf("root index missing entries: %v", root)
	}
}
