package server

import (
	"context"
	"time"

	"github.com/Autumn-27/artex/agent"
)

func (s *Server) background(fn func()) bool {
	s.backgroundMu.Lock()
	defer s.backgroundMu.Unlock()
	if s.stopping || (s.ctx != nil && s.ctx.Err() != nil) {
		return false
	}
	s.backgroundWG.Add(1)
	go func() {
		defer s.backgroundWG.Done()
		fn()
	}()
	return true
}

// Shutdown stops admission and waits for background writers before the caller
// closes the manager's database connection.
func (s *Server) Shutdown(ctx context.Context) error {
	s.backgroundMu.Lock()
	s.stopping = true
	s.backgroundMu.Unlock()
	if s.stop != nil {
		s.stop(agent.AbortShutdown)
	}
	s.engine.deleteMu.Lock()
	s.engine.stopping = true
	s.engine.deleteMu.Unlock()
	for _, task := range s.m.List() {
		s.engine.cancelExec(task.ID, agent.AbortShutdown)
	}
	s.chatMu.Lock()
	for _, cancel := range s.chatCancel {
		cancel(agent.AbortShutdown)
	}
	s.chatMu.Unlock()
	done := make(chan struct{})
	go func() {
		s.backgroundWG.Wait()
		s.archiveWG.Wait()
		s.engine.runtimeMu.Lock()
		runtimes := make([]*taskRuntime, 0, len(s.engine.runtimes))
		for _, runtime := range s.engine.runtimes {
			runtime.cancel()
			runtimes = append(runtimes, runtime)
		}
		s.engine.runtimeMu.Unlock()
		for _, runtime := range runtimes {
			runtime.wg.Wait()
		}
		close(done)
	}()
	select {
	case <-ctx.Done():
		return ctx.Err()
	case <-done:
	}
	for {
		pending := false
		for _, task := range s.m.List() {
			pending = pending || s.engine.inflightCount(task.ID) > 0
		}
		if !pending {
			return nil
		}
		if sleepCtx(ctx, 50*time.Millisecond) {
			return ctx.Err()
		}
	}
}
