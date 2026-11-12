package llm

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestThinkingFallbackAllProtocols(t *testing.T) {
	for _, format := range []Format{FormatAnthropic, FormatOpenAI, FormatOpenAIResponses} {
		for _, stream := range []bool{false, true} {
			t.Run(string(format)+map[bool]string{true: "/stream", false: "/complete"}[stream], func(t *testing.T) {
				calls := 0
				srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
					calls++
					var body map[string]json.RawMessage
					if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
						t.Error(err)
					}
					if calls == 1 {
						w.WriteHeader(400)
						w.Write([]byte(`{"error":{"code":"unsupported_parameter","param":"thinking"}}`))
						return
					}
					for _, key := range []string{"thinking", "reasoning_effort", "reasoning", "output_config"} {
						if _, ok := body[key]; ok {
							t.Errorf("fallback retained %s", key)
						}
					}
					if stream {
						w.Header().Set("Content-Type", "text/event-stream")
						switch format {
						case FormatAnthropic:
							w.Write([]byte("data: {\"type\":\"content_block_delta\",\"delta\":{\"type\":\"text_delta\",\"text\":\"ok\"}}\n\ndata: {\"type\":\"message_stop\"}\n\n"))
						case FormatOpenAI:
							w.Write([]byte("data: {\"choices\":[{\"delta\":{\"content\":\"ok\"},\"finish_reason\":\"stop\"}]}\n\ndata: [DONE]\n\n"))
						case FormatOpenAIResponses:
							w.Write([]byte("data: {\"type\":\"response.output_text.delta\",\"delta\":\"ok\"}\n\ndata: {\"type\":\"response.completed\",\"response\":{\"status\":\"completed\"}}\n\n"))
						}
						return
					}
					switch format {
					case FormatAnthropic:
						w.Write([]byte(`{"content":[{"type":"text","text":"ok"}],"stop_reason":"end_turn"}`))
					case FormatOpenAI:
						w.Write([]byte(`{"choices":[{"message":{"role":"assistant","content":"ok"},"finish_reason":"stop"}]}`))
					case FormatOpenAIResponses:
						w.Write([]byte(`{"status":"completed","output":[{"type":"message","content":[{"type":"output_text","text":"ok"}]}]}`))
					}
				}))
				defer srv.Close()
				p, err := NewProvider(Config{Format: format, BaseURL: srv.URL, Model: "test", ThinkingType: "enabled", ReasoningEffort: "high", MaxRetries: -1, EmptyResponseRetries: -1})
				if err != nil {
					t.Fatal(err)
				}
				req := CompletionRequest{Thinking: "disabled", Messages: []Message{UserText("summarize")}}
				if stream {
					for _, err := range p.Stream(t.Context(), req) {
						if err != nil {
							t.Fatal(err)
						}
					}
				} else {
					if _, _, _, err := p.Complete(t.Context(), req); err != nil {
						t.Fatal(err)
					}
				}
				if calls != 2 {
					t.Fatalf("calls=%d", calls)
				}
			})
		}
	}
}
func TestThinkingFallbackRejectsUnrelatedErrors(t *testing.T) {
	for _, tc := range []struct {
		status int
		body   string
	}{
		{400, `{"error":{"code":"unsupported_parameter","param":"max_tokens"}}`},
		{400, `{"error":{"message":"thinking quota exceeded"}}`},
		{401, `{"error":{"code":"unsupported_parameter","param":"thinking"}}`},
	} {
		if thinkingParameterRejected(&HTTPStatusError{Status: tc.status, Body: tc.body}) {
			t.Fatal("unrelated error triggers retry")
		}
	}
}
