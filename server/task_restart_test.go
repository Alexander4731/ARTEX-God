package server

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/Autumn-27/artex/agent"
	"github.com/Autumn-27/artex/db"
	"github.com/Autumn-27/norma/llm"
)

func terminalRestartFixture(t *testing.T, m *Manager, status string) (*Task, int64, int64, int64) {
	t.Helper()
	task, err := m.CreateTask(t.Name(), "Verify the local fixture", nil, 120, 0)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _, _ = m.DeleteTask(task.ID, DeleteTaskOptions{}) })
	goals, err := task.Store.CompleteBootstrap([]map[string]any{{"text": task.Goal}})
	if err != nil {
		t.Fatal(err)
	}
	fact, err := task.Store.AddNode(db.KindFact, map[string]any{"summary": "old fixture evidence"}, 0, "confirmed", "human", nil)
	if err != nil {
		t.Fatal(err)
	}
	if err := task.Store.ProveGoal(goals[0], fact); err != nil {
		t.Fatal(err)
	}
	intent, err := task.Store.AddIntent(map[string]any{"summary": "already executed"}, 0, nil, "human")
	if err != nil {
		t.Fatal(err)
	}
	if err := task.Store.SetNodeState(intent, "done"); err != nil {
		t.Fatal(err)
	}
	if _, err := m.StampTaskFirstRun(task.ID); err != nil {
		t.Fatal(err)
	}
	if err := m.SetTaskStatus(task.ID, status); err != nil {
		t.Fatal(err)
	}
	return task, goals[0], fact, intent
}

func restartTestManager(t *testing.T) *Manager {
	t.Helper()
	m, err := NewManager(t.TempDir(), "")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { m.Close() })
	t.Cleanup(restoreConcurrencySetting(t, m))
	if err := m.SetConcurrency(false, 1); err != nil {
		t.Fatal(err)
	}
	return m
}

func TestRestartTaskControlReopensGoalsAndPreservesHistory(t *testing.T) {
	for _, status := range []string{"done", "failed", "timeout"} {
		t.Run(status, func(t *testing.T) {
			m := restartTestManager(t)
			task, goal, fact, intent := terminalRestartFixture(t, m, status)
			s := newAdmissionTestServer(m, nil)
			s.jwtKey = []byte("restart-route-fixture")
			s.engine.settling.Store(task.ID, true)
			s.engine.deadline.Store(task.ID, task.lifecycleSnapshot().DeadlineAt)
			token, err := signJWT(s.jwtKey)
			if err != nil {
				t.Fatal(err)
			}
			handler := s.Handler()
			request := func(action string) *httptest.ResponseRecorder {
				req := httptest.NewRequest(http.MethodPost, "/api/tasks/"+task.ID+"/control", strings.NewReader(`{"action":"`+action+`"}`))
				req.Header.Set("Authorization", "Bearer "+token)
				w := httptest.NewRecorder()
				handler.ServeHTTP(w, req)
				return w
			}
			if w := request("resume"); w.Code != http.StatusConflict {
				t.Fatalf("ordinary resume unexpectedly changed terminal task: %d %s", w.Code, w.Body.String())
			}
			if w := request("restart"); w.Code != http.StatusOK {
				t.Fatalf("restart route: %d %s", w.Code, w.Body.String())
			}
			if w := request("restart"); w.Code != http.StatusConflict {
				t.Fatalf("duplicate restart accepted: %d %s", w.Code, w.Body.String())
			}
			stored, err := m.pg.GetTask(mustTaskID(t, task.ID))
			if err != nil || stored.Status != "running" || stored.CompletedAt != nil || stored.FirstRunAt != nil || stored.DeadlineAt != nil {
				t.Fatalf("restart did not reset persisted lifecycle: %+v %v", stored, err)
			}
			if s.engine.isSettling(task.ID) {
				t.Fatal("old settling gate survived restart")
			}
			for id, state := range map[int64]string{goal: "open", intent: "done"} {
				node, err := task.Store.GetNode(id)
				if err != nil || node == nil || node.State != state {
					t.Fatalf("node %d: %+v %v", id, node, err)
				}
			}
			var edges int
			if err := m.pg.QueryRow(`SELECT COUNT(*) FROM exploration_edges WHERE exploration_id=$1 AND src_id=$2 AND dst_id=$3 AND rel=$4`, task.ExpID, fact, goal, db.RelProves).Scan(&edges); err != nil || edges != 1 {
				t.Fatalf("historical evidence edge lost: %d %v", edges, err)
			}
		})
	}
}

