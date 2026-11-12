"use client";

import * as React from "react";

import { toast } from "sonner";

import { Button } from "@/components/ui/button";
import { Switch } from "@/components/ui/switch";
import { api } from "@/lib/api";
import type { ExecutionRecovery } from "@/lib/execution-types";

const states: Record<string, string> = {
  none: "等待核验",
  pending: "等待自动核验",
  checking: "正在核验",
  resolved: "已完成恢复",
  parked: "已搁置",
  waiting_input: "需要处理",
  unknown: "执行结果未知",
  prepared: "已登记执行",
  running: "仍在执行",
  succeeded: "工具正常返回",
  failed: "工具执行失败",
  canceled: "进程已取消",
  not_started: "未启动",
};
const effects: Record<string, string> = {
  indeterminate: "目标效果未确定",
  achieved: "目标已达成",
  partial: "已有部分结果",
  not_achieved: "目标未达成",
  not_applicable: "无需效果核验",
};

export function ExecutionRecoveries({ taskId }: { taskId: string }) {
  const [mode, setMode] = React.useState<"legacy" | "observe" | "auto" | null>(null);
  const [items, setItems] = React.useState<ExecutionRecovery[]>([]);
  const [error, setError] = React.useState("");
  const [saving, setSaving] = React.useState(false);
  const [pageCount, setPageCount] = React.useState(1);
  const [hasMore, setHasMore] = React.useState(false);
  const [loading, setLoading] = React.useState(false);
  const [revision, refresh] = React.useReducer((n: number) => n + 1, 0);
  const id = React.useId();
  React.useEffect(() => {
    let alive = true;
    let timer: ReturnType<typeof setTimeout>;
    async function load() {
      if (alive) setLoading(true);
      try {
        const [policy, firstPage] = await Promise.all([api.executionPolicy(taskId), api.executionRecoveries(taskId)]);
        const rows = [...firstPage];
        let page = firstPage;
        for (let index = 1; index < pageCount && page.length === 100 && alive; index++) {
          page = await api.executionRecoveries(taskId, page[page.length - 1].id);
          rows.push(...page);
        }
        if (alive) {
          setMode(policy.mode);
          setItems(rows);
          setError("");
          setHasMore(page.length === 100);
        }
      } catch (e) {
        if (alive) setError((e as Error).message);
      } finally {
        if (alive) {
          setLoading(false);
          timer = setTimeout(load, 5000);
        }
      }
    }
    void load();
    return () => {
      alive = false;
      clearTimeout(timer);
    };
  }, [taskId, revision, pageCount]);

  async function save(enabled: boolean) {
    setSaving(true);
    try {
      const policy = await api.setExecutionPolicy(taskId, enabled ? "auto" : "legacy");
      setMode(policy.mode);
      refresh();
      toast.success(enabled ? "已开启此任务的自动核验与恢复" : "已恢复此任务的原核验方式");
    } catch (e) {
      toast.error((e as Error).message);
    } finally {
      setSaving(false);
    }
  }

  const waiting = items.filter(
    (item) =>
      item.recovery_state === "waiting_input" ||
      (item.write_blocked && ["resolved", "parked"].includes(item.recovery_state)),
  ).length;
  return (
    <section className="space-y-3 rounded-lg border p-4" aria-label="执行结果与恢复">
      <div className="flex items-center justify-between gap-4">
        <div className="space-y-1">
          <label htmlFor={id} className="text-sm font-medium">
            仅此任务自动核验与恢复
          </label>
          <p className="text-xs text-muted-foreground">
            中断后保存结果并自动检查，必要时由模型据证核验。其他任务与全局工具权限不变。
          </p>
        </div>
        <Switch id={id} checked={mode === "auto"} disabled={mode === null || saving} onCheckedChange={save} />
      </div>
      {error && (
        <p role="alert" className="text-sm text-destructive">
          加载失败：{error}
        </p>
      )}
      {mode === "observe" && <p className="text-sm">当前为观察模式：记录核验建议，保留原调度方式。</p>}
      <div className="flex items-center justify-between">
        <p className="text-sm">
          执行恢复记录 · 已加载 {items.length} 条{waiting > 0 ? ` · 已加载记录中 ${waiting} 项需要处理` : ""}
        </p>
        <Button size="sm" variant="ghost" onClick={refresh}>
          刷新
        </Button>
      </div>
      {items.length === 0 && (
        <p className="text-xs text-muted-foreground">暂无执行恢复记录。执行前审批仍在下方单独显示。</p>
      )}
      {items.map((item) => (
        <RecoveryItem key={item.id} item={item} taskId={taskId} onChange={refresh} />
      ))}
      {hasMore && (
        <Button variant="outline" size="sm" disabled={loading} onClick={() => setPageCount((n) => n + 1)}>
          {loading ? "正在加载…" : "加载更早的恢复记录"}
        </Button>
      )}
    </section>
  );
}

