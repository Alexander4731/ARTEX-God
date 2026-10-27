package server

import (
	"context"
	"errors"
	"log"
	"time"

	"github.com/Autumn-27/artex/agent"
	"github.com/Autumn-27/artex/db"
	"github.com/Autumn-27/artex/intercept"
)

func (e *Engine) pauseForInput(t *Task, reason string) {
	e.Pause(t.ID, agent.AbortPausedByOrchestrator)
	if err := e.m.SetTaskPaused(t.ID, true); err != nil {
		log.Printf("[planner] task %s 暂停保存失败: %v", t.ID, err)
	}
	e.emitActivity(t, db.Activity{Worker: "system", Kind: "text", Summary: reason})
}

// Keep the worker admission and its result until PostgreSQL acknowledges it.
// Shutdown gets one bounded final write; it never starts another Execute.
func settleIntent(ctx context.Context, store *db.ExplorationStore, id int64, state, reason string) error {
	finish := intercept.BeginPhase(ctx, "settling")
	defer finish()
	for {
		writeCtx, cancel := context.WithTimeout(context.WithoutCancel(ctx), 5*time.Second)
		err := store.SettleIntent(writeCtx, id, state, reason)
		cancel()
		if err == nil || errors.Is(err, db.ErrIntentStateConflict) {
			return err
		}
		log.Printf("[settlement] intent %d state=%s 等待状态提交: %v", id, state, err)
		if ctx.Err() != nil || sleepCtx(ctx, time.Second) {
			return err
		}
	}
}