func TestRestartTaskQueuesOnceAndSeedsMissingGoal(t *testing.T) {
	m := restartTestManager(t)
	if err := m.SetConcurrency(true, 1); err != nil {
		t.Fatal(err)
	}
	task, goal, _, _ := terminalRestartFixture(t, m, "done")
	if err := task.Store.DeleteGoal(goal); err != nil {
		t.Fatal(err)
	}
	// No runnable model: restart must stay gated in FIFO.
	s := newAdmissionTestServer(m, func(*Task) bool { return false })
	var successes atomic.Int32
	var wg sync.WaitGroup
	for range 2 {
		wg.Add(1)
		go func() {
			defer wg.Done()
			if _, err := s.applyTaskControl(task, "restart"); err == nil {
				successes.Add(1)
			}
		}()
	}
	wg.Wait()
	if successes.Load() != 1 || !task.lifecycleSnapshot().Queued || !s.engine.IsPaused(task.ID) {
		t.Fatalf("duplicate restart/queue gate: successes=%d state=%+v", successes.Load(), task.lifecycleSnapshot())
	}
	goals, err := task.Store.ListByKind(db.KindGoal, 10)
	if err != nil || len(goals) != 1 || goals[0].State != "open" || !strings.Contains(string(goals[0].Payload), task.Goal) {
		t.Fatalf("missing objective not restored: %+v %v", goals, err)
	}
}

func TestRestartTaskRollsBackWhenGoalResetFails(t *testing.T) {
	m := restartTestManager(t)
	task, goal, _, _ := terminalRestartFixture(t, m, "done")
	before := task.lifecycleSnapshot()
	// Inject a real SQL failure after the lifecycle UPDATE, before commit.
	_, err := m.pg.Exec(fmt.Sprintf(`CREATE FUNCTION restart_goal_failure() RETURNS trigger LANGUAGE plpgsql AS $$
	BEGIN RAISE EXCEPTION 'fixture goal write failed'; END $$;
	CREATE TRIGGER restart_goal_failure BEFORE UPDATE ON exploration_nodes FOR EACH ROW
	WHEN (OLD.id = %d) EXECUTE FUNCTION restart_goal_failure()`, goal))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		_, _ = m.pg.Exec(`DROP TRIGGER restart_goal_failure ON exploration_nodes; DROP FUNCTION restart_goal_failure()`)
	})
	s := newAdmissionTestServer(m, nil)
	if _, err := s.applyTaskControl(task, "restart"); err == nil {
		t.Fatal("restart ignored failed goal reset")
	}
	stored, err := m.pg.GetTask(mustTaskID(t, task.ID))
	after := task.lifecycleSnapshot()
	if err != nil || stored.Status != "done" || stored.CompletedAt == nil || stored.DeadlineAt == nil || after.Status != before.Status || after.FirstRunAt != before.FirstRunAt || after.DeadlineAt != before.DeadlineAt {
		t.Fatalf("failed restart partially committed: %+v %v", stored, err)
	}
	node, err := task.Store.GetNode(goal)
	if err != nil || node.State != "met" {
		t.Fatalf("failed restart changed goal: %+v %v", node, err)
	}
}