function RecoveryItem({ item, taskId, onChange }: { item: ExecutionRecovery; taskId: string; onChange: () => void }) {
  const [note, setNote] = React.useState("");
  const [busy, setBusy] = React.useState(false);
  async function decide(decision: "recheck" | "park" | "accept_partial") {
    if (decision !== "recheck" && !note.trim()) {
      toast.error("请填写处理依据");
      return;
    }
    setBusy(true);
    try {
      await api.decideExecutionRecovery(taskId, item.id, item.version, decision, note.trim() || "用户请求重新自动核验");
      onChange();
      toast.success("已保存处理决定；原始执行记录保留");
    } catch (e) {
      toast.error((e as Error).message);
      onChange();
    } finally {
      setBusy(false);
    }
  }
  const actionable =
    item.recovery_state === "waiting_input" ||
    item.recovery_state === "parked" ||
    (item.recovery_state === "resolved" && item.write_blocked);
  return (
    <details className="rounded-md border p-3">
      <summary className="cursor-pointer text-sm">
        #{item.id} · {item.tool_name} · {states[item.recovery_state] ?? item.recovery_state} ·{" "}
        {states[item.execution_state] ?? item.execution_state}
      </summary>
      <div className="mt-3 space-y-3 text-sm">
        <p>
          意图 #{item.intent_id || "—"} · {effects[item.effect_state] ?? item.effect_state}
        </p>
        {item.process.cancel_cause && <p>中断原因：{item.process.cancel_cause}</p>}
        {item.process.exit_code !== undefined && <p>进程退出码：{item.process.exit_code}</p>}
        {item.decision.verdict?.reason && <p>{item.decision.verdict.reason}</p>}
        {item.write_blocked && (
          <p>
            原操作是否仍有写入尚未确定，同一任务的潜在冲突写入暂缓，仍可读取已有结果。真实执行回执到达后会自动解除；也可在下方记录人工处理依据。
          </p>
        )}
        {item.last_error && <p className="text-muted-foreground">最近核验情况：{item.last_error}</p>}
        <pre className="max-h-40 overflow-auto whitespace-pre-wrap rounded bg-muted p-2 text-xs">
          {JSON.stringify(item.input, null, 2)}
        </pre>
        <pre className="max-h-64 overflow-auto whitespace-pre-wrap rounded bg-muted p-2 text-xs">
          {item.output.slice(0, 16000) || "尚无可读取的工具输出"}
        </pre>
        {(item.output_truncated || item.output.length > 16000) && (
          <p className="text-xs text-muted-foreground">
            此处显示部分输出。已保存的进程输出可在执行回执目录查阅，单次最多保留 51 MiB。
          </p>
        )}
        {actionable && (
          <div className="space-y-2">
            <label htmlFor={`recovery-note-${item.id}`}>处理依据</label>
            <textarea
              id={`recovery-note-${item.id}`}
              className="min-h-20 w-full rounded border p-2"
              value={note}
              onChange={(event) => setNote(event.target.value)}
              placeholder="记录已检查的结果，以及为何可以接受部分结果或放弃此分支"
            />
            <div className="flex flex-wrap gap-2">
              <Button size="sm" variant="outline" disabled={busy} onClick={() => void decide("recheck")}>
                重新自动核验
              </Button>
              <Button size="sm" variant="outline" disabled={busy} onClick={() => void decide("park")}>
                搁置此分支
              </Button>
              <Button size="sm" disabled={busy} onClick={() => void decide("accept_partial")}>
                接受部分结果并继续
              </Button>
            </div>
          </div>
        )}
      </div>
    </details>
  );
}
