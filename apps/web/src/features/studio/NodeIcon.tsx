interface NodeIconProps {
  category: string
  decorative?: boolean
}

type IconKind = 'ai' | 'text' | 'logic' | 'integration' | 'generic'

function iconKind(category: string): IconKind {
  const normalized = category.toLocaleLowerCase()
  if (normalized.includes('ai') || normalized.includes('模型')) return 'ai'
  if (normalized.includes('文本')) return 'text'
  if (normalized.includes('逻辑') || normalized.includes('流程')) return 'logic'
  if (normalized.includes('集成') || normalized.includes('扩展')) return 'integration'
  return 'generic'
}

function IconShape({ kind }: { kind: IconKind }) {
  switch (kind) {
    case 'ai':
      return <><path d="M12 3v3M12 18v3M3 12h3M18 12h3" /><circle cx="12" cy="12" r="5" /><path d="m8.5 8.5 7 7M15.5 8.5l-7 7" /></>
    case 'text':
      return <><path d="M5 6h14M8 10h8M8 14h8M5 18h14" /><path d="M5 10v4" /></>
    case 'logic':
      return <><circle cx="6" cy="6" r="2" /><circle cx="18" cy="12" r="2" /><circle cx="6" cy="18" r="2" /><path d="M8 6h3a3 3 0 0 1 3 3v0a3 3 0 0 0 2 2.8M8 18h3a3 3 0 0 0 3-3v0a3 3 0 0 1 2-2.8" /></>
    case 'integration':
      return <><path d="M8 8V5a2 2 0 0 1 4 0v3M12 16v3a2 2 0 0 0 4 0v-3" /><path d="M5 8h10v4a4 4 0 0 1-4 4H9a4 4 0 0 1-4-4Z" /></>
    default:
      return <><rect x="4" y="4" width="6" height="6" rx="1" /><rect x="14" y="4" width="6" height="6" rx="1" /><rect x="9" y="14" width="6" height="6" rx="1" /><path d="M7 10v2h5M17 10v2h-5M12 12v2" /></>
  }
}

export function NodeIcon({ category, decorative = false }: NodeIconProps) {
  return (
    <svg
      className="node-icon"
      viewBox="0 0 24 24"
      fill="none"
      stroke="currentColor"
      strokeWidth="1.75"
      strokeLinecap="round"
      strokeLinejoin="round"
      aria-hidden={decorative ? 'true' : undefined}
      aria-label={decorative ? undefined : `${category} 节点`}
      role={decorative ? undefined : 'img'}
    >
      <IconShape kind={iconKind(category)} />
    </svg>
  )
}
