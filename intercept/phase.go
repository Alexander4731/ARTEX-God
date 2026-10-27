package intercept

import (
	"context"
	"sync"
)

type phaseKey struct{}

func WithPhaseObserver(ctx context.Context, observe func(string, int)) context.Context {
	return context.WithValue(ctx, phaseKey{}, observe)
}

func BeginPhase(ctx context.Context, phase string) func() {
	observe, _ := ctx.Value(phaseKey{}).(func(string, int))
	if observe == nil {
		return func() {}
	}
	observe(phase, 1)
	var once sync.Once
	return func() { once.Do(func() { observe(phase, -1) }) }
}
