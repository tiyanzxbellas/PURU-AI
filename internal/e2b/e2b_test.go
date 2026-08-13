package e2b

import "testing"

func TestNormalizeLanguage(t *testing.T) {
	cases := map[string]string{
		"python":     "python",
		"py":         "python",
		"python3":    "python",
		"node":       "javascript",
		"nodejs":     "javascript",
		"js":         "javascript",
		"javascript": "javascript",
		"Node":       "javascript",
		"SheLL":      "SheLL", // alias tak dikenal = dibiarkan
	}
	for in, want := range cases {
		if got := normalizeLanguage(in); got != want {
			t.Errorf("normalizeLanguage(%q) = %q, want %q", in, got, want)
		}
	}
}

func TestWrapJavaScript(t *testing.T) {
	got := wrapJavaScript("console.log('hi')")
	want := "{\nconsole.log('hi')\n}\n"
	if got != want {
		t.Errorf("wrapJavaScript = %q, want %q", got, want)
	}
	// Body kode harus tetap tidak ter-modify; variabel dibiarkan di dalam blok.
}

func TestWrapJavaScriptScopeIsolation(t *testing.T) {
	// Blok { } harus mengisolasi `const` dari scope persisten kernel JS E2B:
	// dua payload berbeda yang sama-sama mendeklarasikan `const a` akan
	// SyntaxError "already declared" di global scope, tapi aman bila di-blok.
	a := wrapJavaScript("const a = 1; console.log(a)")
	b := wrapJavaScript("const a = 2; console.log(a)")
	if a == b {
		t.Fatalf("wrapped payloads harus berbeda")
	}
	for i, w := range []string{a, b} {
		if len(w) < 2 || w[0] != '{' || w[len(w)-2:] != "}\n" {
			t.Errorf("payload %d tidak dibungkus blok: %q", i, w)
		}
	}
}
