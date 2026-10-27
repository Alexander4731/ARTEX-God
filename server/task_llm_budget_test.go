package server

import (
	"context"
	"net/http"
	"net/http/httptest"
	"sync/atomic"
	"testing"
	"time"

	"github.com/Autumn-27/artex/agent"
	"github.com/Autumn-27/norma/llm"
)

func TestTaskRequestBudgetCountsSDKAttempts(t *testing.T) {
	for _, status := range []int{401, 503} {
		t.Run(http.StatusText(status), func(t *testing.T) {
			var calls atomic.Int32
			endpoint := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				calls.Add(1)
				w.WriteHeader(status)
				w.Write([]byte(`{"error":"provider unavailable"}`))
			}))
			defer endpoint.Close()
			retry := agent.RetryConfig{ConnectAttempts: 2, ConnectInterval: time.Millisecond, EmptyAttempts: -1, StreamAttempts: 2, StreamInterval: time.Millisecond}
			provider, err := (agent.Config{Format: llm.FormatOpenAI, BaseURL: endpoint.URL, APIKey: "test", Model: "test", Retry: retry}).NewProvider()
			if err != nil {
				t.Fatal(err)
			}
			hooks := taskLLMStreamHooks{current: func() (taskLLMSelection, error) { return taskLLMSelection{provider: provider, retry: retry}, nil }}
			ctx, cancel := context.WithTimeout(t.Context(), 5*time.Second)
			defer cancel()
			_, _, _, err = completeTaskLLM(ctx, "test", llm.CompletionRequest{}, hooks)
			if err == nil {
				t.Fatal("expected provider failure")
			}
			want := agent.RequestAttemptLimit(retry)
			if status == 401 {
				want = 1
			}
			if int(calls.Load()) != want {
				t.Fatalf("HTTP attempts=%d want=%d", calls.Load(), want)
			}
		})
	}
}
