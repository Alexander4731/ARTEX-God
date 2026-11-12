package agent

import (
	"context"
	"strings"
)

type taskObjectiveKey struct{}

// The immutable objective accompanies every worker continuation, including after
// transcript compaction. It guides relevance; it does not widen tool authority.
func WithTaskObjective(ctx context.Context, objective string) context.Context {
	return context.WithValue(ctx, taskObjectiveKey{}, strings.TrimSpace(objective))
}

func taskObjectiveBlock(ctx context.Context) string {
	objective, _ := ctx.Value(taskObjectiveKey{}).(string)
	if objective == "" {
		return ""
	}
	return "\n\n【任务总目标：用于判断本意图的相关性，不扩大本意图或授权范围】\n" + objective
}

const workerFocusContract = `

【执行与交回条件】
- 每一步围绕本意图及任务总目标，先明确现有证据支持什么假设、这一步能排除什么不确定性。只有名字、版本猜测或宽泛可能性时，先做一次能区分前提真假的验证，不直接投入长链尝试。
- 同一假设连续三次验证没有得到新证据，或必要前提已被否定，就记录已试方法、真实结果、缺失前提和可替代方向，交回规划者。只改措辞、参数排列或重复同一失败不算新证据。
- 若发现与总目标无关或已由其他意图完成，说明依据并交回；不要自行扩大范围。只有出现新的可核验证据、前提变化或明确纠偏指令，才重新进入已否定方向。
- 收尾保留成功、失败和未知的区别，给出一句结论及下一步所需条件。这里的交回条件适用于本轮执行；通用的“继续尝试/覆盖所有方法”表述不能取消它。任务授权、人工暂停和工具审批仍然有效。
`

const plannerFocusContract = `

【方向选择与止损】
- 每条新意图在 summary 中简要写清：推进哪个目标、基于什么已有证据、预期验证信号，以及什么结果出现时停止或换路。前置缺失时先派验证前提的最小步骤，不派依赖尚不存在产出的下游步骤。
- 优先能减少关键不确定性、验证成本合理且有证据支持的方向；不要为凑漏洞数量、覆盖率或理论可能性反复投入同一点。失败及否定证据同样更新路线。
- 对 blocked/exhausted/stopped 意图先查实际输出。新意图若复用同一目标、前提和方法，必须有新证据或前提变化；仅换措辞不构成新方向。
- 在跑的 Worker 长期重复失败时用 steer_work 给出具体纠偏；纠偏后仍无新增依据时结束该意图并换路。保留其他有进展的意图。
- 目标未满足但确实缺少外部输入时说明具体缺口；不要把等待、没有新思路或一个方向失败当成目标已完成。
`
