import { Link } from 'react-router-dom'

import { WorkflowManagementView } from './WorkflowManagementView'

export function ManagementPage({ section }: { section: 'workflows' | 'runs' }) {
  return <main className="page-container management-page">
    <nav className="management-tabs" aria-label="管理台">
      <Link to="/workflows" aria-current={section === 'workflows' ? 'page' : undefined}>工作流</Link>
      <Link to="/runs" aria-current={section === 'runs' ? 'page' : undefined}>运行</Link>
    </nav>
    {section === 'workflows' ? <WorkflowManagementView /> : <div className="state-card">运行管理即将就绪</div>}
  </main>
}
