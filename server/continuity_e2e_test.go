package server

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/Autumn-27/artex/agent"
	"github.com/Autumn-27/artex/db"
	"github.com/Autumn-27/norma/llm"
	actool "github.com/Autumn-27/norma/tool"
	"github.com/Autumn-27/norma/transcript"
)

// No real model or target tool: drives the actual planner/worker, transcript,
// domain tools, PostgreSQL settlement and completion guard across failures.
func TestTaskCompletesAfterEmptyWorkerAndPlannerOutage(t *testing.T) {
	m, err := NewManager(t.TempDir(), "")
	if err != nil {
		t.Fatal(err)
	}
	defer m.Close()
	task, err := m.CreateTask(t.Name(), "Verify the local fixture only", nil, 0, 0)
	if err != nil {
		t.Fatal(err)
	}
	defer m.DeleteTask(task.ID, DeleteTaskOptions{})
	goals, err := task.Store.CompleteBootstrap([]map[string]any{{"text": task.Goal}})
	if err != nil {
		t.Fatal(err)
	}
	intentID, err := task.Store.AddIntent(map[string]any{"summary": "Record the fixture result"}, 5, nil, "planner")
	if err != nil {
		t.Fatal(err)
	}
	if err = m.SetTaskPaused(task.ID, false); err != nil {
		t.Fatal(err)
	}
	if err = m.SetTaskStatus(task.ID, "running"); err != nil {
		t.Fatal(err)
	}
	oldAugment, oldResolve, oldPrompt := agent.ToolAugment, agent.ToolResolve, agent.PromptOverride
	agent.ToolAugment, agent.ToolResolve, agent.PromptOverride = nil, nil, nil
	defer func() { agent.ToolAugment, agent.ToolResolve, agent.PromptOverride = oldAugment, oldResolve, oldPrompt }()
	var workerCalls, plannerCalls atomic.Int32
	workerProvider := retestProvider{complete: func(_ context.Context, req llm.CompletionRequest) (llm.Message, string, llm.Usage, error) {
		if !strings.Contains(strings.Join(req.System, "\n"), task.Goal) {
			return llm.Message{}, "", llm.Usage{}, fmt.Errorf("missing persistent task objective")
		}
		call := workerCalls.Add(1)
		if call <= 2 {
			return llm.Message{Role: llm.RoleAssistant, Content: []llm.ContentBlock{{Type: llm.BlockThinking, Thinking: "thinking only"}}}, "end_turn", llm.Usage{}, nil
		}
		if call == 3 {
			input, _ := json.Marshal(map[string]any{"summary": "fixture verified", "intent_id": intentID, "evidence": "local synthetic fixture", "confidence": "observed"})
			return llm.Message{Role: llm.RoleAssistant, Content: []llm.ContentBlock{{Type: llm.BlockToolUse, ID: "fixture-record", Name: "record_fact", Input: input}}}, "tool_use", llm.Usage{}, nil
		}
		return llm.Message{Role: llm.RoleAssistant, Content: []llm.ContentBlock{llm.TextBlock("Fixture result recorded.")}}, "end_turn", llm.Usage{}, nil
	}}
	plannerProvider := retestProvider{complete: func(_ context.Context, _ llm.CompletionRequest) (llm.Message, string, llm.Usage, error) {
		call := plannerCalls.Add(1)
		if call <= 3 {
			return llm.Message{}, "", llm.Usage{}, fmt.Errorf("status 503: temporary fixture outage")
		}
		if call == 4 {
			facts, err := task.Store.ListByKind(db.KindFact, 100)
			if err != nil {
				return llm.Message{}, "", llm.Usage{}, err
			}
			var evidence int64
			for _, fact := range facts {
				if fact.State != "origin" {
					evidence = fact.ID
					break
				}
			}
			input, _ := json.Marshal(map[string]any{"goal_id": goals[0], "evidence_id": evidence})
			return llm.Message{Role: llm.RoleAssistant, Content: []llm.ContentBlock{{Type: llm.BlockToolUse, ID: "fixture-proof", Name: "prove_goal", Input: input}}}, "tool_use", llm.Usage{}, nil
		}
		return llm.Message{Role: llm.RoleAssistant, Content: []llm.ContentBlock{llm.TextBlock("Goal verified from the fixture evidence.")}}, "end_turn", llm.Usage{}, nil
	}}
	tx := transcript.NewStore(t.TempDir())
	worker := agent.NewWorker(workerProvider, "fixture", m.dir, tx, 32000, 10)
	worker.SetNonStreaming(func() bool { return true })
	planner := agent.NewPlanner(plannerProvider, "fixture", m.dir, tx, 32000, 10)
	planner.SetNonStreaming(func() bool { return true })
	e := NewEngine(m)
	e.debounce = time.Millisecond
	e.SetAuthoritativeAgentResolver(func(*Task) (*agent.Planner, *agent.Worker) { return planner, worker })
	ctx, cancel := context.WithCancel(context.Background())
	defer func() { cancel(); e.StopTask(task.ID) }()
	// The shared execution context may have been created by the planner first.
	// Per-run task data must be layered after this cache, not captured inside it.
	e.execContextFor(ctx, task.ID)
	e.Run(ctx, task)
	deadline := time.After(28 * time.Second)
	tick := time.NewTicker(20 * time.Millisecond)
	defer tick.Stop()
	for {
		select {
		case <-deadline:
			t.Fatalf("task did not finish: status=%s paused=%v worker=%d planner=%d", m.TaskStatus(task.ID), e.IsPaused(task.ID), workerCalls.Load(), plannerCalls.Load())
		case <-tick.C:
			if e.IsPaused(task.ID) || task.lifecycleSnapshot().Paused {
				t.Fatal("temporary failures paused the task")
			}
			if m.TaskStatus(task.ID) == "done" {
				if workerCalls.Load() < 4 || plannerCalls.Load() < 5 {
					t.Fatal("task completed without recovering and proving goal")
				}
				met, err := task.Store.AllGoalsMet()
				if err != nil || !met {
					t.Fatalf("met=%v err=%v", met, err)
				}
				return
			}
		}
	}
}

