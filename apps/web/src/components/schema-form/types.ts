export type UIWidget = 'text' | 'textarea' | 'select' | 'code' | 'json'

export interface JSONSchema {
  $schema?: string
  type?: 'object' | 'string' | 'number' | 'integer' | 'boolean' | 'array'
  title?: string
  description?: string
  default?: unknown
  enum?: Array<string | number>
  properties?: Record<string, JSONSchema>
  required?: string[]
  items?: JSONSchema
  minItems?: number
  maxItems?: number
  minimum?: number
  maximum?: number
  minLength?: number
  maxLength?: number
  pattern?: string
  additionalProperties?: boolean | JSONSchema
  'x-ui-widget'?: UIWidget
  'x-ui-order'?: string[]
  'x-ui-placeholder'?: string
}

export type FormValue = Record<string, unknown>

export function pointerChild(path: string, token: string): string {
  return `${path}/${token.replace(/~/g, '~0').replace(/\//g, '~1')}`
}
