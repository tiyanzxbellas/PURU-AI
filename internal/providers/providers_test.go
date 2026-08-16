package providers

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/purujawa06-bot/PURU-AI/internal/config"
	"github.com/purujawa06-bot/PURU-AI/internal/firebase"
)

// fakeRTDB mimics Firebase REST semantics (GET->null, PUT replaces node w/ children).
type fakeRTDB struct {
	db map[string]string
}

func newFakeRTDB() *fakeRTDB { return &fakeRTDB{db: map[string]string{}} }

func (f *fakeRTDB) handler() http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		key := r.URL.Path
		key = key[1 : len(key)-len(".json")]
		switch r.Method {
		case http.MethodGet:
			if v, ok := f.db[key]; ok {
				w.Write([]byte(v))
			} else {
				w.Write([]byte("null"))
			}
		case http.MethodPut:
			var body any
			if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
				w.WriteHeader(http.StatusBadRequest)
				return
			}
			raw, _ := json.Marshal(body)
			f.db[key] = string(raw)
			for k := range f.db {
				if len(k) > len(key) && k[:len(key)] == key && k[len(key)] == '/' {
					delete(f.db, k)
				}
			}
			w.Write(raw)
		case http.MethodDelete:
			for k := range f.db {
				if k == key || (len(k) > len(key) && k[:len(key)+1] == key+"/") {
					delete(f.db, k)
				}
			}
			w.Write([]byte("null"))
		default:
			w.WriteHeader(http.StatusMethodNotAllowed)
		}
	})
}

func newEnv(t *testing.T) (*Manager, *httptest.Server) {
	t.Helper()
	rt := httptest.NewServer(newFakeRTDB().handler())
	t.Cleanup(rt.Close)
	hc := rt.Client()
	fb := firebase.New(rt.URL, hc)
	return New(fb, hc, time.Minute), rt
}

func TestUpsertValidationAndUniqueness(t *testing.T) {
	m, _ := newEnv(t)
	ctx := context.Background()

	p := Provider{Name: "Drop", Prefix: "oc", Type: TypeOpenAI, APIType: APITypeChat, BaseURL: "https://x/v1"}
	// missing prefix
	bad := p
	bad.Prefix = ""
	if _, err := m.Upsert(ctx, 1, bad); err == nil {
		t.Fatal("expected prefix validation error")
	}
	// missing base URL
	bad = p
	bad.BaseURL = ""
	if _, err := m.Upsert(ctx, 1, bad); err == nil {
		t.Fatal("expected baseURL validation error")
	}
	p1, err := m.Upsert(ctx, 1, p)
	if err != nil {
		t.Fatalf("upsert: %v", err)
	}
	if p1.ID == "" || p1.APIType != APITypeChat {
		t.Fatalf("bad provider: %+v", p1)
	}
	// duplicate prefix -> error
	p2 := p
	p2.Name = "Other"
	if _, err := m.Upsert(ctx, 1, p2); err == nil {
		t.Fatal("expected duplicate-prefix error")
	}
	// second provider, different prefix
	p2.Prefix = "ac"
	_, err = m.Upsert(ctx, 1, p2)
	if err != nil {
		t.Fatalf("upsert second: %v", err)
	}
	list, err := m.List(ctx, 1)
	if err != nil || len(list) != 2 {
		t.Fatalf("list = %d (%v)", len(list), err)
	}
}

func TestSplitModelRef(t *testing.T) {
	cases := []struct{ in, prefix, model string }{
		{"oc/deepseek-v4-flash-free", "oc", "deepseek-v4-flash-free"},
		{"gpt-4o", "", "gpt-4o"},
		{"", "", ""},
		{"/x", "", "x"},
		{"oc/", "oc", ""},
	}
	for _, c := range cases {
		pre, mod := SplitModelRef(c.in)
		if pre != c.prefix || mod != c.model {
			t.Fatalf("SplitModelRef(%q) = %q,%q want %q,%q", c.in, pre, mod, c.prefix, c.model)
		}
	}
}

