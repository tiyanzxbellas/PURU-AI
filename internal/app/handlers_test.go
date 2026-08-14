package app

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/purujawa06-bot/PURU-AI/internal/telegram"
)

func TestSplitCommand(t *testing.T) {
	cases := []struct {
		text      string
		cmd, rest string
	}{
		{"/menu", "/menu", ""},
		{"/menu@puru_code_bot", "/menu", ""},
		{"/menu@puru_code_bot xyz", "/menu", "xyz"},
		{"/ai apa kabar", "/ai", "apa kabar"},
		{"/ai@puru_code_bot apa kabar", "/ai", "apa kabar"},
		{"/login@puru_code_bot", "/login", ""},
		{"/pw@puru_code_bot MyPass123", "/pw", "MyPass123"},
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

// mockTelegram spins up a fake Telegram Bot API server and returns the API
// client wired to it. sendMessage-like methods answer ok.
func mockTelegram(t *testing.T, onReq func(r *http.Request)) *telegram.API {
	t.Helper()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if onReq != nil {
			onReq(r)
		}
		_ = r.ParseForm()
		if strings.HasSuffix(r.URL.Path, "/sendMessage") ||
			strings.HasSuffix(r.URL.Path, "/sendDocument") ||
			strings.HasSuffix(r.URL.Path, "/editMessageText") ||
			strings.HasSuffix(r.URL.Path, "/deleteMessage") {
			w.Write([]byte(`{"ok":true,"result":{"message_id":1}}`))
			return
		}
		http.Error(w, "not found", http.StatusNotFound)
	}))
	t.Cleanup(srv.Close)
	old := telegram.BaseURL
	telegram.BaseURL = srv.URL
	t.Cleanup(func() { telegram.BaseURL = old })
	return telegram.New("test-token", srv.Client())
}

func TestHandleTextAIBannedInPrivateChat(t *testing.T) {
	var sentText string
	tg := mockTelegram(t, func(r *http.Request) {
		if strings.HasSuffix(r.URL.Path, "/sendMessage") {
			sentText = r.FormValue("text")
		}
	})
	a := &App{tg: tg}
	msg := &telegram.Message{
		MessageID: 1,
		From:      &telegram.User{ID: 7},
		Chat:      &telegram.Chat{ID: 7, Type: "private"},
		Text:      "/ai halo",
	}
	if err := a.handleText(context.Background(), msg); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(sentText, "Tidak perlu /ai") {
		t.Fatalf("expected private-chat /ai ban reply, got %q", sentText)
	}
}

func TestHandleDocumentGroupRequiresAICommand(t *testing.T) {
	var gotPaths []string
	tg := mockTelegram(t, func(r *http.Request) { gotPaths = append(gotPaths, r.URL.Path) })
	a := &App{tg: tg}
	msg := &telegram.Message{
		MessageID: 1,
		From:      &telegram.User{ID: 7},
		Chat:      &telegram.Chat{ID: -100, Type: "supergroup"},
		Document:  &telegram.Document{FileID: "f1", FileName: "a.txt", FileSize: 100},
	}

	// Without /ai caption: ignored entirely, no Telegram call.
	msg.Caption = "halo"
	if err := a.handleDocument(context.Background(), msg); err != nil {
		t.Fatal(err)
	}
	if len(gotPaths) != 0 {
		t.Fatalf("group document without /ai should be ignored, but server was hit: %v", gotPaths)
	}

	// With /ai caption: proceeds past the gate and downloads the file.
	gotPaths = nil
	msg.Caption = "/ai analisis file ini"
	if err := a.handleDocument(context.Background(), msg); err != nil {
		t.Fatal(err)
	}
	found := false
	for _, p := range gotPaths {
		if strings.Contains(p, "/getFile") {
			found = true
		}
	}
	if !found {
		t.Fatalf("expected getFile call for group /ai document, got paths %v", gotPaths)
	}
}
