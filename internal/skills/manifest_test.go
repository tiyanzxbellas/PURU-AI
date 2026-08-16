package skills

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"

	"github.com/purujawa06-bot/PURU-AI/internal/firebase"
	"github.com/purujawa06-bot/PURU-AI/internal/vfs"
)

// fakeRTDB mimics the Firebase REST semantics the VFS relies on (missing node
// GET returns literal "null", PUT replaces the node and wipes any descendant
// keys like real RTDB, PATCH merges without wiping descendants, DELETE removes
// the node).
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
		if r.URL.Query().Get("shallow") == "true" {
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
			var body any
			if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
				w.WriteHeader(http.StatusBadRequest)
				return
			}
			raw, _ := json.Marshal(body)
			f.db[key] = string(raw)
			if r.Method == http.MethodPut {
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

func testCatalog(t *testing.T) *Catalog {
	t.Helper()
	srv := httptest.NewServer(newFakeRTDB().handler())
	t.Cleanup(srv.Close)
	return NewCatalog(vfs.New(firebase.New(srv.URL, srv.Client())))
}

func TestDeleteSkillRemovesWholeTree(t *testing.T) {
	c := testCatalog(t)
	ctx := context.Background()
	v := c.vfs
	if err := v.WriteFile(ctx, 1, "skills/pdf/SKILL.md", "---\nname: pdf\n---\n# PDF\nbody"); err != nil {
		t.Fatalf("write SKILL.md: %v", err)
	}
	if err := v.WriteFile(ctx, 1, "skills/pdf/scripts/a.py", "print(1)"); err != nil {
		t.Fatalf("write a.py: %v", err)
	}
	if err := v.WriteFile(ctx, 1, "skills/pdf/.skill-origin.json", "{}"); err != nil {
		t.Fatalf("write origin: %v", err)
	}
	if err := v.WriteFile(ctx, 1, "skills/other/SKILL.md", "---\nname: other\n---\n# Other\n"); err != nil {
		t.Fatalf("write other: %v", err)
	}

	// Regression: DeleteSkill must remove the whole skill subtree (SKILL.md +
	// scripts + origin meta), not just SKILL.md like the old tool did, and it
	// must not touch sibling skills.
	deleted, err := c.DeleteSkill(ctx, 1, "pdf")
	if err != nil {
		t.Fatalf("DeleteSkill: %v", err)
	}
	if !deleted {
		t.Fatal("DeleteSkill of an existing skill reported false")
	}
	if _, ok := v.ReadFile(ctx, 1, "skills/pdf/SKILL.md"); ok {
		t.Error("SKILL.md still present after DeleteSkill")
	}
	if _, ok := v.ReadFile(ctx, 1, "skills/pdf/scripts/a.py"); ok {
		t.Error("scripts/a.py still present after DeleteSkill")
	}
	if _, ok := v.ReadFile(ctx, 1, "skills/pdf/.skill-origin.json"); ok {
		t.Error(".skill-origin.json still present after DeleteSkill")
	}
	if entries := v.ListDirectory(ctx, 1, "skills"); len(entries) != 1 || entries[0].Name != "other" {
		t.Errorf("sibling skill affected: skills = %v", entries)
	}
	if _, ok := v.ReadFile(ctx, 1, "skills/other/SKILL.md"); !ok {
		t.Error("sibling skill content was removed")
	}
}

// Regression: the skill list shows the manifest name (frontmatter name or
// "# title"), which may differ from the installed directory name. Deleting by
// the displayed name must resolve to the right directory instead of reporting
// "not found".
func TestDeleteSkillByDisplayName(t *testing.T) {
	c := testCatalog(t)
	ctx := context.Background()
	v := c.vfs
	if err := v.WriteFile(ctx, 1, "skills/actual-dir/SKILL.md",
		"---\nname: fancy-name\n---\n# Fancy Name\nbody"); err != nil {
		t.Fatalf("write SKILL.md: %v", err)
	}
	if err := v.WriteFile(ctx, 1, "skills/actual-dir/scripts/x.sh", "echo hi"); err != nil {
		t.Fatalf("write x.sh: %v", err)
	}
	// The list would display "fancy-name", which is not a skills/ subdir.
	list := c.ListSkills(ctx, 1)
	if len(list) != 1 || list[0].Name != "fancy-name" {
		t.Fatalf("ListSkills = %+v", list)
	}

	deleted, err := c.DeleteSkill(ctx, 1, "fancy-name")
	if err != nil || !deleted {
		t.Fatalf("DeleteSkill by display name: deleted=%v err=%v", deleted, err)
	}
	if _, ok := v.ReadFile(ctx, 1, "skills/actual-dir/SKILL.md"); ok {
		t.Error("SKILL.md still present after DeleteSkill by display name")
	}
	if entries := v.ListDirectory(ctx, 1, "skills"); len(entries) != 0 {
		t.Errorf("skill dir not cleaned: %+v", entries)
	}
}

func TestDeleteSkillFlatFileAndMissing(t *testing.T) {
	c := testCatalog(t)
	ctx := context.Background()
	v := c.vfs
	if err := v.WriteFile(ctx, 1, "skills/legacy.md", "---\nname: legacy\n---\n# Legacy\n"); err != nil {
		t.Fatalf("write legacy.md: %v", err)
	}
	deleted, err := c.DeleteSkill(ctx, 1, "legacy")
	if err != nil || !deleted {
		t.Fatalf("DeleteSkill(legacy.md): deleted=%v err=%v", deleted, err)
	}
	if _, ok := v.ReadFile(ctx, 1, "skills/legacy.md"); ok {
		t.Error("legacy.md still present after DeleteSkill")
	}

	if deleted, _ := c.DeleteSkill(ctx, 1, "tidak-ada"); deleted {
		t.Error("DeleteSkill of a missing skill reported success")
	}
}
