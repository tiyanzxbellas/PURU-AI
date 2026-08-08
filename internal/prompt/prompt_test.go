package prompt

import (
	"strings"
	"testing"
)

func TestGetRendersLiteralBraces(t *testing.T) {
	out, err := Get("memory-x", "skills-y")
	if err != nil {
		t.Fatalf("template error: %v", err)
	}
	if !strings.Contains(out, "/skills/{{name}}/SKILL.md") {
		t.Fatalf("literal braces not preserved: %s", out)
	}
	if !strings.Contains(out, "memory-x") {
		t.Fatalf("memory not injected")
	}
	if !strings.Contains(out, "skills-y") {
		t.Fatalf("skills not injected")
	}
}
