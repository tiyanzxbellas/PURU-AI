// Package auth stores the per-user web login password in Firebase RTDB. The
// password gates the /login/{id}/{pw} settings page; it lives at auth/{chatID}
// (independent of settings/, history/ and fs/) so /reset chat, /reset memory
// and clearing the API config never wipe the credential.
package auth

import (
	"context"
	"encoding/json"
	"strconv"

	"github.com/purujawa06-bot/PURU-AI/internal/firebase"
)

const minPasswordLen = 4

// Manager reads and writes per-user web login passwords.
type Manager struct {
	fb *firebase.Client
}

type record struct {
	Password string `json:"password"`
}

func New(fb *firebase.Client) *Manager {
	return &Manager{fb: fb}
}

func path(chatID int64) string { return "auth/" + strconv.FormatInt(chatID, 10) }

// Set stores (or replaces) the web login password for the chat.
func (m *Manager) Set(ctx context.Context, chatID int64, password string) error {
	return m.fb.Put(ctx, path(chatID), record{Password: password})
}

// Get returns the stored password, or "" when none is set yet.
func (m *Manager) Get(ctx context.Context, chatID int64) string {
	raw := m.fb.Get(ctx, path(chatID))
	if len(raw) == 0 {
		return ""
	}
	var rec record
	if err := json.Unmarshal(raw, &rec); err != nil {
		return ""
	}
	return rec.Password
}

// Verify reports whether the password matches the stored one.
func (m *Manager) Verify(ctx context.Context, chatID int64, password string) bool {
	return m.Get(ctx, chatID) != "" && m.Get(ctx, chatID) == password
}

// Has reports whether a password has been set for the chat.
func (m *Manager) Has(ctx context.Context, chatID int64) bool {
	return m.Get(ctx, chatID) != ""
}

// Delete removes the password, locking the user out of the web page.
func (m *Manager) Delete(ctx context.Context, chatID int64) error {
	return m.fb.Delete(ctx, path(chatID))
}

// ValidPassword checks the format of a user-supplied password: at least
// minPasswordLen characters, URL-safe (letters, digits, underscore, dash).
func ValidPassword(pw string) bool {
	if len(pw) < minPasswordLen {
		return false
	}
	for _, r := range pw {
		if !(r >= 'a' && r <= 'z' || r >= 'A' && r <= 'Z' || r >= '0' && r <= '9' || r == '_' || r == '-') {
			return false
		}
	}
	return true
}
