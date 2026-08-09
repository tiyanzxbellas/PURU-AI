package ai

import (
	"encoding/json"
	"strings"
	"testing"
)

// TestNormalizeURL verifies crawl URL handling: empty values and invalid URLs
// must yield a clear error (not "unsupported protocol scheme"), while missing
// schemes get https:// prepended and unsupported schemes are rejected.
// Regression: the model occasionally passes an empty url, which used to leak a
// raw http client error back to the agent.
func TestNormalizeURL(t *testing.T) {
	tests := []struct {
		raw   string
		want  string
		errOn string
	}{
		{raw: "https://example.com/article", want: "https://example.com/article"},
		{raw: "example.com/x", want: "https://example.com/x"},
		{raw: "  http://example.com  ", want: "http://example.com"},
		{raw: "", errOn: "url kosong"},
		{raw: "   ", errOn: "url kosong"},
		{raw: "ftp://example.com", errOn: "skema URL"},
		{raw: "javascript:alert(1)", errOn: "skema URL"},
		{raw: "http://", errOn: "host kosong"},
	}
	for _, tc := range tests {
		got, err := normalizeURL(tc.raw)
		if tc.errOn != "" {
			if err == nil {
				t.Errorf("normalizeURL(%q): expected error containing %q, got %q", tc.raw, tc.errOn, got)
			} else if !strings.Contains(err.Error(), tc.errOn) {
				t.Errorf("normalizeURL(%q): error %q does not contain %q", tc.raw, err, tc.errOn)
			}
			continue
		}
		if err != nil {
			t.Errorf("normalizeURL(%q): unexpected error: %v", tc.raw, err)
		}
		if got != tc.want {
			t.Errorf("normalizeURL(%q) = %q, want %q", tc.raw, got, tc.want)
		}
	}
}

// TestToolSchemasValid verifies every tool schema is JSON-schema-clean for
// strict OpenAI-compatible providers: "required" must be an array (or absent),
// never null. Regression: obj(nil, ...) used to emit `"required": null`, which
// some providers reject with "None is not of type 'array'".
func TestToolSchemasValid(t *testing.T) {
	for name, schema := range toolSchemas {
		raw, err := json.Marshal(schema)
		if err != nil {
			t.Fatalf("%s: marshal: %v", name, err)
		}
		var root map[string]any
		if err := json.Unmarshal(raw, &root); err != nil {
			t.Fatalf("%s: unmarshal built schema: %v", name, err)
		}
		checkRequired(t, name, root)
	}
}

func checkRequired(t *testing.T, path string, node map[string]any) {
	if req, ok := node["required"]; ok {
		if req == nil {
			t.Fatalf("%s: required is null", path)
		}
		arr, ok := req.([]any)
		if !ok {
			t.Fatalf("%s: required is not an array: %T", path, req)
		}
		for _, r := range arr {
			if _, ok := r.(string); !ok {
				t.Fatalf("%s: required element not a string: %T", path, r)
			}
		}
	}
	// Recurse into objects/arrays to catch nested required: null (create_skill).
	switch v := node["properties"].(type) {
	case map[string]any:
		for k, item := range v {
			if m, ok := item.(map[string]any); ok {
				checkRequired(t, path+"."+k, m)
			}
		}
	}
	switch v := node["items"].(type) {
	case map[string]any:
		checkRequired(t, path+".items", v)
	}
}
