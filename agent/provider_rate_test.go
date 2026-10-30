package agent

import (
	"context"
	"io"
	"net/http"
	"net/http/httptest"
	"sync"
	"testing"
	"time"

	"github.com/Autumn-27/norma/llm"
)

func TestProviderRateLimitSharedAndCancellation(t *testing.T) {
	var mu sync.Mutex
	var arrivals []time.Time
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		mu.Lock()
		arrivals = append(arrivals, time.Now())
		mu.Unlock()
		w.Header().Set("Content-Type", "application/json")
		_, _ = io.WriteString(w, `{"id":"test","choices":[{"message":{"role":"assistant","content":"ok"},"finish_reason":"stop"}]}`)
	}))
	defer upstream.Close()
	cfg := ConfigFrom("openai", "test", upstream.URL, "test-key", "")
	cfg.RatePerSecond, cfg.RatePerMinute = 10, 240
	provider, err := cfg.NewProvider()
	if err != nil {
		t.Fatal(err)
	}
	req := llm.CompletionRequest{Messages: []llm.Message{llm.UserText("test")}}
	if _, _, _, err := provider.Complete(t.Context(), req); err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(t.Context(), 20*time.Millisecond)
	defer cancel()
	if _, _, _, err := provider.Complete(ctx, req); err == nil || ctx.Err() == nil {
		t.Fatalf("queued request did not cancel: %v", err)
	}
	var wg sync.WaitGroup
	for range 3 {
		wg.Go(func() {
			if _, _, _, err := provider.Complete(t.Context(), req); err != nil {
				t.Error(err)
			}
		})
	}
	wg.Wait()
	mu.Lock()
	defer mu.Unlock()
	if len(arrivals) != 4 {
		t.Fatalf("HTTP requests = %d, want 4 (cancelled request must not be sent)", len(arrivals))
	}
	for i := 1; i < len(arrivals); i++ {
		if gap := arrivals[i].Sub(arrivals[i-1]); gap < 200*time.Millisecond {
			t.Errorf("concurrent request %d bypassed the stricter minute limit: gap=%v", i, gap)
		}
	}
}

func TestProviderRateLimitsHTTPRetries(t *testing.T) {
	for _, stream := range []bool{false, true} {
		for _, perMinute := range []bool{false, true} {
			name := "second"
			if perMinute {
				name = "minute"
			}
			if stream {
				name += "/stream"
			} else {
				name += "/complete"
			}
			t.Run(name, func(t *testing.T) {
				var mu sync.Mutex
				var arrivals []time.Time
				upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
					mu.Lock()
					arrivals = append(arrivals, time.Now())
					mu.Unlock()
					w.Header().Set("Content-Type", "application/json")
					w.WriteHeader(http.StatusTooManyRequests)
					_, _ = io.WriteString(w, `{"error":{"message":"rate limit exceeded"}}`)
				}))
				defer upstream.Close()
				cfg := ConfigFrom("openai", "test", upstream.URL, "test-key", "")
				if perMinute {
					cfg.RatePerMinute = 240
				} else {
					cfg.RatePerSecond = 4
				}
				cfg.Retry = RetryConfig{ConnectAttempts: 2, ConnectInterval: time.Millisecond, EmptyAttempts: -1}
				provider, err := cfg.NewProvider()
				if err != nil {
					t.Fatal(err)
				}
				req := llm.CompletionRequest{Messages: []llm.Message{llm.UserText("test")}}
				if stream {
					for _, e := range provider.Stream(t.Context(), req) {
						err = e
					}
				} else {
					_, _, _, err = provider.Complete(t.Context(), req)
				}
				if err == nil {
					t.Fatal("expected upstream rate-limit error")
				}
				mu.Lock()
				defer mu.Unlock()
				if len(arrivals) != 3 {
					t.Fatalf("HTTP attempts = %d, want 3", len(arrivals))
				}
				for i := 1; i < len(arrivals); i++ {
					if gap := arrivals[i].Sub(arrivals[i-1]); gap < 200*time.Millisecond {
						t.Errorf("HTTP retry %d bypassed rate limit: gap=%v, configured interval=250ms", i, gap)
					}
				}
			})
		}
	}
}
