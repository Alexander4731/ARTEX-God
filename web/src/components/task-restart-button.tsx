"use client";

import * as React from "react";

import { Loader2Icon, RotateCcwIcon } from "lucide-react";

import {
  AlertDialog,
  AlertDialogAction,
  AlertDialogCancel,
  AlertDialogContent,
  AlertDialogDescription,
  AlertDialogFooter,
  AlertDialogHeader,
  AlertDialogTitle,
  AlertDialogTrigger,
} from "@/components/ui/alert-dialog";
import { Button } from "@/components/ui/button";

export function TaskRestartButton({
  taskId,
  onRestart,
  iconOnly = false,
  disabled = false,
}: {
  taskId: string;
  onRestart: () => Promise<void>;
  iconOnly?: boolean;
  disabled?: boolean;
}) {
  const [pending, setPending] = React.useState(false);
  const submitting = React.useRef(false);
  async function restart() {
    if (submitting.current || disabled) return;
    submitting.current = true;
    setPending(true);
    try {
      await onRestart();
    } finally {
      submitting.current = false;
      setPending(false);
    }
  }
  return (
    <AlertDialog>
      <AlertDialogTrigger asChild>
        <Button
          size={iconOnly ? "icon" : "sm"}
          variant={iconOnly ? "ghost" : "outline"}
          disabled={disabled || pending}
          aria-label={`重新启动任务 #${taskId}`}
          title="重新启动任务"
        >
          {pending ? <Loader2Icon className="animate-spin" /> : <RotateCcwIcon />}
          {!iconOnly && "重新启动"}
        </Button>
      </AlertDialogTrigger>
      <AlertDialogContent>
        <AlertDialogHeader>
          <AlertDialogTitle>重新启动任务 #{taskId}？</AlertDialogTitle>
          <AlertDialogDescription>
            保留历史会话、证据和已有产出，将探索目标重新设为待核验并继续规划。
            本轮时限重新计时，已完成的操作不会整批重跑；达到并发上限时自动排队。
          </AlertDialogDescription>
        </AlertDialogHeader>
        <AlertDialogFooter>
          <AlertDialogCancel>取消</AlertDialogCancel>
          <AlertDialogAction disabled={disabled || pending} onClick={() => void restart()}>
            重新启动
          </AlertDialogAction>
        </AlertDialogFooter>
      </AlertDialogContent>
    </AlertDialog>
  );
}
