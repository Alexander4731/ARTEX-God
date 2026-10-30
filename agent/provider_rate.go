package agent

import (
	"context"
	"time"

	"github.com/Autumn-27/artex/intercept"
)

// Pace actual HTTP attempts, including the SDK's internal retries. The SDK's
// provider limiter only counts logical requests and allows retries to bypass it.
type requestPacer struct {
	gate     chan struct{}
	interval time.Duration
	next     time.Time
}

func newRequestPacer(perSecond, perMinute float64) *requestPacer {
	var seconds float64
	if perSecond > 0 {
		seconds = 1 / perSecond
	}
	if perMinute > 0 {
		seconds = max(seconds, 60/perMinute)
	}
	if seconds == 0 {
		return nil
	}
	return &requestPacer{gate: make(chan struct{}, 1), interval: time.Duration(seconds * float64(time.Second))}
}

func (p *requestPacer) wait(ctx context.Context) error {
	if p == nil {
		return ctx.Err()
	}
	finish := intercept.BeginPhase(ctx, "backoff")
	defer finish()
	select {
	case p.gate <- struct{}{}:
		defer func() { <-p.gate }()
	case <-ctx.Done():
		return ctx.Err()
	}
	if delay := time.Until(p.next); delay > 0 {
		timer := time.NewTimer(delay)
		defer timer.Stop()
		select {
		case <-timer.C:
		case <-ctx.Done():
			return ctx.Err()
		}
	}
	if err := ctx.Err(); err != nil {
		return err
	}
	p.next = time.Now().Add(p.interval)
	return nil
}