func TestResolve(t *testing.T) {
	m, _ := newEnv(t)
	ctx := context.Background()
	if _, err := m.Upsert(ctx, 7, Provider{
		Name: "Prod", Prefix: "oc-prod", Type: TypeOpenAI,
		APIType: APITypeChat, BaseURL: "https://prod/v1", APIKey: "secret",
	}); err != nil {
		t.Fatal(err)
	}

	r := m.Resolve(ctx, 7, "oc-prod/glm-4.7")
	if r == nil || r.Model != "glm-4.7" || r.Provider.Prefix != "oc-prod" || r.Provider.APIKey != "secret" {
		t.Fatalf("resolve = %+v", r)
	}
	// unknown prefix -> nil (settings fallback)
	if r := m.Resolve(ctx, 7, "nope/model"); r != nil {
		t.Fatalf("unknown prefix should not resolve: %+v", r)
	}
	// plain model id -> nil
	if r := m.Resolve(ctx, 7, "gpt-4o"); r != nil {
		t.Fatalf("plain model should not resolve: %+v", r)
	}
	// other chat isolated
	if r := m.Resolve(ctx, 8, "oc-prod/glm-4.7"); r != nil {
		t.Fatalf("other chat should not see provider: %+v", r)
	}
}

func TestReferencing(t *testing.T) {
	if !Referencing("oc", "oc/model-1") {
		t.Fatal("prefix/model should be referencing")
	}
	if Referencing("oc", "ocean/model") {
		t.Fatal("overlapping prefix must not match")
	}
	if Referencing("oc", "model-only") {
		t.Fatal("plain model must not be referencing")
	}
}

func TestBuiltinProviderLifecycle(t *testing.T) {
	m, _ := newEnv(t)
	m.WithBuiltin(BuiltinProvider(config.AIConfig{BaseURL: "https://puru/v1", APIKey: "k", ProxyURL: "https://relay"}))
	ctx := context.Background()

	list, err := m.List(ctx, 1)
	if err != nil {
		t.Fatal(err)
	}
	if len(list) != 1 || list[0].Prefix != BuiltinPrefix || !list[0].Builtin {
		t.Fatalf("builtin list = %+v", list)
	}

	r := m.Resolve(ctx, 1, "puru/puru")
	if r == nil || r.Model != "puru" || r.Provider.BaseURL != "https://puru/v1" ||
		r.Provider.APIKey != "k" || r.Provider.ProxyURL != "" {
		t.Fatalf("builtin resolve = %+v", r)
	}

	// prefix reserved (case-insensitive)
	if _, err := m.Upsert(ctx, 1, Provider{Name: "X", Prefix: "Puru", Type: TypeOpenAI, BaseURL: "https://x"}); err == nil {
		t.Fatal("expected reserved-prefix error")
	}
	// builtin id can not be edited
	if _, err := m.Upsert(ctx, 1, Provider{ID: BuiltinProviderID, Name: "X", Prefix: "p", Type: TypeOpenAI, BaseURL: "https://x"}); err == nil {
		t.Fatal("expected builtin-edit error")
	}

	// stored providers coexist; builtin still listed exactly once
	if _, err := m.Upsert(ctx, 1, Provider{Name: "Extra", Prefix: "ex", Type: TypeOpenAI, APIType: APITypeChat, BaseURL: "https://ex"}); err != nil {
		t.Fatal(err)
	}
	list, err = m.List(ctx, 1)
	if err != nil || len(list) != 2 {
		t.Fatalf("list after upsert = %+v (%v)", list, err)
	}
	builtins := 0
	for _, p := range list {
		if p.Builtin {
			builtins++
		}
	}
	if builtins != 1 {
		t.Fatalf("builtin must appear once, got %d", builtins)
	}

	// builtin can not be removed
	if removed, err := m.Delete(ctx, 1, BuiltinProviderID); err != nil || removed {
		t.Fatalf("delete builtin = removed %v err %v", removed, err)
	}
}

func TestPublicHidesKey(t *testing.T) {
	p := Provider{ID: "p1", Name: "P", Prefix: "x", BaseURL: "https://x", APIKey: "topsecret"}
	pub := p.Public()
	if pub.HasKey != true {
		t.Fatalf("Public must flag HasKey: %+v", pub)
	}
}

