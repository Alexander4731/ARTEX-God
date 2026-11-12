package llm

import "context"

type attemptObserverKey struct{}

// WithAttemptObserver reports each completed HTTP attempt's known usage. Missing
// provider usage remains zero/unknown; callers must never estimate billed tokens.
func WithAttemptObserver(ctx context.Context, observer func(Usage, error)) context.Context {
	return context.WithValue(ctx, attemptObserverKey{}, observer)
}
func reportAttempt(ctx context.Context, usage Usage, err error) {
	if observer, ok := ctx.Value(attemptObserverKey{}).(func(Usage, error)); ok {
		observer(usage, err)
	}
}

type callPurposeKey struct{}

func WithCallPurpose(ctx context.Context, purpose string) context.Context {
	return context.WithValue(ctx, callPurposeKey{}, purpose)
}
func CallPurpose(ctx context.Context) string {
	purpose, _ := ctx.Value(callPurposeKey{}).(string)
	return purpose
}
