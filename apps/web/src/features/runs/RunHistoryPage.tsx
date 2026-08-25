import { Navigate, useParams } from 'react-router-dom'

export function RunHistoryPage() {
  const { id = '' } = useParams()
  return <Navigate replace to={`/runs?workflowId=${encodeURIComponent(id)}`} />
}