func TestCheckStoredOpenAIShape(t *testing.T) {
	m, _ := newEnv(t)
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/v1/models" {
			t.Fatalf("path = %q", r.URL.Path)
		}
		if got := r.Header.Get("Authorization"); got != "Bearer sk-123" {
			t.Fatalf("auth = %q", got)
		}
		w.WriteHeader(http.StatusOK)
		_ = json.NewEncoder(w).Encode(map[string]any{
			"data": []map[string]string{
				{"id": "a", "name": "Model A"},
				{"id": "b"},
			},
		})
	}))
	t.Cleanup(upstream.Close)
	ctx := context.Background()

	prov, err := m.Upsert(ctx, 1, Provider{
		Name: "Up", Prefix: "up", Type: TypeOpenAI, APIType: APITypeChat,
		BaseURL: upstream.URL + "/v1", APIKey: "sk-123",
	})
	if err != nil {
		t.Fatal(err)
	}
	res := m.CheckStored(ctx, 1, prov.ID, false)
	if !res.Online || len(res.Models) != 2 {
		t.Fatalf("online=%v models=%+v", res.Online, res.Models)
	}
	if res.Models[0].ID != "a" || res.Models[1].ID != "b" {
		t.Fatalf("models = %+v", res.Models)
	}
	// cached second call hits same result
	res2 := m.CheckStored(ctx, 1, prov.ID, false)
	if !res2.Online {
		t.Fatal("cached call should stay online")
	}
}

func TestCheckStoredNon200(t *testing.T) {
	m, _ := newEnv(t)
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Error(w, "no way", 401)
	}))
	t.Cleanup(upstream.Close)
	ctx := context.Background()
	prov, err := m.Upsert(ctx, 1, Provider{
		Name: "Bad", Prefix: "bad", Type: TypeOpenAI, BaseURL: upstream.URL,
	})
	if err != nil {
		t.Fatal(err)
	}
	res := m.CheckStored(ctx, 1, prov.ID, true)
	if res.Online {
		t.Fatal("must be offline")
	}
	if res.Status != 401 {
		t.Fatalf("status = %d", res.Status)
	}
	if res.Error == "" {
		t.Fatal("expect error message")
	}
}

func TestCheckStoredRelayHeaders(t *testing.T) {
	m, _ := newEnv(t)
	var relayTarget, relayPath string
	relay := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		relayTarget = r.Header.Get("x-relay-target")
		relayPath = r.Header.Get("x-relay-path")
		w.Write([]byte(`{"data":[{"id":"relayed-model"}]}`))
	}))
	t.Cleanup(relay.Close)
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Write([]byte(`{"data":[{"id":"direct"}]}`))
	}))
	t.Cleanup(upstream.Close)
	ctx := context.Background()

	prov, err := m.Upsert(ctx, 1, Provider{
		Name: "Rel", Prefix: "rel", Type: TypeOpenAI,
		BaseURL: upstream.URL, ProxyURL: relay.URL,
	})
	if err != nil {
		t.Fatal(err)
	}
	res := m.CheckStored(ctx, 1, prov.ID, true)
	if !res.Online || len(res.Models) != 1 || res.Models[0].ID != "relayed-model" {
		t.Fatalf("relay should be used: %+v", res)
	}
	// relay got x-relay-target/path describing the real endpoint.
	if relayTarget == "" || relayPath == "" {
		t.Fatalf("relay headers missing: target=%q path=%q", relayTarget, relayPath)
	}
}

func TestCheckInline(t *testing.T) {
	m, _, _ := newEnvWithDeps(t)
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Write([]byte(`["m1","m2"]`))
	}))
	t.Cleanup(upstream.Close)
	ctx := context.Background()
	res := m.CheckInline(ctx, Provider{BaseURL: upstream.URL + "/v1", Type: TypeOpenAI})
	if !res.Online || len(res.Models) != 2 {
		t.Fatalf("inline check = %+v", res)
	}
}

func TestParseModelsShapes(t *testing.T) {
	cases := []struct {
		raw  string
		want []string
	}{
		{`{"data":[{"id":"a"},{"id":"b"}]}`, []string{"a", "b"}},
		{`{"models":[{"id":"a"},{"model":"b","name":"Bee"}]}`, []string{"a", "b"}},
		{`["x","y"]`, []string{"x", "y"}},
		{`{"data":[]}`, nil},
		{`not-json`, nil},
	}
	for _, c := range cases {
		got := parseModels([]byte(c.raw))
		if len(got) != len(c.want) {
			t.Fatalf("parseModels(%s) = %+v, want %v", c.raw, got, c.want)
		}
		for i := range c.want {
			if got[i].ID != c.want[i] {
				t.Fatalf("parseModels(%s)[%d] = %+v, want %v", c.raw, i, got[i], c.want[i])
			}
		}
	}
}

// newEnvWithDeps returns the manager + fake senders so inline-only checks can
// build a manager without a scratch fake. (Simplest: reuse newEnv.)
func newEnvWithDeps(t *testing.T) (*Manager, *httptest.Server, struct{}) {
	m, s := newEnv(t)
	return m, s, struct{}{}
}
