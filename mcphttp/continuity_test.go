package mcphttp

import (
	"context"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"sync/atomic"
	"testing"
	"time"
)

func TestSSEHandshakeHonorsCallerCancellation(t *testing.T) {
	for _, headers := range []bool{false, true} {
		t.Run(map[bool]string{false: "headers", true: "endpoint"}[headers], func(t *testing.T) {
			release := make(chan struct{})
			entered := make(chan struct{})
			srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				if headers {
					w.Header().Set("Content-Type", "text/event-stream")
					w.(http.Flusher).Flush()
				}
				close(entered)
				select {
				case <-release:
				case <-r.Context().Done():
				}
			}))
			defer srv.Close()
			defer close(release)
			ctx, cancel := context.WithCancel(t.Context())
			defer cancel()
			done := make(chan error, 1)
			go func() {
				c, err := NewSSE(ctx, "test", srv.URL, nil, false)
				if c != nil {
					_ = c.Close()
				}
				done <- err
			}()
			<-entered
			cancel()
			select {
			case err := <-done:
				if err == nil {
					t.Fatal("canceled handshake succeeded")
				}
			case <-time.After(300 * time.Millisecond):
				t.Error("canceled handshake remained stuck")
			}
		})
	}
}

func TestClosedSSEFailsEveryLaterCallWithoutPosting(t *testing.T) {
	var posts atomic.Int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { posts.Add(1); w.WriteHeader(202) }))
	defer srv.Close()
	done := make(chan struct{})
	close(done)
	c := &Client{legacySSE: true, http: srv.Client(), messageURL: srv.URL, streamDone: done, streamErr: io.EOF, pending: map[int]chan *rpcResponse{}}
	for range 3 {
		ctx, cancel := context.WithTimeout(t.Context(), time.Second)
		_, err := c.call(ctx, "tools/call", nil)
		cancel()
		if !errors.Is(err, io.EOF) {
			t.Fatalf("error=%v", err)
		}
	}
	if posts.Load() != 0 {
		t.Fatalf("sent %d calls after losing response stream", posts.Load())
	}
}
