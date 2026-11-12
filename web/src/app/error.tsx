"use client";

export default function ErrorPage({ reset }: { error: Error & { digest?: string }; reset: () => void }) {
  return (
    <div role="alert" className="flex min-h-64 flex-col items-center justify-center gap-4 p-6">
      <h1 className="text-lg font-semibold">页面暂时无法显示</h1>
      <p className="text-muted-foreground text-sm">可以重试加载，或返回任务列表。</p>
      <div className="flex gap-4">
        <button type="button" className="underline" onClick={reset}>重试</button>
        <a className="underline" href="/function/tasks/">任务列表</a>
      </div>
    </div>
  );
}
