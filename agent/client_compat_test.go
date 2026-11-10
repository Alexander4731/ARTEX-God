package agent

import (
	"context"
	"io"
	"net/http"
	"net/http/httptest"
	"reflect"
	"strings"
	"testing"

	"github.com/Autumn-27/norma/llm"
	"github.com/Autumn-27/norma/transcript"
	"github.com/google/uuid"
)

func TestClientCompatTransport(t *testing.T) {
	for _, format := range []llm.Format{llm.FormatAnthropic, llm.FormatOpenAI, llm.FormatOpenAIResponses} {
		for _, enabled := range []bool{false, true} {
			t.Run(string(format)+"/"+map[bool]string{true: "on", false: "off"}[enabled], func(t *testing.T) {
				var received []http.Header
				const body = `{"tools":[{"name":"echo"}],"stream":true}`
				const wire = "data: {\"delta\":\"hello\"}\n\ndata: [DONE]\n\n"
				srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
					b, _ := io.ReadAll(r.Body)
					if string(b) != body {
						t.Errorf("request body changed: %s", b)
					}
					received = append(received, r.Header.Clone())
					w.Header().Set("Content-Type", "text/event-stream")
					_, _ = io.WriteString(w, wire)
				}))
				defer srv.Close()
				client, err := quotaAwareHTTPClient(Config{Format: format, ClientCompat: enabled, SessionHeaderKey: "X-Session"})
				if err != nil {
					t.Fatal(err)
				}
				defer client.CloseIdleConnections()
				for _, sid := range []string{"conv-12", "conv-12", "conv-13"} {
					ctx := transcript.WithSessionID(context.Background(), sid)
					req, _ := http.NewRequestWithContext(ctx, http.MethodPost, srv.URL, strings.NewReader(body))
					req.Header.Set("Authorization", "Bearer test-key")
					req.Header.Set("X-Api-Key", "test-key")
					req.Header.Set("User-Agent", "original-client")
					req.Header.Set("Accept", "text/event-stream")
					req.Header.Set("Anthropic-Beta", "existing-beta")
					before := req.Header.Clone()
					resp, err := client.Do(req)
					if err != nil {
						t.Fatal(err)
					}
					out, err := io.ReadAll(resp.Body)
					resp.Body.Close()
					if err != nil || string(out) != wire {
						t.Fatalf("response changed: %q, %v", out, err)
					}
					if !reflect.DeepEqual(req.Header, before) {
						t.Fatal("mutated caller's headers")
					}
				}
				for _, h := range received {
					if h.Get("Authorization") != "Bearer test-key" || h.Get("X-Api-Key") != "test-key" {
						t.Fatal("authentication changed")
					}
					if h.Get("Accept") != "text/event-stream" {
						t.Fatal("stream accept header changed")
					}
					if !enabled {
						if h.Get("User-Agent") != "original-client" || h.Get("X-Client-Request-Id") != "" {
							t.Fatal("disabled compatibility changed headers")
						}
						continue
					}
					if _, err := uuid.Parse(h.Get("X-Client-Request-Id")); err != nil {
						t.Fatal(err)
					}
					if format == llm.FormatAnthropic {
						if h.Get("User-Agent") != claudeUserAgent || h.Get("X-App") != "cli" || h.Get("Anthropic-Beta") != "existing-beta,claude-code-20250219" {
							t.Fatalf("Claude headers: %v", h)
						}
					} else if h.Get("User-Agent") != codexUserAgent || h.Get("Originator") != "codex_cli_rs" {
						t.Fatalf("Codex headers: %v", h)
					}
				}
				if received[0].Get("X-Session") != "conv-12" || received[2].Get("X-Session") != "conv-13" {
					t.Fatal("custom session header lost")
				}
				if enabled {
					key := "Session_id"
					if format == llm.FormatAnthropic {
						key = "X-Client-Session-Id"
					}
					if received[0].Get(key) == "" || received[0].Get(key) != received[1].Get(key) || received[0].Get(key) == received[2].Get(key) {
						t.Fatal("session identity must be stable and isolated")
					}
					if received[0].Get("X-Client-Request-Id") == received[1].Get("X-Client-Request-Id") {
						t.Fatal("request identity reused")
					}
				}
			})
		}
	}
}

func TestClientCompatDoesNotBufferSSE(t *testing.T) {
	release := make(chan struct{})
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/event-stream")
		_, _ = io.WriteString(w, "data: first\n\n")
		w.(http.Flusher).Flush()
		<-release
	}))
	defer srv.Close()
	defer close(release)
	client, err := quotaAwareHTTPClient(Config{Format: llm.FormatOpenAIResponses, ClientCompat: true})
	if err != nil {
		t.Fatal(err)
	}
	defer client.CloseIdleConnections()
	req, _ := http.NewRequestWithContext(t.Context(), http.MethodGet, srv.URL, nil)
	resp, err := client.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	b := make([]byte, len("data: first\n\n"))
	if _, err := io.ReadFull(resp.Body, b); err != nil {
		t.Fatal(err)
	}
	if string(b) != "data: first\n\n" {
		t.Fatalf("unexpected first frame: %q", b)
	}
}
