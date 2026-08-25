import type { JSONSchema } from './types'
import { Field } from './Field'

interface ArrayFieldProps {
  id: string
  label: string
  description?: string
  schema: JSONSchema
  value: unknown[]
  onChange: (value: unknown[]) => void
  onBlur?: (path: string) => void
  errors: Record<string, string>
}

export function ArrayField({ id, label, description, schema, value, onChange, onBlur, errors }: ArrayFieldProps) {
  const itemSchema = schema.items ?? { type: 'string' }
  const atMinimum = schema.minItems !== undefined && value.length <= schema.minItems
  const atMaximum = schema.maxItems !== undefined && value.length >= schema.maxItems
  const error = errors[`/${id}`]
  const fieldID = `field-${id.replace(/[^a-zA-Z0-9_-]/g, '-')}`
  const errorID = error ? `${fieldID}-error` : undefined
  const descriptionID = description ? `${fieldID}-description` : undefined
  return (
    <fieldset className="schema-array" aria-invalid={Boolean(error)} aria-describedby={[descriptionID, errorID].filter(Boolean).join(' ') || undefined}>
      <legend>{label}</legend>
      {description && <small id={descriptionID}>{description}</small>}
      {value.map((item, index) => (
        <div className="schema-array-row" key={`${id}-${index}`}>
          <Field
            path={`${id}/${index}`}
            name={`${label} ${index + 1}`}
            schema={itemSchema}
            value={item}
            required
            errors={errors}
            onChange={(itemValue) => {
              const next = [...value]
              next[index] = itemValue
              onChange(next)
            }}
            onBlur={onBlur}
          />
          <button type="button" aria-label={`移除${label} ${index + 1}`} disabled={atMinimum} onClick={() => onChange(value.filter((_, itemIndex) => itemIndex !== index))}>移除</button>
        </div>
      ))}
      {error && <span className="field-error" id={errorID}>{error}</span>}
      <button type="button" disabled={atMaximum} onClick={() => onChange([...value, defaultItem(itemSchema)])}>添加一项</button>
    </fieldset>
  )
}

function defaultItem(schema: JSONSchema): unknown {
  if (schema.default !== undefined) return structuredClone(schema.default)
  if (schema.type === 'object') {
    return Object.fromEntries(
      Object.entries(schema.properties ?? {})
        .map(([name, child]) => [name, defaultItem(child)] as const)
        .filter(([, child]) => child !== undefined),
    )
  }
  if (schema.type === 'array') return []
  if (schema.type === 'boolean') return false
  if (schema.enum?.length) return structuredClone(schema.enum[0])
  if (schema.type === 'string') return ''
  return undefined
}
