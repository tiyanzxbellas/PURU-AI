package telegram

import (
	"testing"
)

func TestSanitizeText(t *testing.T) {
	got := sanitizeText("halo \xff\xfe dunia")
	if got != "halo \ufffd dunia" {
		t.Fatalf("unexpected sanitize result: %q", got)
	}
}

func TestSanitizeTextKeepsValidUTF8(t *testing.T) {
	in := "Halo 😀 — judul: Gunung Everest, paragraf pertama: Mount Everest"
	if got := sanitizeText(in); got != in {
		t.Fatalf("valid UTF-8 must be unchanged, got %q", got)
	}
}
