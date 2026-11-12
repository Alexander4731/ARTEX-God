package server

import (
	"context"
	"errors"
	"fmt"
	"sync/atomic"
	"testing"
	"time"

	"github.com/Autumn-27/artex/agent"
	"github.com/Autumn-27/norma/harness"
)

func TestModelRequestDeadlineDoesNotExpandToRunBudget(t *testing.T) {
	for _, duration := range []time.Duration{time.Hour, time.Minute} {
		ctx, cancel := context.WithTimeout(t.Context(), duration)
		got := requestBudgetDuration(ctx)
		cancel()
		if got > 5*time.Minute || got > duration || got <= 0 {
			t.Fatalf("run=%s request=%s", duration, got)
		}
	}
}

func TestPlannerTemporaryFailuresRemainRetryable(t *testing.T) {
	for _, err := range []error{context.DeadlineExceeded, agent.ErrRetryBudget, fmt.Errorf("status 503"), harness.ErrEmptyResponse} {
		if !retryablePlannerModelError(err) {
			t.Fatalf("temporary error not retryable: %v", err)
		}
	}
	for _, err := range []error{context.Canceled, fmt.Errorf("status 401"), fmt.Errorf("insufficient_quota"), fmt.Errorf("transcript permission denied"), fmt.Errorf("invalid proxy configuration")} {
		if retryablePlannerModelError(err) {
			t.Fatalf("permanent/canceled error retried: %v", err)
		}
	}
	if got := plannerModelBackoff(1000); got > time.Minute || got <= 0 {
		t.Fatalf("backoff=%s", got)
	}
}

func TestRepeatedToolFailureStopsOnlyOffendingIntent(t *testing.T) {
	taskCtx, cancelTask := context.WithCancel(t.Context())
	defer cancelTask()
	workCtx, cancelWork := context.WithCancelCause(taskCtx)
	defer cancelWork(nil)
	guard := newWorkerFailureGuard(cancelWork)
	hooks := steerHooks{failures: guard}
	for range 3 {
		if blocked, _, _ := hooks.PreToolUse(workCtx, "fixture", []byte(`{"b":2,"a":1}`)); blocked {
			t.Fatal("blocked within retry allowance")
		}
		hooks.PostToolUse(workCtx, "fixture", []byte(`{"a":1, "b":2}`), []byte("failed"), true)
	}
	if blocked, _, _ := hooks.PreToolUse(workCtx, "fixture", []byte(`{"a":2}`)); blocked {
		t.Fatal("different method was blocked")
	}
	for range 3 {
		if blocked, _, _ := hooks.PreToolUse(workCtx, "fixture", []byte(`{"a":1,"b":2}`)); !blocked {
			t.Fatal("repeated failure allowed")
		}
	}
	if !errors.Is(context.Cause(workCtx), agent.AbortRepeatedToolFailure) || taskCtx.Err() != nil {
		t.Fatalf("work=%v task=%v", context.Cause(workCtx), taskCtx.Err())
	}
}

func TestToolFailureGuardPreservesPollingAndBoundsMemory(t *testing.T) {
	var stops atomic.Int32
	g := newWorkerFailureGuard(func(error) { stops.Add(1) })
	for i := range 100 {
		input := []byte(fmt.Sprintf(`{"query":%d}`, i))
		g.after("read", input, true)
		g.after("read", input, false)
		if blocked, _ := g.before("read", input); blocked {
			t.Fatal("successful polling blocked")
		}
	}
	if len(g.entries) > 32 || stops.Load() != 0 {
		t.Fatalf("entries=%d stops=%d", len(g.entries), stops.Load())
	}
	// Never merge distinct integers through float64 rounding.
	if toolAttemptKey("read", []byte(`{"id":9007199254740992}`)) == toolAttemptKey("read", []byte(`{"id":9007199254740993}`)) {
		t.Fatal("large IDs conflated")
	}
}
