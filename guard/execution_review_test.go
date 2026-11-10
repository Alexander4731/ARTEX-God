package guard

import (
	"context"
	"encoding/json"
	"testing"

	"github.com/Autumn-27/norma/harness"
	"github.com/Autumn-27/norma/tool"
)

type executionProbeHooks struct {
	harness.HookRunner
	blocked bool
	reviews int
}

func (h *executionProbeHooks) PreToolUse(context.Context, string, []byte) (bool, string, []byte) {
	h.reviews++
	return h.blocked, "fixture denial", []byte(`{"checked":true}`)
}

func TestExecutionReviewCannotBypassPolicy(t *testing.T) {
	for _, blocked := range []bool{false, true} {
		h := &executionProbeHooks{blocked: blocked}
		calls := 0
		probe := tool.Build(tool.Spec{Name: "fixture", RawInput: true,
			Run: func(_ context.Context, input json.RawMessage, _ *tool.ToolContext) (tool.Result, error) {
				calls++
				if string(input) != `{"checked":true}` {
					t.Fatalf("updated input lost: %s", input)
				}
				return tool.Text("ok"), nil
			}})
		hooks, wrapped := WithExecutionReview(h, []tool.CoreTool{probe})
		hooks.PreToolUse(t.Context(), probe.Name(), []byte(`{}`))
		if h.reviews != 0 {
			t.Fatal("review did not defer to exact execution")
		}
		result, err := wrapped[0].Call(t.Context(), []byte(`{}`), &tool.ToolContext{ToolUseID: "exact"})
		if err != nil || h.reviews != 1 || result.IsError != blocked {
			t.Fatalf("result=%+v err=%v reviews=%d", result, err, h.reviews)
		}
		if (calls == 0) != blocked {
			t.Fatalf("blocked=%v calls=%d", blocked, calls)
		}
		if !wrapped[0].(interface{ AcceptsRawInput() bool }).AcceptsRawInput() {
			t.Fatal("raw input contract lost")
		}
		// The SDK may discard a supplied same-name tool. Its global pre-hook
		// must still run, including when that hook blocks the generated tool.
		hooks, wrapped = WithExecutionReview(h, []tool.CoreTool{probe}, "fixture")
		got, _, _ := hooks.PreToolUse(t.Context(), "fixture", []byte(`{}`))
		if got != blocked || h.reviews != 2 || wrapped[0] != probe {
			t.Fatal("SDK replacement bypassed review")
		}
	}
}
