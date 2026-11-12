"use client";

export default function GlobalError({ reset }: { error: Error & { digest?: string }; reset: () => void }) {
  return (
    <html lang="zh-CN"><body>
      <main role="alert" style={{ margin: "15vh auto", maxWidth: 560, padding: 24, fontFamily: "sans-serif" }}>
        <h1>应用暂时无法显示</h1>
        <p>请重试加载。若问题持续，可刷新页面。</p>
        <button type="button" onClick={reset}>重试</button>{" "}
        <button type="button" onClick={() => window.location.reload()}>刷新页面</button>
      </main>
    </body></html>
  );
}
