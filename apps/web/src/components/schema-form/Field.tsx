import { ArrayField } from './ArrayField'
import type { JSONSchema } from './types'

interface FieldProps {
  path: string
  name: string
  schema: JSONSchema
  value: unknown
  required: boolean
  errors: Record<string, string>
  onChange: (value: unknown) => void
  onBlur?: (path: string) => void
}

export function Field({ path, name, schema, value, required, errors, onChange, onBlur }: FieldProps) {
  const label = schema.title ?? name
  const id = `field-${path.replace(/[^a-zA-Z0-9_-]/g, '-')}`
  const error = errors[`/${path}`]
  const errorID = error ? `${id}-error` : undefined
  const common = {
    id,
    required,
    'aria-invalid': Boolean(error),
    'aria-describedby': errorID,
  }

  let control
  if (schema.type === 'boolean') {
    control = <input {...common} type="checkbox" checked={Boolean(value)} onChange={(event) => onChange(event.currentTarget.checked)} onBlur={() => onBlur?.(path)} />
  } else if (schema.type === 'array') {
    return <ArrayField id={path} label={label} schema={schema} value={Array.isArray(value) ? value : []} onChange={onChange} onBlur={onBlur} errors={errors} />
  } else if (schema.type === 'object' && schema['x-ui-widget'] !== 'json') {
    const objectValue = typeof value === 'object' && value !== null && !Array.isArray(value) ? value as Record<string, unknown> : {}
    const properties = schema.properties ?? {}
    const order = [...(schema['x-ui-order'] ?? []), ...Object.keys(properties).filter((key) => !schema['x-ui-order']?.includes(key))]
    return (
      <fieldset className="schema-object">
        <legend>{label}</legend>
        {schema.description && <small>{schema.description}</small>}
        {order.map((childName) => properties[childName] && (
          <Field
            key={childName}
            path={`${path}/${childName}`}
            name={childName}
            schema={properties[childName]}
            value={objectValue[childName]}
            required={schema.required?.includes(childName) ?? false}
            errors={errors}
            onChange={(childValue) => onChange({ ...objectValue, [childName]: childValue })}
            onBlur={onBlur}
          />
        ))}
      </fieldset>
    )
  } else if (schema.enum || schema['x-ui-widget'] === 'select') {
    control = (
      <select {...common} value={String(value ?? '')} onChange={(event) => onChange(schema.enum?.find((option) => String(option) === event.currentTarget.value) ?? event.currentTarget.value)} onBlur={() => onBlur?.(path)}>
        <option value="">请选择</option>
        {(schema.enum ?? []).map((option) => <option key={String(option)} value={String(option)}>{String(option)}</option>)}
      </select>
    )
  } else if (schema['x-ui-widget'] === 'textarea' || schema['x-ui-widget'] === 'code' || schema['x-ui-widget'] === 'json') {
    const text = schema['x-ui-widget'] === 'json' && typeof value !== 'string' ? JSON.stringify(value ?? {}, null, 2) : String(value ?? '')
    control = (
      <textarea
        {...common}
        data-widget={schema['x-ui-widget']}
        value={text}
        placeholder={schema['x-ui-placeholder']}
        onChange={(event) => onChange(event.currentTarget.value)}
        onBlur={(event) => {
          if (schema['x-ui-widget'] === 'json') {
            try { onChange(JSON.parse(event.currentTarget.value) as unknown) } catch { /* validation reports the retained text */ }
          }
          onBlur?.(path)
        }}
      />
    )
  } else {
    const numeric = schema.type === 'number' || schema.type === 'integer'
    control = (
      <input
        {...common}
        type={numeric ? 'number' : 'text'}
        value={value === undefined || value === null ? '' : String(value)}
        placeholder={schema['x-ui-placeholder']}
        min={schema.minimum}
        max={schema.maximum}
        minLength={schema.minLength}
        maxLength={schema.maxLength}
        pattern={schema.pattern}
        step={schema.type === 'integer' ? 1 : numeric ? 'any' : undefined}
        onChange={(event) => onChange(numeric ? (event.currentTarget.value === '' ? undefined : event.currentTarget.valueAsNumber) : event.currentTarget.value)}
        onBlur={() => onBlur?.(path)}
      />
    )
  }

  return (
    <div className="schema-field">
      <label htmlFor={id}>{label}</label>
      {schema.description && <small>{schema.description}</small>}
      {control}
      {error && <span className="field-error" id={errorID}>{error}</span>}
    </div>
  )
}
