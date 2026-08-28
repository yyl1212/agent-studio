import { render, screen } from '@testing-library/react'
import userEvent from '@testing-library/user-event'
import { describe, expect, it, vi } from 'vitest'

import type { InvalidEdgeImpact } from './configDraft'
import { PortChangeConfirmation } from './PortChangeConfirmation'

const impact: InvalidEdgeImpact = {
  edgeId: 'edge-old', sourceNodeId: 'node-a', sourcePort: 'old', targetNodeId: 'node-b', targetPort: 'input',
}

describe('PortChangeConfirmation', () => {
  it('列出移除端口和每条受影响连线', () => {
    render(<PortChangeConfirmation open nodeTitle="动态节点" removedPorts={['output:old']} invalidEdges={[impact]} busy={false} onConfirm={vi.fn()} onCancel={vi.fn()} />)
    expect(screen.getByRole('dialog', { name: '确认端口变化' })).toBeVisible()
    expect(screen.getByText('output:old')).toBeVisible()
    expect(screen.getByText('node-a.old → node-b.input')).toBeVisible()
    expect(screen.getByText(/不会自动重连/)).toBeVisible()
  })

  it('确认、忙碌保护和取消后的焦点恢复保持独立', async () => {
    const onConfirm = vi.fn()
    const onCancel = vi.fn()
    const trigger = document.createElement('button')
    trigger.textContent = '应用配置'
    document.body.append(trigger)
    trigger.focus()
    const { rerender } = render(<PortChangeConfirmation open nodeTitle="动态节点" removedPorts={['output:old']} invalidEdges={[impact]} busy onConfirm={onConfirm} onCancel={onCancel} />)
    expect(screen.getByRole('button', { name: '确认应用' })).toBeDisabled()
    rerender(<PortChangeConfirmation open nodeTitle="动态节点" removedPorts={['output:old']} invalidEdges={[impact]} busy={false} onConfirm={onConfirm} onCancel={onCancel} />)
    await userEvent.click(screen.getByRole('button', { name: '取消' }))
    expect(onCancel).toHaveBeenCalledOnce()
    rerender(<PortChangeConfirmation open={false} nodeTitle="动态节点" removedPorts={[]} invalidEdges={[]} busy={false} onConfirm={onConfirm} onCancel={onCancel} />)
    expect(trigger).toHaveFocus()
    trigger.remove()
  })
})
