import { render, screen } from '@testing-library/react'
import { describe, expect, it } from 'vitest'

import type { NodeDefinition } from '../../lib/api/client'
import { NodePlacementPreview } from './NodePlacementPreview'

const definition: NodeDefinition = {
  type: 'template',
  version: '1',
  title: '提示词模板',
  description: '将变量注入提示词',
  category: '文本',
  configSchema: {},
  inputs: [],
  outputs: [],
  capabilities: [],
  executionSafety: 'pure',
  package: {
    name: 'agent-studio.dev/core',
    displayName: 'Agent Studio Core',
    version: 'v0.5.0',
    license: 'Apache-2.0',
    repository: 'https://github.com/yyl1212/agent-studio',
    source: 'builtin',
  },
}

describe('NodePlacementPreview', () => {
  it('表达待放置节点和取消方式但不伪装成真实图元素', () => {
    const { container } = render(
      <NodePlacementPreview state={{ definition, position: { x: 320, y: 240 } }} />,
    )

    expect(screen.getByText('提示词模板')).toBeVisible()
    expect(screen.getByText('点击画布放置，Esc 取消')).toBeVisible()
    expect(container.querySelector('.react-flow__node')).not.toBeInTheDocument()
    expect(container.querySelector('.react-flow__edge')).not.toBeInTheDocument()
  })
})