func TestRestartTaskPreservesExecutionAndDeleteBarriers(t *testing.T) {
	m := restartTestManager(t)
	task, goal, _, intent := terminalRestartFixture(t, m, "done")
	s := newAdmissionTestServer(m, nil)
	if !s.engine.BeginDelete(task.ID) {
		t.Fatal("install delete barrier")
	}
	if _, err := s.applyTaskControl(task, "restart"); err == nil {
		t.Fatal("restart bypassed delete barrier")
	}
	s.engine.AbortDelete(task.ID, false)
	if err := m.pg.SetExecutionPolicy(t.Context(), mustTaskID(t, task.ID), "legacy"); err != nil {
		t.Fatal(err)
	}
	audit := &db.InterceptAudit{RunID: "restart-run", ToolUseID: "restart-call", IntentID: intent, InputDigest: "digest", Correlation: "exact", EffectiveAction: "allow", ExecutionStatus: "unknown"}
	id, err := m.pg.CreateDecidedIntercept(0, 0, task.ID, "worker", "Write", []byte(`{"file_path":"fixture"}`), "allowed", "fixture", audit)
	if err != nil {
		t.Fatal(err)
	}
	if err := m.pg.PrepareToolExecution(t.Context(), id, "restart-run", "restart-call", t.TempDir(), false); err != nil {
		t.Fatal(err)
	}
	if err := m.pg.RecoverExecutionLedger(t.Context()); err != nil {
		t.Fatal(err)
	}
	if _, err := s.applyTaskControl(task, "restart"); err == nil || !strings.Contains(err.Error(), "结果未确认") {
		t.Fatalf("restart bypassed unresolved execution: %v", err)
	}
	node, err := task.Store.GetNode(goal)
	if err != nil || node.State != "met" || m.TaskStatus(task.ID) != "done" {
		t.Fatalf("rejected restart changed goals/lifecycle: %+v %v", node, err)
	}
}

func TestRestartTaskReachesPlannerWithoutReplayingCompletedIntent(t *testing.T) {
	m := restartTestManager(t)
	task, goal, fact, intent := terminalRestartFixture(t, m, "done")
	oldAugment, oldResolve, oldPrompt := agent.ToolAugment, agent.ToolResolve, agent.PromptOverride
	agent.ToolAugment, agent.ToolResolve, agent.PromptOverride = nil, nil, nil
	t.Cleanup(func() { agent.ToolAugment, agent.ToolResolve, agent.PromptOverride = oldAugment, oldResolve, oldPrompt })
	var plannerCalls, workerCalls atomic.Int32
	plannerProvider := retestProvider{complete: func(context.Context, llm.CompletionRequest) (llm.Message, string, llm.Usage, error) {
		if plannerCalls.Add(1) == 1 {
			input, _ := json.Marshal(map[string]any{"goal_id": goal, "evidence_id": fact})
			return llm.Message{Role: llm.RoleAssistant, Content: []llm.ContentBlock{{Type: llm.BlockToolUse, ID: "recheck", Name: "prove_goal", Input: input}}}, "tool_use", llm.Usage{}, nil
		}
		return llm.Message{Role: llm.RoleAssistant, Content: []llm.ContentBlock{llm.TextBlock("Rechecked fixture evidence.")}}, "end_turn", llm.Usage{}, nil
	}}
	workerProvider := retestProvider{complete: func(context.Context, llm.CompletionRequest) (llm.Message, string, llm.Usage, error) {
		workerCalls.Add(1)
		return llm.Message{}, "", llm.Usage{}, fmt.Errorf("completed intent must not replay")
	}}
	planner := agent.NewPlanner(plannerProvider, "fixture", m.dir, nil, 32000, 10)
	planner.SetNonStreaming(func() bool { return true })
	worker := agent.NewWorker(workerProvider, "fixture", m.dir, nil, 32000, 10)
	worker.SetNonStreaming(func() bool { return true })
	ctx, cancel := context.WithCancel(context.Background())
	s := &Server{m: m, engine: NewEngine(m), ctx: ctx}
	s.engine.debounce = time.Millisecond
	s.engine.SetAuthoritativeAgentResolver(func(*Task) (*agent.Planner, *agent.Worker) { return planner, worker })
	t.Cleanup(func() { cancel(); s.engine.StopTask(task.ID) })
	if _, err := s.applyTaskControl(task, "restart"); err != nil {
		t.Fatal(err)
	}
	deadline := time.After(5 * time.Second)
	tick := time.NewTicker(10 * time.Millisecond)
	defer tick.Stop()
	for {
		select {
		case <-deadline:
			t.Fatalf("restart never completed a planning round: %d %s", plannerCalls.Load(), m.TaskStatus(task.ID))
		case <-tick.C:
			if m.TaskStatus(task.ID) == "done" {
				node, err := task.Store.GetNode(intent)
				if err != nil || node.State != "done" || plannerCalls.Load() < 2 || workerCalls.Load() != 0 {
					t.Fatalf("restart skipped recheck or replayed intent: planner=%d worker=%d node=%+v err=%v", plannerCalls.Load(), workerCalls.Load(), node, err)
				}
				return
			}
		}
	}
}
