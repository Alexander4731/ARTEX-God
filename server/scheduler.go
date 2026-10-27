package server

import (
	"context"
	"fmt"
	"log"
	"sort"
	"time"

	"github.com/Autumn-27/artex/db"
)

type Scheduler struct {
	s    *Server
	pg   *db.DB
	tick time.Duration
}

func newScheduler(s *Server) *Scheduler { return &Scheduler{s: s, pg: s.m.pg, tick: 5 * time.Second} }

func (sc *Scheduler) Run(ctx context.Context) {
	if sc.pg == nil {
		return
	}
	sc.step()
	timer := time.NewTicker(sc.tick)
	defer timer.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-timer.C:
			sc.s.reconcileConcurrency()
			sc.step()
		}
	}
}

func (sc *Scheduler) step() {
	ctx := sc.s.ctx
	var live []int64
	sc.s.triggerLive.Range(func(key, value any) bool { live = append(live, key.(int64)); return true })
	if err := sc.pg.RecoverUnloadedTriggers(ctx, live); err != nil {
		log.Printf("[scheduler] 执行交接恢复失败: %v", err)
		return
	}
	if err := sc.pg.EnqueueDueTriggers(ctx); err != nil {
		log.Printf("[scheduler] 定时事件提交失败: %v", err)
		return
	}
	if sc.s.chatAgentRef() == nil {
		return
	}
	sc.s.queueMu.Lock()
	occupied := 0
	excluded := []string{}
	for key, q := range sc.s.triggerQ {
		occupied += len(q)
		if len(q) >= 32 {
			excluded = append(excluded, key)
		}
	}
	for _, count := range sc.s.triggerActive {
		occupied += count
	}
	sc.s.queueMu.Unlock()
	if occupied >= 128 {
		return
	}
	runs, err := sc.pg.ClaimTriggerRuns(ctx, min(64, 128-occupied), excluded)
	if err != nil {
		log.Printf("[scheduler] 领取事件失败: %v", err)
		return
	}
	sort.Slice(runs, func(i, j int) bool { return runs[i].ID < runs[j].ID })
	for _, r := range runs {
		cfg := sc.s.readTriggerBehavior(r.AgentKey)
		sc.s.queueMu.Lock()
		if len(sc.s.triggerQ[r.AgentKey]) >= 32 || len(r.Message) > maxTriggerMessageBytes {
			sc.s.queueMu.Unlock()
			if err := sc.pg.ReleaseTriggerRuns(ctx, []int64{r.ID}); err != nil {
				log.Printf("[scheduler] 归还事件 %d: %v", r.ID, err)
			}
			continue
		}
		sc.s.triggerCfg[r.AgentKey] = cfg
		sc.s.triggerLive.Store(r.ID, true)
		sc.s.triggerQ[r.AgentKey] = append(sc.s.triggerQ[r.AgentKey], triggeredRun{
			runIDs: []int64{r.ID}, agentKey: r.AgentKey, title: r.Title, message: r.Message,
			taskID: r.TaskID, taskDesc: r.TaskDescription, taskGoal: r.TaskGoal, mergeable: r.TaskID > 0,
		})
		sc.s.queueMu.Unlock()
	}
	sc.s.queueMu.Lock()
	for key := range sc.s.triggerQ {
		sc.s.pumpLocked(key)
	}
	sc.s.queueMu.Unlock()
}

// trunc caps display context without changing the persisted trigger payload.
func trunc(s string, limit int) string {
	r := []rune(s)
	if len(r) <= limit {
		return s
	}
	return string(r[:limit]) + fmt.Sprintf("…(已截断,共 %d 字)", len(r))
}
