export function App() {
  return (
    <div className="app-shell">
      <header className="topbar">
        <a className="brand" href="/" aria-label="Agent Studio 首页">
          <span className="brand-mark" aria-hidden="true">
            AS
          </span>
          <h1>Agent Studio</h1>
        </a>
        <nav aria-label="主导航">
          <a href="/workflows">工作流</a>
        </nav>
      </header>
      <main className="welcome-panel">
        <p className="eyebrow">轻量化 Agent 开发系统</p>
        <h2>从画布开始构建你的 Agent</h2>
        <p>通过可扩展节点连接工作流，并从开始参数自动生成运行页面。</p>
      </main>
    </div>
  )
}
