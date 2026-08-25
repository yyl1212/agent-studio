import { BrowserRouter, Link, Navigate, Route, Routes } from 'react-router-dom'

import { WorkflowListPage } from '../features/workflows/WorkflowListPage'
import { StudioPage } from '../features/studio/StudioPage'
import { AgentPage } from '../features/agent/AgentPage'
import { RunHistoryPage } from '../features/runs/RunHistoryPage'
import { NodePackageDirectoryPage } from '../features/node-packages/NodePackageDirectoryPage'
import { DebugPage } from '../features/studio/DebugPage'

function AppLayout() {
  return (
    <div className="app-shell">
      <header className="topbar">
        <Link className="brand" to="/workflows" aria-label="Agent Studio 首页">
          <span className="brand-mark" aria-hidden="true">AS</span>
          <h1>Agent Studio</h1>
        </Link>
		<nav aria-label="主导航">
			<Link to="/workflows">工作流</Link>
			<Link to="/node-packages">节点包</Link>
		</nav>
      </header>
      <Routes>
        <Route path="/" element={<Navigate to="/workflows" replace />} />
        <Route path="/workflows" element={<WorkflowListPage />} />
        <Route path="/workflows/:id" element={<StudioPage />} />
        <Route path="/workflows/:id/runs" element={<RunHistoryPage />} />
		<Route path="/workflows/:id/runs/:runId/debug" element={<DebugPage />} />
		<Route path="/node-packages" element={<NodePackageDirectoryPage />} />
        <Route path="/agents/:slug" element={<AgentPage />} />
        <Route path="*" element={<Placeholder title="页面不存在" />} />
      </Routes>
    </div>
  )
}

function Placeholder({ title }: { title: string }) {
  return <main className="page-container"><h2>{title}</h2><p>功能正在加载。</p></main>
}

export function AppRouter() {
  return <BrowserRouter><AppLayout /></BrowserRouter>
}
