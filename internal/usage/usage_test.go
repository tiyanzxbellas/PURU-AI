package usage

import (
	"testing"
	"time"
)

func TestProviderLabel(t *testing.T) {
	cases := map[string]string{
		"":                                   "default",
		"https://api.openai.com/v1":          "api.openai.com",
		"http://localhost:3000/v1":           "localhost:3000",
		"https://opencode.ai/zen/v1":         "opencode.ai",
		"https://puruboy-api.vercel.app/api": "puruboy-api.vercel.app",
	}
	for in, want := range cases {
		if got := ProviderLabel(in); got != want {
			t.Fatalf("ProviderLabel(%q) = %q, want %q", in, got, want)
		}
	}
}

func TestKeyOrdering(t *testing.T) {
	// Reverse-epoch key: newer timestamps → smaller key strings.
	k1 := key(parse(t, "2026-02-01T00:00:00Z"))
	k2 := key(parse(t, "2026-03-01T00:00:00Z"))
	if k2 >= k1 {
		t.Fatalf("later time must produce smaller key: k1=%s k2=%s", k1, k2)
	}
}

func TestSummarize(t *testing.T) {
	recs := []Record{
		{Model: "a", Input: 10, Output: 5},
		{Model: "b", Input: 20, Output: 15},
	}
	s := Summarize(recs)
	if s.TotalRequests != 2 || s.TotalInput != 30 || s.TotalOutput != 20 {
		t.Fatalf("bad summary: %+v", s)
	}
	if Summarize(nil).TotalRequests != 0 {
		t.Fatal("empty summary must be zero")
	}
}

func parse(t *testing.T, s string) (ts time.Time) {
	t.Helper()
	var err error
	ts, err = time.Parse(time.RFC3339, s)
	if err != nil {
		t.Fatal(err)
	}
	return ts
}
