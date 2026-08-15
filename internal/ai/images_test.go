package ai

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

var testPNG = []byte{0x89, 'P', 'N', 'G', 0x0D, 0x0A, 0x1A, 0x0A, 0x01, 0x02, 0x03}

func TestDescribeImageSuccess(t *testing.T) {
	var gotBody map[string]any
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/api/ai/gemini-v3" {
			t.Errorf("unexpected path: %s", r.URL.Path)
		}
		if ct := r.Header.Get("Content-Type"); ct != "application/json" {
			t.Errorf("content-type = %q", ct)
		}
		defer r.Body.Close()
		if err := json.NewDecoder(r.Body).Decode(&gotBody); err != nil {
			t.Fatalf("decode request: %v", err)
		}
		w.Write([]byte(`{"candidates":[{"content":{"parts":[{"text":"ada kucing merah"}],"role":"model"}}]}`))
	}))
	defer srv.Close()

	got, err := DescribeImage(context.Background(), srv.Client(), srv.URL+"/api/ai/gemini-v3", "Apa isinya?", testPNG, "image/png")
	if err != nil {
		t.Fatalf("DescribeImage: %v", err)
	}
	if got != "ada kucing merah" {
		t.Fatalf("got %q", got)
	}

	raw, _ := json.Marshal(gotBody)
	if !strings.Contains(string(raw), `"inlineData"`) ||
		!strings.Contains(string(raw), `"data":"`+base64.StdEncoding.EncodeToString(testPNG)+`"`) ||
		!strings.Contains(string(raw), "Apa isinya?") {
		t.Fatalf("request body = %s, want inlineData image + text", raw)
	}
}

func TestDescribeImageErrors(t *testing.T) {
	ctx := context.Background()

	if _, err := DescribeImage(ctx, http.DefaultClient, "", "x", testPNG, "image/png"); err == nil {
		t.Fatal("expected error for empty endpoint")
	}

	srv500 := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusInternalServerError)
		w.Write([]byte("MIDDLEWARE_INVOCATION_FAILED"))
	}))
	defer srv500.Close()
	if _, err := DescribeImage(ctx, srv500.Client(), srv500.URL, "x", testPNG, "image/png"); err == nil || !strings.Contains(err.Error(), "HTTP 500") {
		t.Fatalf("expected HTTP 500 error, got %v", err)
	}

	srvEmpty := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Write([]byte(`{"candidates":[]}`))
	}))
	defer srvEmpty.Close()
	if _, err := DescribeImage(ctx, srvEmpty.Client(), srvEmpty.URL, "x", testPNG, "image/png"); err == nil {
		t.Fatal("expected empty-candidates error")
	}
}

func TestSniffImageMIME(t *testing.T) {
	cases := []struct {
		raw  []byte
		want string
	}{
		{[]byte{0x89, 'P', 'N', 'G', 0x0D, 0x0A, 0x1A, 0x0A, 1, 2}, "image/png"},
		{[]byte{0xFF, 0xD8, 0xFF, 0xE0, 1}, "image/jpeg"},
		{[]byte{'G', 'I', 'F', '8', '9', 'a'}, "image/gif"},
		{[]byte{'R', 'I', 'F', 'F', 0, 0, 0, 0, 'W', 'E', 'B', 'P', 'V', 'P'}, "image/webp"},
		{[]byte("hello"), "application/octet-stream"},
	}
	for _, c := range cases {
		if got := sniffImageMIME(c.raw); got != c.want {
			t.Errorf("sniff(%v) = %q, want %q", c.raw, got, c.want)
		}
	}
}
