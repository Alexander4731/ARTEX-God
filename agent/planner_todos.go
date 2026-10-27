package agent

import (
	"context"
	"encoding/json"
	"fmt"
	"sync"

	"github.com/Autumn-27/artex/db"
	"github.com/Autumn-27/norma/harness"
	"github.com/Autumn-27/norma/hook"
	actool "github.com/Autumn-27/norma/tool"
)

type plannerTodoHooks struct {
	harness.HookRunner
	store    *db.ExplorationStore
	mu       sync.Mutex
	revision int64
}

func durablePlannerTodos(ctx context.Context, ts *db.ExplorationStore, todos *actool.TodoStore, inner harness.HookRunner) (harness.HookRunner, error) {
	items, revision, err := ts.PlannerTodos()
	if err != nil {
		return nil, err
	}
	input, err := json.Marshal(map[string]any{"todos": items})
	if err != nil {
		return nil, err
	}
	if _, err := todos.Tool().Call(ctx, input, nil); err != nil {
		return nil, fmt.Errorf("恢复规划待办: %w", err)
	}
	if inner == nil {
		inner = hook.NewRegistry()
	}
	return &plannerTodoHooks{HookRunner: inner, store: ts, revision: revision}, nil
}

func (h *plannerTodoHooks) PreToolUse(ctx context.Context, name string, input []byte) (bool, string, []byte) {
	blocked, reason, updated := h.HookRunner.PreToolUse(ctx, name, input)
	if blocked || name != "TodoWrite" {
		return blocked, reason, updated
	}
	if len(updated) != 0 {
		input = updated
	}
	var value struct {
		Todos []actool.Todo `json:"todos"`
	}
	if err := json.Unmarshal(input, &value); err != nil {
		return true, err.Error(), nil
	}
	for _, todo := range value.Todos {
		if todo.Status != actool.TodoPending && todo.Status != actool.TodoInProgress && todo.Status != actool.TodoCompleted {
			return true, "待办状态无效", nil
		}
	}
	items, err := json.Marshal(value.Todos)
	if err != nil {
		return true, err.Error(), nil
	}
	h.mu.Lock()
	defer h.mu.Unlock()
	if err := h.store.SavePlannerTodos(items, h.revision); err != nil {
		return true, "规划待办保存失败: " + err.Error(), nil
	}
	h.revision++
	return false, "", updated
}
