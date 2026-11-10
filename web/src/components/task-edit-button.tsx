"use client";

import * as React from "react";

import { PencilIcon } from "lucide-react";
import { toast } from "sonner";

import { Button } from "@/components/ui/button";
import {
  Dialog,
  DialogContent,
  DialogDescription,
  DialogFooter,
  DialogHeader,
  DialogTitle,
  DialogTrigger,
} from "@/components/ui/dialog";
import { Input } from "@/components/ui/input";
import { Label } from "@/components/ui/label";
import { Textarea } from "@/components/ui/textarea";
import { api } from "@/lib/api";
import type { Task } from "@/lib/types";

export function TaskEditButton({
  task,
  onUpdated,
  iconOnly = false,
}: {
  task: Task;
  onUpdated: () => void;
  iconOnly?: boolean;
}) {
  const [open, setOpen] = React.useState(false);
  const [saving, setSaving] = React.useState(false);
  const [name, setName] = React.useState("");
  const [description, setDescription] = React.useState("");
  const [goal, setGoal] = React.useState("");
  const editable = task.paused === true || ["done", "failed", "timeout"].includes(task.status);
  const id = React.useId();
  function changeOpen(next: boolean) {
    if (saving) return;
    if (next) {
      setName(task.name ?? "");
      setDescription(task.description);
      setGoal(task.goal);
    }
    setOpen(next);
  }
  async function save(event: React.FormEvent) {
    event.preventDefault();
    const patch: Parameters<typeof api.updateTask>[1] = {};
    if (name.trim() !== (task.name ?? "")) patch.name = name.trim();
    if (editable && description.trim() !== task.description) patch.description = description.trim();
    if (editable && goal.trim() !== task.goal) patch.goal = goal.trim();
    if (!Object.keys(patch).length) {
      setOpen(false);
      return;
    }
    setSaving(true);
    try {
      await api.updateTask(task.id, patch);
      toast.success("任务已保存");
      setOpen(false);
      onUpdated();
    } catch (error) {
      toast.error((error as Error).message);
    } finally {
      setSaving(false);
    }
  }
  return (
    <Dialog open={open} onOpenChange={changeOpen}>
      <DialogTrigger asChild>
        <Button
          size={iconOnly ? "icon" : "sm"}
          variant={iconOnly ? "ghost" : "outline"}
          aria-label={`编辑任务 #${task.id}`}
          title="编辑任务"
        >
          <PencilIcon />
          {!iconOnly && "编辑任务"}
        </Button>
      </DialogTrigger>
      <DialogContent className="sm:max-w-xl">
        <form onSubmit={save} className="space-y-4">
          <DialogHeader>
            <DialogTitle>编辑任务 #{task.id}</DialogTitle>
            <DialogDescription>
              {editable
                ? "保存描述和目的不会自动重启任务。已有探索目标、执行记录和证据保留。"
                : "运行中可修改名称；修改描述和目的请先暂停任务。"}
            </DialogDescription>
          </DialogHeader>
          <div className="space-y-2">
            <Label htmlFor={`${id}-name`}>名称</Label>
            <Input
              id={`${id}-name`}
              value={name}
              onChange={(e) => setName(e.target.value)}
              maxLength={200}
              disabled={saving}
            />
          </div>
          <div className="space-y-2">
            <Label htmlFor={`${id}-description`}>描述</Label>
            <Textarea
              id={`${id}-description`}
              value={description}
              onChange={(e) => setDescription(e.target.value)}
              maxLength={16000}
              required
              disabled={saving || !editable}
              className="min-h-28"
            />
          </div>
          <div className="space-y-2">
            <Label htmlFor={`${id}-goal`}>目的</Label>
            <Textarea
              id={`${id}-goal`}
              value={goal}
              onChange={(e) => setGoal(e.target.value)}
              maxLength={16000}
              required
              disabled={saving || !editable}
              className="min-h-28"
            />
          </div>
          <DialogFooter>
            <Button type="button" variant="outline" disabled={saving} onClick={() => setOpen(false)}>
              取消
            </Button>
            <Button type="submit" disabled={saving}>
              {saving ? "保存中…" : "保存"}
            </Button>
          </DialogFooter>
        </form>
      </DialogContent>
    </Dialog>
  );
}
