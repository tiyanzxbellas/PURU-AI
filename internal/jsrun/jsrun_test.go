package jsrun

import (
	"strings"
	"testing"
)

func TestRunCheerioText(t *testing.T) {
	html := `<html><body><h1>Halo</h1></body></html>`
	res, _, err := RunCheerio(html, `$("h1").text()`)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if res != "Halo" {
		t.Fatalf("got %q, want Halo", res)
	}
}

func TestRunCheerioConsole(t *testing.T) {
	html := `<html><body><h1>Halo lagi</h1></body></html>`
	_, logs, err := RunCheerio(html, `(console.log($("h1").text()), $("h1").length)`)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !strings.Contains(logs, "Halo lagi") {
		t.Fatalf("console output missing text: %q", logs)
	}
}

func TestRunCheerioMapWithEl(t *testing.T) {
	html := `<html><body><p class="x">A</p><p class="x">B</p></body></html>`
	res, _, err := RunCheerio(html, `$("p").map(function(i, el){ return $(el).text(); }).join(",")`)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if res != "A,B" {
		t.Fatalf("map result mismatch: %q", res)
	}
}

func TestRunCheerioFindAttr(t *testing.T) {
	html := `<html><body><a href="https://x/a" class="l">ting</a></body></html>`
	res, _, err := RunCheerio(html, `$("a.l").first().attr("href")`)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if res != "https://x/a" {
		t.Fatalf("attr result mismatch: %q", res)
	}
}

func TestRunCheerioUnknownMethod(t *testing.T) {
	html := `<html></html>`
	_, _, err := RunCheerio(html, `$("h1").noSuchMethod()`)
	if err == nil {
		t.Fatalf("expected error for unknown method")
	}
}

func TestRunCheerioReturnStatement(t *testing.T) {
	html := `<html><body><h1>Judul</h1><p>Paragraf pertama</p></body></html>`
	res, _, err := RunCheerio(html, `return {title: $("h1").text(), first: $("p").first().text()};`)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !strings.Contains(res, "Judul") || !strings.Contains(res, "Paragraf pertama") {
		t.Fatalf("unexpected result: %q", res)
	}
}

func TestEvalMath(t *testing.T) {
	res, err := EvalMath("sqrt(144) * (25 + 5)")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if res != "360" {
		t.Fatalf("got %q, want 360", res)
	}
}

func TestEvalMathInvalid(t *testing.T) {
	if _, err := EvalMath("1 +"); err == nil {
		t.Fatalf("expected error for invalid expression")
	}
}
