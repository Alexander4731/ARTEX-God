package guard

import (
	"bytes"
	"context"
	"encoding/json"
	"strings"

	"github.com/Autumn-27/artex/intercept"
	"github.com/Autumn-27/norma/harness"
	actool "github.com/Autumn-27/norma/tool"
)

// ReviewDescriber exposes the actual operation produced by a tool's defaults
// and templates without executing it.
type ReviewDescriber interface {
	ReviewInput(json.RawMessage) (json.RawMessage, bool, error)
}

func DescribeInput(tool actool.CoreTool, input json.RawMessage) (json.RawMessage, bool, error) {
	if describer, ok := tool.(ReviewDescriber); ok {
		return describer.ReviewInput(input)
	}
	name := tool.Name()
	external := strings.HasPrefix(name, "mcp__") || name == "Bash" || name == "shell_open" || name == "shell_send"
	return input, external && !tool.IsReadOnly(input), nil
}

type externalReviewKey struct{}

type reviewHooks struct {
	harness.HookRunner
	tools map[string]actool.CoreTool
}

func WithToolReview(inner harness.HookRunner, tools []actool.CoreTool) harness.HookRunner {
	if inner == nil {
		return nil
	}
	h := &reviewHooks{HookRunner: inner, tools: make(map[string]actool.CoreTool, len(tools))}
	for _, tool := range tools {
		h.tools[tool.Name()] = tool
	}
	return h
}

func (h *reviewHooks) PreToolUse(ctx context.Context, name string, input []byte) (bool, string, []byte) {
	if ctx.Err() != nil {
		return true, "执行已取消", nil
	}
	tool := h.tools[name]
	if tool == nil {
		return h.HookRunner.PreToolUse(ctx, name, input)
	}
	ctx = intercept.WithCall(ctx, name, input)
	description, external, err := DescribeInput(tool, input)
	if err != nil {
		return true, "无法生成执行描述: " + err.Error(), nil
	}
	ctx = context.WithValue(ctx, externalReviewKey{}, external)
	blocked, message, updated := h.HookRunner.PreToolUse(ctx, name, description)
	if len(updated) != 0 {
		if bytes.Equal(description, input) {
			return blocked, message, updated
		}
		return true, "执行描述不能直接改写工具参数，请用新参数重新调用", nil
	}
	// A description is for authorization only. The real handler receives the
	// original arguments and applies the same immutable defaults/templates.
	return blocked, message, nil
}
