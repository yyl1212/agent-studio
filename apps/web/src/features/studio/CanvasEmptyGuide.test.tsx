import { render, screen } from '@testing-library/react'
import userEvent from '@testing-library/user-event'
import { expect, it, vi } from 'vitest'

import { CanvasEmptyGuide } from './CanvasEmptyGuide'

it('空图引导只报告添加意图', async () => {
  const user = userEvent.setup()
  const onAdd = vi.fn()
  render(
    <CanvasEmptyGuide position={{ x: 320, y: 180 }} onAdd={onAdd} />,
  )

  const button = screen.getByRole('button', {
    name: '在这里添加第一个节点',
  })
  expect(button).toHaveClass('nodrag', 'nopan')
  expect(button.parentElement).toHaveStyle({
    transform: 'translate(320px, 180px)',
  })
  await user.click(button)
  expect(onAdd).toHaveBeenCalledOnce()
})
