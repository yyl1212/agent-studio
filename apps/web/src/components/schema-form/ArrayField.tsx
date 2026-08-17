import type { JSONSchema } from './types'
import { Field } from './Field'

interface ArrayFieldProps {
  id: string
  label: string
  schema: JSONSchema
  value: unknown[]
  onChange: (value: unknown[]) => void
  errors: Record<string, string>
}

export function ArrayField({ id, label, schema, value, onChange, errors }: ArrayFieldProps) {
  const itemSchema = schema.items ?? { type: 'string' }
  return (
    <fieldset className="schema-array">
      <legend>{label}</legend>
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
          />
          <button type="button" onClick={() => onChange(value.filter((_, itemIndex) => itemIndex !== index))}>移除</button>
        </div>
      ))}
      <button type="button" onClick={() => onChange([...value, itemSchema.default ?? ''])}>添加一项</button>
    </fieldset>
  )
}
