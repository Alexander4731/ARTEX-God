package agent

import (
	"context"
	"io"
	"sync"
)

type providerActivityKey struct{}

func WithProviderActivity(ctx context.Context, begin func() func()) context.Context {
	return context.WithValue(ctx, providerActivityKey{}, begin)
}

type activityBody struct {
	io.ReadCloser
	finish func()
}

func (b *activityBody) Read(p []byte) (int, error) {
	n, err := b.ReadCloser.Read(p)
	if err != nil {
		b.finish()
	}
	return n, err
}
func (b *activityBody) Close() error {
	defer b.finish()
	return b.ReadCloser.Close()
}

func beginProviderActivity(ctx context.Context) func() {
	begin, _ := ctx.Value(providerActivityKey{}).(func() func())
	if begin == nil {
		return func() {}
	}
	end := begin()
	var once sync.Once
	finish := func() { once.Do(end) }
	stop := context.AfterFunc(ctx, finish)
	return func() { stop(); finish() }
}
