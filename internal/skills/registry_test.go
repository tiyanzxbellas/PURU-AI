package skills

import (
	"context"
	"slices"
	"strings"
	"testing"

	pcskills "github.com/sipeed/picoclaw/pkg/skills"
)

func TestListBuiltinSkills(t *testing.T) {
	got := ListBuiltinSkills()
	expected := []string{"github", "skill-creator", "summarize", "weather"}
	for _, name := range expected {
		if !slices.Contains(got, name) {
			t.Errorf("ListBuiltinSkills() missing %q; got %v", name, got)
		}
	}
}

func TestNewRegistryRegistries(t *testing.T) {
	r := NewRegistry(nil, RegistryOptions{})
	if r.manager == nil {
		t.Fatal("NewRegistry(nil,{}) produced nil manager")
	}
	if r.manager.GetRegistry("github") == nil {
		t.Error("github registry should be enabled by default")
	}
	if r.manager.GetRegistry("clawhub") != nil {
		t.Error("clawhub registry should be disabled when not configured")
	}
}

func TestNewRegistryClawHubWhenConfigured(t *testing.T) {
	r := NewRegistry(nil, RegistryOptions{ClawHubToken: "secret"})
	if r.manager.GetRegistry("clawhub") == nil {
		t.Error("clawhub registry should be enabled when ClawHubToken is set")
	}
}

func TestSearchSkillsWithoutToken(t *testing.T) {
	r := NewRegistry(nil, RegistryOptions{})
	_, err := r.SearchSkills(context.Background(), "cuaca")
	if err == nil {
		t.Fatal("expected error when no GITHUB_TOKEN and no clawhub registry")
	}
	if !strings.Contains(strings.ToLower(err.Error()), "github_token") {
		t.Errorf("error should mention GITHUB_TOKEN, got: %v", err)
	}
}

func TestSearchSkillsEmptyQuery(t *testing.T) {
	r := NewRegistry(nil, RegistryOptions{GitHubToken: "test"})
	_, err := r.SearchSkills(context.Background(), "   ")
	if err == nil {
		t.Fatal("expected error for empty query")
	}
}

func TestInstallTargetsRejectedWithoutManager(t *testing.T) {
	r := &Registry{}
	res := r.InstallFromGitHub(context.Background(), 1, "user/repo")
	if res.Success {
		t.Fatal("install should fail when manager is nil")
	}
	if res.Error == "" {
		t.Fatal("expected an error message")
	}
}

func pcResult(reg, slug string, score float64) pcskills.SearchResult {
	return pcskills.SearchResult{Score: score, Slug: slug, DisplayName: slug, RegistryName: reg}
}

func TestInterleaveByRegistryKeepsEveryRegistry(t *testing.T) {
	// Regresi: SearchAll picoclaw men-sort skor global, jadi GitHub (score ~1)
	// selalu kalah dari ClawHub (score ribuan). Interleave harus menaruh hasil
	// kedua registry secara bergantian.
	by := map[string][]pcskills.SearchResult{
		"github": {
			pcResult("github", "acme/weatherapp", 1),
			pcResult("github", "acme2/city", 0.8),
		},
		"clawhub": {
			pcResult("clawhub", "weather", 6120),
			pcResult("clawhub", "forecast", 5100),
			pcResult("clawhub", "sky", 5000),
		},
	}
	got := interleaveByRegistry(by, 0)
	var gh, ch []string
	for _, r := range got {
		if r.RegistryName == "github" {
			gh = append(gh, r.Slug)
		} else {
			ch = append(ch, r.Slug)
		}
	}
	if len(gh) != 2 {
		t.Fatalf("github harus 2 hasil, dapat %v", gh)
	}
	if len(ch) != 3 {
		t.Fatalf("clawhub harus 3 hasil, dapat %v", ch)
	}
	// Kedua registry harus muncul di 2 posisi pertama (round-robin).
	seen := map[string]bool{}
	for _, r := range got[:2] {
		seen[r.RegistryName] = true
	}
	if len(seen) != 2 {
		t.Errorf("dua registry pertama harus beda registry, dapat %v", seen)
	}
}

func TestInterleaveByRegistryRespectsLimit(t *testing.T) {
	by := map[string][]pcskills.SearchResult{
		"github": {
			pcResult("github", "g1", 1),
			pcResult("github", "g2", 1),
			pcResult("github", "g3", 1),
		},
		"clawhub": {
			pcResult("clawhub", "c1", 9000),
			pcResult("clawhub", "c2", 9000),
			pcResult("clawhub", "c3", 9000),
		},
	}
	got := interleaveByRegistry(by, 4)
	if len(got) != 4 {
		t.Fatalf("limit 4 harus menghasilkan 4, dapat %d", len(got))
	}
}

func TestInterleaveByRegistryDedupsWithinRegistry(t *testing.T) {
	// ClawHub mengembalikan skor sama untuk banyak entri slug identik; duplikat
	// dalam satu registry harus dibuang sendiri agar hasil tidak penuh "weather".
	by := map[string][]pcskills.SearchResult{
		"clawhub": {
			pcResult("clawhub", "weather", 6120),
			pcResult("clawhub", "weather", 6120),
			pcResult("clawhub", "forecast", 5100),
		},
	}
	got := interleaveByRegistry(by, 0)
	if len(got) != 2 {
		t.Fatalf("harus 2 hasil unik, dapat %d (%v)", len(got), got)
	}
}
