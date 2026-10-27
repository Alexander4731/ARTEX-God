package server

import (
	"context"
	"sync"

	"github.com/Autumn-27/artex/intercept"
)

type phaseActivity struct {
	mu     sync.Mutex
	counts map[string]int
}

func (e *Engine) phaseContext(ctx context.Context, taskID string) context.Context {
	return intercept.WithPhaseObserver(ctx, func(phase string, delta int) {
		v, _ := e.phases.LoadOrStore(taskID, &phaseActivity{counts: map[string]int{}})
		a := v.(*phaseActivity)
		a.mu.Lock()
		a.counts[phase] = max(0, a.counts[phase]+delta)
		a.mu.Unlock()
		e.touch(taskID)
	})
}

func (e *Engine) executionPhase(taskID string) string {
	if e.isSettling(taskID) {
		return "settling"
	}
	if v, ok := e.phases.Load(taskID); ok {
		a := v.(*phaseActivity)
		a.mu.Lock()
		defer a.mu.Unlock()
		for _, phase := range []string{"approval", "tool", "settling", "backoff"} {
			if a.counts[phase] > 0 {
				return phase
			}
		}
	}
	if e.ActiveLLMCalls(taskID) > 0 {
		return "model"
	}
	return "idle"
}
