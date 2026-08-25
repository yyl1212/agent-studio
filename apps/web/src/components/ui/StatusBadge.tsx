import type { ReactNode } from 'react'

export interface StatusBadgeProps {
  tone?: 'neutral' | 'info' | 'success' | 'warning' | 'danger'
  children: ReactNode
  className?: string
}

export function StatusBadge({ tone = 'neutral', children, className = '' }: StatusBadgeProps) {
  return <span className={`status-badge${className ? ` ${className}` : ''}`} data-tone={tone}>{children}</span>
}
