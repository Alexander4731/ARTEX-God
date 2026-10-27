package intercept

import (
	"context"
	"time"
)

type approvalDeadlineKey struct{}

func WithApprovalDeadline(ctx context.Context, deadline time.Time) context.Context {
	return context.WithValue(ctx, approvalDeadlineKey{}, deadline)
}

func approvalContext(ctx context.Context) (context.Context, context.CancelFunc) {
	if deadline, ok := ctx.Value(approvalDeadlineKey{}).(time.Time); ok {
		return context.WithDeadline(ctx, deadline)
	}
	return ctx, func() {}
}
