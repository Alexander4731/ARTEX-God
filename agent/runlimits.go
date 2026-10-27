package agent

import (
	"context"
	"errors"
	"fmt"
	"io/fs"
	"time"

	"github.com/Autumn-27/artex/intercept"
	"github.com/Autumn-27/norma/agentcore"
	"github.com/Autumn-27/norma/harness"
	actool "github.com/Autumn-27/norma/tool"
)

// The SDK's tool deadline does not cover PreToolUse. Give approval the same
// deadline and bound the complete run, including its settlement phase.
func runDeadline(ctx context.Context, duration time.Duration) (context.Context, context.CancelFunc) {
	if duration <= 0 {
		return ctx, func() {}
	}
	deadline := time.Now().Add(duration)
	ctx = intercept.WithApprovalDeadline(ctx, deadline)
	return context.WithDeadlineCause(ctx, deadline.Add(30*time.Second), AbortRunHardTimeout)
}

func resumeSession(s *agentcore.Session, id string) error {
	if err := s.Resume(id); err != nil && !errors.Is(err, fs.ErrNotExist) {
		return fmt.Errorf("恢复会话 %s 失败: %w", id, err)
	}
	return nil
}

var finalPlannerAllowed = map[string]bool{
	"graph_overview": true, "list_goals": true, "list_facts": true,
	"list_findings": true, "get_worker_output": true, "get_worker_trace": true,
	"search_all_worker_traces": true, "prove_goal": true, "goal_met": true,
}

type finalPlannerHooks struct{ harness.HookRunner }

func (h finalPlannerHooks) PreToolUse(ctx context.Context, name string, input []byte) (bool, string, []byte) {
	if !finalPlannerAllowed[name] {
		return true, "终局判定仅允许读取证据和确认目标", nil
	}
	return h.HookRunner.PreToolUse(ctx, name, input)
}

func finalPlannerTools(tools []actool.CoreTool) []actool.CoreTool {
	out := make([]actool.CoreTool, 0, len(tools))
	for _, tool := range tools {
		if finalPlannerAllowed[tool.Name()] {
			out = append(out, tool)
		}
	}
	return out
}
