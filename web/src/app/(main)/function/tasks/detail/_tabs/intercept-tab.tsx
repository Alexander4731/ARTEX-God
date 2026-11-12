"use client";

import * as React from "react";

import { toast } from "sonner";

import { ApprovalRecords } from "@/components/approval-records";
import { ExecutionRecoveries } from "@/components/execution-recoveries";
import { Switch } from "@/components/ui/switch";
import { api } from "@/lib/api";

export function InterceptTab({ taskId }: { taskId: string }) {
  return (
    <div className="space-y-4">
      <TaskToolPolicy key={taskId} taskId={taskId} />
      <ExecutionRecoveries key={`recovery-${taskId}`} taskId={taskId} />
      <ApprovalRecords key={taskId} taskId={taskId} />
    </div>
  );
}

function TaskToolPolicy({ taskId }: { taskId: string }) {
  const [allowAll, setAllowAll] = React.useState<boolean | null>(null);
  const [saving, setSaving] = React.useState(false);
  const id = React.useId();
  React.useEffect(() => {
    let active = true;
    api
      .taskToolPolicy(taskId)
      .then((policy) => {
        if (active) setAllowAll(policy.allow_all);
      })
      .catch((error: Error) => {
        if (active) toast.error("加载任务工具策略失败：" + error.message);
      });
    return () => {
      active = false;
    };
  }, [taskId]);

  async function save(next: boolean) {
    setSaving(true);
    try {
      const policy = await api.setTaskToolPolicy(taskId, next);
      setAllowAll(policy.allow_all);
      toast.success(policy.allow_all ? "仅此任务的工具将直接放行" : "此任务已恢复使用全局工具策略");
    } catch (error) {
      toast.error("保存失败：" + (error as Error).message);
    } finally {
      setSaving(false);
    }
  }

  return (
    <div className="flex items-center justify-between gap-4 rounded-lg border p-4">
      <div className="space-y-1">
        <label htmlFor={id} className="cursor-pointer text-sm font-medium">
          仅此任务直接放行所有工具
        </label>
        <p className="text-xs text-muted-foreground">
          开启后跳过此任务的工具拦截规则和模型审批，保留执行记录。其他任务仍使用全局设置。
        </p>
      </div>
      <Switch id={id} checked={allowAll === true} disabled={allowAll === null || saving} onCheckedChange={save} />
    </div>
  );
}
