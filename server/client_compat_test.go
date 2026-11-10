package server

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/Autumn-27/artex/db"
)

func TestClientCompatProfileAndTestOverrides(t *testing.T) {
	dsn, _, err := db.DSN()
	if err != nil {
		t.Skip(err)
	}
	pg, err := db.Open(dsn)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { pg.Close() })
	s := &Server{m: &Manager{pg: pg}}
	var received []http.Header
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		received = append(received, r.Header.Clone())
		w.Header().Set("Content-Type", "application/json")
		if r.URL.Path == "/models" {
			w.Write([]byte(`{"data":[{"id":"test-model"}]}`))
			return
		}
		w.Write([]byte(`{"status":"completed","output":[{"type":"message","role":"assistant","content":[{"type":"output_text","text":"OK"}]}],"usage":{"input_tokens":1,"output_tokens":1}}`))
	}))
	defer upstream.Close()
	id, err := pg.SaveProfile(&db.LLMProfile{Name: "test-client-compat-api", Format: "openai-responses", Model: "test-model", BaseURL: upstream.URL, APIKey: "stored-key", ClientCompat: true})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { pg.Exec(`DELETE FROM llm_profiles WHERE id=$1`, id) })
	call := func(handler http.HandlerFunc, body map[string]any) map[string]any {
		t.Helper()
		b, _ := json.Marshal(body)
		w := httptest.NewRecorder()
		handler(w, httptest.NewRequest(http.MethodPost, "/", bytes.NewReader(b)))
		if w.Code != 200 {
			t.Fatalf("status %d: %s", w.Code, w.Body.String())
		}
		var result map[string]any
		if err := json.Unmarshal(w.Body.Bytes(), &result); err != nil {
			t.Fatal(err)
		}
		return result
	}
	for _, handler := range []http.HandlerFunc{s.testLLM, s.pgListModels} {
		for _, value := range []any{nil, false, true} {
			body := map[string]any{"provider": "openai-responses", "model": "test-model", "base_url": upstream.URL, "profile_id": id, "streaming": false}
			if value != nil {
				body["client_compat"] = value
			}
			result := call(handler, body)
			if result["ok"] != true {
				t.Fatalf("API result: %v", result)
			}
			h := received[len(received)-1]
			if h.Get("Authorization") != "Bearer stored-key" {
				t.Fatal("saved credential not resolved")
			}
			want := value == nil || value == true
			if (h.Get("Originator") == "codex_cli_rs") != want {
				t.Fatalf("explicit setting %v was not respected", value)
			}
		}
	}
	for _, value := range []any{nil, false, true} {
		body := map[string]any{"id": id, "name": "test-client-compat-api", "format": "openai-responses", "model": "test-model", "base_url": upstream.URL}
		if value != nil {
			body["client_compat"] = value
		}
		call(s.pgSaveProfile, body)
		p, err := pg.ProfileByID(id)
		if err != nil {
			t.Fatal(err)
		}
		want := value == nil || value == true
		if p.ClientCompat != want || p.APIKey != "stored-key" {
			t.Fatal("save lost compatibility or credentials")
		}
		if llmProfileDTO(p).ClientCompat != want {
			t.Fatal("DTO lost compatibility")
		}
		cfg, ok := s.loadProfileConfig(id)
		if !ok || cfg.ClientCompat != want {
			t.Fatal("runtime profile lost compatibility")
		}
	}
}
