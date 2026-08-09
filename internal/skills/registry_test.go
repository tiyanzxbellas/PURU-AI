package skills

import (
	"context"
	"slices"
	"strings"
	"testing"
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
