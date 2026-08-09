package ai

import (
	"encoding/json"
	"testing"
)

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