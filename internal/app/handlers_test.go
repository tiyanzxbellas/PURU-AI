package app

import "testing"

func TestSplitCommand(t *testing.T) {
	cases := []struct {
		text     string
		cmd, rest string
	}{
		{"/menu", "/menu", ""},
		{"/menu@puru_code_bot", "/menu", ""},
		{"/menu@puru_code_bot xyz", "/menu", "xyz"},
		{"/ai apa kabar", "/ai", "apa kabar"},
		{"/ai@puru_code_bot apa kabar", "/ai", "apa kabar"},
		{"/skills@bot search go", "/skills", "search go"},
		{"/config@bot model gpt-4o", "/config", "model gpt-4o"},
		{"/reset@bot chat", "/reset", "chat"},
		{"halo", "halo", ""},
		{"halo semua", "halo", "semua"},
		{"", "", ""},
	}
	for _, tc := range cases {
		cmd, rest := splitCommand(tc.text)
		if cmd != tc.cmd || rest != tc.rest {
			t.Errorf("splitCommand(%q) = (%q, %q), want (%q, %q)", tc.text, cmd, rest, tc.cmd, tc.rest)
		}
	}
}

func TestIsCommandText(t *testing.T) {
	for _, tc := range []struct {
		text string
		want bool
	}{
		{"/menu@puru_code_bot", true},
		{"/ai halo", true},
		{"/", true},
		{"halo", false},
		{"", false},
	} {
		if got := isCommandText(tc.text); got != tc.want {
			t.Errorf("isCommandText(%q) = %v, want %v", tc.text, got, tc.want)
		}
	}
}

func TestKnownCommandsAcceptSuffix(t *testing.T) {
	for _, c := range knownCommands {
		if !isKnownCommand(c) {
			t.Fatalf("known command %q not recognized", c)
		}
		cmd, _ := splitCommand(c + "@purubot")
		if cmd != c {
			t.Errorf("splitCommand(%q) = %q, want %q", c+"@purubot", cmd, c)
		}
	}
}