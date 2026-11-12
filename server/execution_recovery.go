package server

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"path/filepath"
	"strconv"
	"strings"

	"github.com/Autumn-27/artex/db"
	"github.com/Autumn-27/artex/intercept"
	"github.com/Autumn-27/artex/recovery"
	"github.com/Autumn-27/norma/transcript"
)

func (s *Server) recoverTaskExecutions(ctx context.Context, t *Task) {
	if !s.engine.beginTaskOperation(t.ID) {
		return
	}
	defer s.engine.decInflight(t.ID)
	d := s.m.pg
	if err := intercept.ReplayExecutionReceipts(ctx, d, filepath.Join(s.m.dir, "transcripts")); err != nil {
		return
	}
	if err := s.m.interceptor.RecoverExecutions(ctx, transcript.NewStore(filepath.Join(s.m.dir, "transcripts")), t.ID); err != nil {
		return
	}
	if err := d.QueueExecutionRecoveries(ctx, t.ID); err != nil {
		return
	}
	policy, err := d.ExecutionPolicy(ctx, t.ID)
	if err != nil {
		return
	}
	// Reopen after each pass, including when a receipt resolved before the worker
	// finished committing its recovery-owned pause.
	defer func() {
		if policy.Mode == "auto" && ctx.Err() == nil {
			if n, err := d.SettleParkedIntents(ctx, t.ID); err == nil && n > 0 {
				t.Notify()
			}
			if n, err := d.ReopenRecoveredIntents(ctx, t.ID); err == nil && n > 0 {
				t.Notify()
			}
		}
	}()
	e, err := d.ClaimExecutionRecovery(ctx, t.ID)
	if err != nil || e == nil {
		return
	}
	decision := recovery.Decision{ExecutionID: e.ID, EvidenceVersion: e.EvidenceVersion, EffectState: "indeterminate", Decision: "request_human"}
	state, source := "resolved", "executor"
	switch e.ExecutionState {
	case "succeeded", "failed", "canceled", "not_started", "not_executed":
		decision.Decision = "continue_with_result"
		decision.Reason = "已取得真实工具结果；由 Worker 使用退出状态和输出继续判断目标，不重放原始操作。"
	case "running", "prepared":
		_ = d.DeferExecutionRecovery(ctx, *e, "等待执行器提交实际终态", false)
		return
	default:
		if e.ReadOnly {
			source = "deterministic"
			decision.Decision = "safe_read_retry"
			decision.EffectState = "not_applicable"
			decision.Reason = "原读取结果仍未知；此内置工具只读，可以在新回合重新读取，不把旧执行标为成功。"
		} else if e.Attempts >= 2 {
			source = "budget"
			state = "waiting_input"
			decision.Reason = "自动核验已达到重试上限，仍缺少充分证据。保留原执行状态，可补充依据或搁置此分支。"
		} else {
			if !s.taskRuntimeAvailable(t, "worker") {
				_ = d.DeferExecutionRecovery(ctx, *e, "等待任务核验模型可用", false)
				return
			}
			dir := filepath.Join(s.m.dir, "tasks", t.ID)
			goal := t.Goal
			if e.IntentID > 0 {
				if node, getErr := t.Store.GetNode(e.IntentID); getErr == nil && node != nil {
					goal += "\n" + string(node.Payload)
				}
			}
			var evidence []recovery.Evidence
			decision, evidence, err = recovery.Review(ctx, s.agentsForTask(t).workerRuntime.Complete, dir, goal, *e)
			if err != nil {
				_ = d.DeferExecutionRecovery(context.WithoutCancel(ctx), *e, err.Error(), ctx.Err() == nil)
				return
			}
			// Keep the exact evidence supplied to the model alongside its conclusion.
			source = "model"
			if decision.Decision == "request_human" {
				state = "waiting_input"
			} else if decision.Decision == "replan" {
				state = "parked"
			}
			raw, _ := json.Marshal(map[string]any{"verdict": decision, "evidence": evidence})
			_, _ = d.CommitRecovery(ctx, *e, state, decision.EffectState, source, raw)
			return
		}
	}
	raw, _ := json.Marshal(map[string]any{"verdict": decision})
	_, _ = d.CommitRecovery(ctx, *e, state, decision.EffectState, source, raw)
}

