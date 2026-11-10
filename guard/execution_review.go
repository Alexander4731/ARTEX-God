package guard

import (
	"context"
	"encoding/json"

	"github.com/Autumn-27/artex/intercept"
	"github.com/Autumn-27/norma/harness"
	"github.com/Autumn-27/norma/tool"
)

// WithExecutionReview runs supplied tools' existing pre-hooks inside Call, where
// the SDK exposes ToolUseID. All policy checks still run before the handler.
// SDK-added tools retain the original hook path. Post/Stop hooks stay with SDK.
func WithExecutionReview(inner harness.HookRunner, tools []tool.CoreTool, sdkOwned ...string) (harness.HookRunner, []tool.CoreTool) {
	if inner == nil {
		return inner, tools
	}
	hooks := &executionHooks{HookRunner: inner, wrapped: map[string]bool{}}
	excluded := map[string]bool{}
	for _, name := range sdkOwned {
		excluded[name] = true
	}
	wrapped := make([]tool.CoreTool, len(tools))
	for index, t := range tools {
		if excluded[t.Name()] {
			wrapped[index] = t
			continue
		}
		hooks.wrapped[t.Name()] = true
		wrapped[index] = &reviewedExecution{CoreTool: t, hooks: inner}
	}
	return hooks, wrapped
}

type executionHooks struct {
	harness.HookRunner
	wrapped map[string]bool
}

func (h *executionHooks) PreToolUse(ctx context.Context, name string, input []byte) (bool, string, []byte) {
	if h.wrapped[name] {
		return false, "", nil
	}
	return h.HookRunner.PreToolUse(ctx, name, input)
}

type reviewedExecution struct {
	tool.CoreTool
	hooks harness.HookRunner
}

func (t *reviewedExecution) AcceptsRawInput() bool {
	raw, ok := t.CoreTool.(interface{ AcceptsRawInput() bool })
	return ok && raw.AcceptsRawInput()
}

func (t *reviewedExecution) Call(ctx context.Context, input json.RawMessage, tc *tool.ToolContext) (tool.Result, error) {
	if tc != nil {
		ctx = intercept.WithExecutionCall(ctx, tc.ToolUseID)
	}
	if blocked, message, updated := t.hooks.PreToolUse(ctx, t.Name(), input); blocked {
		return tool.Errorf("Blocked by hook: " + message), nil
	} else if len(updated) > 0 {
		input = updated
	}
	if err := ctx.Err(); err != nil {
		return tool.Result{}, err
	}
	return t.CoreTool.Call(ctx, input, tc)
}
