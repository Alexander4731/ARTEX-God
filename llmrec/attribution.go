package llmrec

import (
	"context"
	"github.com/Autumn-27/norma/llm"
	"strconv"
	"strings"
)

type attributionKey struct{}
type attribution struct {
	ExplorationID, IntentID int64
	Purpose                 string
}

func WithAttribution(ctx context.Context, explorationID, intentID int64) context.Context {
	a, _ := ctx.Value(attributionKey{}).(attribution)
	a.ExplorationID = explorationID
	a.IntentID = intentID
	return context.WithValue(ctx, attributionKey{}, a)
}
func WithPurpose(ctx context.Context, purpose string) context.Context {
	return llm.WithCallPurpose(ctx, purpose)
}
func callAttribution(ctx context.Context, session, worker string, expID int64) attribution {
	a, _ := ctx.Value(attributionKey{}).(attribution)
	if a.ExplorationID <= 0 {
		a.ExplorationID = expID
	}
	a.Purpose = llm.CallPurpose(ctx)
	if a.Purpose == "" {
		a.Purpose = worker
	}
	if a.IntentID == 0 && a.ExplorationID > 0 {
		prefix := "exp" + strconv.FormatInt(a.ExplorationID, 10) + "-worker-i"
		if suffix, ok := strings.CutPrefix(session, prefix); ok {
			id, err := strconv.ParseInt(suffix, 10, 64)
			if err == nil && id > 0 {
				a.IntentID = id
			}
		}
	}
	return a
}