func (s *Server) executionPolicy(w http.ResponseWriter, r *http.Request) {
	pg := s.pg(w)
	if pg == nil {
		return
	}
	id, ok := pathInt(r, "id")
	if !ok || id <= 0 {
		writeErr(w, 400, "bad task id")
		return
	}
	t, err := pg.GetTask(id)
	if err != nil {
		writeErr(w, 500, err.Error())
		return
	}
	if t == nil {
		writeErr(w, 404, "task not found")
		return
	}
	if r.Method == http.MethodPut {
		r.Body = http.MaxBytesReader(w, r.Body, 1024)
		var p db.ExecutionPolicy
		if decode(r, &p) != nil || (p.Mode != "legacy" && p.Mode != "observe" && p.Mode != "auto") {
			writeErr(w, 400, "mode must be legacy, observe or auto")
			return
		}
		if err := pg.SetExecutionPolicy(r.Context(), id, p.Mode); err != nil {
			writeErr(w, 500, err.Error())
			return
		}
	}
	p, err := pg.ExecutionPolicy(r.Context(), strconv.FormatInt(id, 10))
	if err != nil {
		writeErr(w, 500, err.Error())
		return
	}
	writeJSON(w, 200, p)
}

func (s *Server) executionRecoveries(w http.ResponseWriter, r *http.Request) {
	pg := s.pg(w)
	if pg == nil {
		return
	}
	id, ok := pathInt(r, "id")
	if !ok || id <= 0 {
		writeErr(w, 400, "bad task id")
		return
	}
	taskID := strconv.FormatInt(id, 10)
	if err := pg.QueueExecutionRecoveries(r.Context(), taskID); err != nil {
		writeErr(w, 500, err.Error())
		return
	}
	rows, err := pg.ListExecutionRecoveries(r.Context(), taskID)
	if err != nil {
		writeErr(w, 500, err.Error())
		return
	}
	writeJSON(w, 200, rows)
}

func (s *Server) executionRecoveryDecision(w http.ResponseWriter, r *http.Request) {
	pg := s.pg(w)
	if pg == nil {
		return
	}
	taskID, ok := pathInt(r, "id")
	if !ok || taskID <= 0 {
		writeErr(w, 400, "bad task id")
		return
	}
	id, err := strconv.ParseInt(r.PathValue("executionID"), 10, 64)
	if err != nil || id <= 0 {
		writeErr(w, 400, "bad execution id")
		return
	}
	var body struct {
		Version  int64  `json:"version"`
		Decision string `json:"decision"`
		Note     string `json:"note"`
	}
	r.Body = http.MaxBytesReader(w, r.Body, 8192)
	if decode(r, &body) != nil || strings.TrimSpace(body.Note) == "" || (body.Decision != "recheck" && body.Decision != "park" && body.Decision != "accept_partial") {
		writeErr(w, 400, "提供版本、处理方式和依据")
		return
	}
	e, err := pg.GetExecution(r.Context(), strconv.FormatInt(taskID, 10), id)
	if errors.Is(err, sql.ErrNoRows) {
		writeErr(w, 404, "execution not found")
		return
	}
	if err != nil {
		writeErr(w, 500, err.Error())
		return
	}
	if e.Version != body.Version {
		writeErr(w, 409, "记录已更新，请刷新")
		return
	}
	won, err := pg.DecideExecutionRecovery(r.Context(), e, body.Decision, body.Note)
	if err != nil {
		writeErr(w, 500, err.Error())
		return
	}
	if !won {
		writeErr(w, 409, "记录已更新，请刷新")
		return
	}
	writeJSON(w, 200, map[string]any{"ok": true, "execution_id": fmt.Sprint(id)})
}
