package server

import (
	"context"
	"log"
	"time"
)

func routineContext(ctx context.Context) context.Context {
	if ctx == nil {
		return context.Background()
	}
	return context.WithoutCancel(ctx)
}

func (m *Manager) executionBlocksTask(ctx context.Context, taskID string) (bool, error) {
	ctx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()
	p, err := m.pg.ExecutionPolicy(ctx, taskID)
	if err != nil {
		return false, err
	}
	if p.Mode == "auto" {
		return false, m.pg.QueueExecutionRecoveries(ctx, taskID)
	}
	return m.hasUnknownTaskExecution(taskID)
}

func (m *Manager) executionBlocksIntent(ctx context.Context, taskID string, intentID int64) (bool, error) {
	p, err := m.pg.ExecutionPolicy(ctx, taskID)
	if err != nil {
		return false, err
	}
	if p.Mode == "auto" {
		return m.pg.HasUnknownIntentExecution(ctx, taskID, intentID)
	}
	return m.hasUnknownTaskExecution(taskID)
}

func (e *Engine) executionRecoveryLoop(ctx context.Context, t *Task) {
	ticker := time.NewTicker(3 * time.Second)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
		}
		if e.recoverExecution == nil || e.IsPaused(t.ID) || e.IsDeleting(t.ID) {
			continue
		}
		state := t.lifecycleSnapshot()
		if state.Paused || state.Queued || isTerminalStatus(state.Status) {
			continue
		}
		p, err := e.m.pg.ExecutionPolicy(ctx, t.ID)
		if err != nil {
			log.Printf("[recovery] task %s policy: %v", t.ID, err)
			continue
		}
		if p.Mode == "legacy" {
			continue
		}
		runCtx, cancel := context.WithTimeout(e.execContextFor(ctx, t.ID), 70*time.Second)
		e.recoverExecution(runCtx, t)
		cancel()
	}
}
