package agent

import (
	"context"
	"errors"
	"net/http"
	"strconv"
	"sync"
	"time"

	"github.com/Autumn-27/artex/intercept"
)

var ErrRetryBudget = errors.New("LLM 请求重试预算已耗尽")

type retryBudgetKey struct{}
type requestBudget struct {
	mu        sync.Mutex
	remaining int
	notBefore time.Time
	cancel    context.CancelCauseFunc
}

// The logical request has one additive budget across the existing retry knobs;
// increasing a configured retry count increases this limit without multiplying layers.
func RequestAttemptLimit(r RetryConfig) int {
	normalize := func(value, fallback int) int {
		if value == 0 {
			return fallback
		}
		return max(value, 0)
	}
	return 1 + normalize(r.ConnectAttempts, 3) + normalize(r.EmptyAttempts, 2) + normalize(r.StreamAttempts, 2)
}

// WithRetryBudget bounds actual HTTP attempts across SDK retries and provider
// switches for one logical model request.
func WithRetryBudget(ctx context.Context, attempts int, duration time.Duration) (context.Context, context.CancelFunc) {
	deadlineCtx, stop := context.WithTimeoutCause(ctx, duration, ErrRetryBudget)
	ctx, cancel := context.WithCancelCause(deadlineCtx)
	b := &requestBudget{remaining: attempts, cancel: cancel}
	return context.WithValue(ctx, retryBudgetKey{}, b), func() { cancel(nil); stop() }
}

func takeRequestAttempt(ctx context.Context) error {
	b, _ := ctx.Value(retryBudgetKey{}).(*requestBudget)
	if b == nil {
		return ctx.Err()
	}
	b.mu.Lock()
	if b.remaining <= 0 {
		b.mu.Unlock()
		b.cancel(ErrRetryBudget)
		return ErrRetryBudget
	}
	b.remaining--
	delay := time.Until(b.notBefore)
	b.mu.Unlock()
	if delay > 0 {
		finish := intercept.BeginPhase(ctx, "backoff")
		defer finish()
		timer := time.NewTimer(delay)
		defer timer.Stop()
		select {
		case <-ctx.Done():
			return context.Cause(ctx)
		case <-timer.C:
		}
	}
	return ctx.Err()
}

func observeRetryAfter(ctx context.Context, response *http.Response) {
	b, _ := ctx.Value(retryBudgetKey{}).(*requestBudget)
	if b == nil || response == nil {
		return
	}
	value := response.Header.Get("Retry-After")
	if value == "" {
		return
	}
	when, err := http.ParseTime(value)
	if seconds, parseErr := strconv.Atoi(value); parseErr == nil && seconds > 0 {
		when, err = time.Now().Add(time.Duration(seconds)*time.Second), nil
	}
	if err != nil {
		return
	}
	b.mu.Lock()
	if when.After(b.notBefore) {
		b.notBefore = when
	}
	b.mu.Unlock()
}