func TestRepeatedToolFailureSettlesAsExhaustedWithoutPausingTask(t *testing.T) {
	m, err := NewManager(t.TempDir(), "")
	if err != nil {
		t.Fatal(err)
	}
	defer m.Close()
	task, err := m.CreateTask(t.Name(), "local fixture", nil, 0, 0)
	if err != nil {
		t.Fatal(err)
	}
	defer m.DeleteTask(task.ID, DeleteTaskOptions{})
	id, err := task.Store.AddIntent(map[string]any{"summary": "check fixture"}, 5, nil, "planner")
	if err != nil {
		t.Fatal(err)
	}
	if err = task.Store.SetNodeState(id, "running"); err != nil {
		t.Fatal(err)
	}
	intent, err := task.Store.GetNode(id)
	if err != nil {
		t.Fatal(err)
	}
	oldAugment, oldResolve, oldPrompt := agent.ToolAugment, agent.ToolResolve, agent.PromptOverride
	agent.ToolAugment, agent.ToolResolve, agent.PromptOverride = nil, nil, nil
	defer func() { agent.ToolAugment, agent.ToolResolve, agent.PromptOverride = oldAugment, oldResolve, oldPrompt }()
	var requests, executions atomic.Int32
	failureTool := actool.Build(actool.Spec{Name: "fixture_failure", Description: "local fixture", Schema: map[string]any{"type": "object"}, Run: func(context.Context, json.RawMessage, *actool.ToolContext) (actool.Result, error) {
		executions.Add(1)
		return actool.Errorf("fixture precondition absent"), nil
	}})
	provider := retestProvider{complete: func(context.Context, llm.CompletionRequest) (llm.Message, string, llm.Usage, error) {
		n := requests.Add(1)
		return llm.Message{Role: llm.RoleAssistant, Content: []llm.ContentBlock{{Type: llm.BlockToolUse, ID: fmt.Sprint(n), Name: "fixture_failure", Input: json.RawMessage(`{}`)}}}, "tool_use", llm.Usage{}, nil
	}}
	worker := agent.NewWorker(provider, "fixture", m.dir, nil, 32000, 20, failureTool)
	worker.SetNonStreaming(func() bool { return true })
	e := NewEngine(m)
	ctx, cancel := context.WithTimeout(t.Context(), 3*time.Second)
	defer cancel()
	e.runIntent(ctx, task, "fixture", worker, intent, "", "")
	got, err := task.Store.GetNode(id)
	if err != nil || got.State != "exhausted" || e.IsPaused(task.ID) || executions.Load() != 3 {
		t.Fatalf("intent=%+v err=%v paused=%v toolCalls=%d modelCalls=%d", got, err, e.IsPaused(task.ID), executions.Load(), requests.Load())
	}
}
