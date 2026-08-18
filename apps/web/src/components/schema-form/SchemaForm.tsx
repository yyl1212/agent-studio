import type { ErrorObject } from 'ajv'
import Ajv2020 from 'ajv/dist/2020.js'
import { useEffect, useMemo, useState, type FormEvent } from 'react'

import { Field } from './Field'
import type { FormValue, JSONSchema } from './types'

interface SchemaFormProps {
  schema: JSONSchema
  value: FormValue
  onChange: (value: FormValue) => void
  onSubmit: (value: FormValue) => void | Promise<void>
  submitLabel: string
  disabled?: boolean
}

export function SchemaForm({ schema, value, onChange, onSubmit, submitLabel, disabled }: SchemaFormProps) {
  const [draft, setDraft] = useState<FormValue>(value)
  const [errors, setErrors] = useState<Record<string, string>>({})
  useEffect(() => setDraft(value), [value])
  const validate = useMemo(() => new Ajv2020({ allErrors: true, strict: false, useDefaults: true }).compile(schema as object), [schema])
  const properties = schema.properties ?? {}
  const order = [...(schema['x-ui-order'] ?? []), ...Object.keys(properties).filter((key) => !schema['x-ui-order']?.includes(key))]

  const update = (name: string, nextValue: unknown) => {
    const next = { ...draft, [name]: nextValue }
    setDraft(next)
    setErrors((current) => ({ ...current, [`/${name}`]: '' }))
    onChange(next)
  }

  const submit = async (event: FormEvent) => {
    event.preventDefault()
    const normalized = { ...draft }
    const jsonErrors: Record<string, string> = {}
    for (const [name, fieldSchema] of Object.entries(properties)) {
      if (fieldSchema['x-ui-widget'] === 'json' && typeof normalized[name] === 'string') {
        try {
          normalized[name] = JSON.parse(normalized[name] as string)
        } catch {
          jsonErrors[`/${name}`] = `${fieldSchema.title ?? name}必须是合法 JSON`
        }
      }
    }
    if (Object.keys(jsonErrors).length > 0) {
      setErrors(jsonErrors)
      return
    }
    if (!validate(normalized)) {
      setErrors(mapErrors(validate.errors ?? [], schema))
      return
    }
    setDraft(normalized)
    setErrors({})
    onChange(normalized)
    await onSubmit(normalized)
  }

  return (
    <form className="schema-form" noValidate onSubmit={submit}>
      {order.map((name) => {
        const fieldSchema = properties[name]
        if (!fieldSchema) return null
        return <Field key={name} path={name} name={name} schema={fieldSchema} value={draft[name]} required={schema.required?.includes(name) ?? false} errors={errors} onChange={(next) => update(name, next)} />
      })}
      <button className="primary-button" type="submit" disabled={disabled}>{submitLabel}</button>
    </form>
  )
}

function mapErrors(errors: ErrorObject[], rootSchema: JSONSchema) {
  const mapped: Record<string, string> = {}
  for (const error of errors) {
    const requiredName = error.keyword === 'required' ? String(error.params.missingProperty) : undefined
    const tokens = decodePointer(error.instancePath)
    if (requiredName !== undefined) tokens.push(requiredName)
    const path = `/${tokens.join('/')}`
    const name = tokens.at(-1) ?? ''
    const title = schemaAtPath(rootSchema, tokens)?.title ?? name
    if (!mapped[path]) {
      if (error.keyword === 'required') mapped[path] = `${title}为必填项`
      else if (error.keyword === 'minLength') mapped[path] = `${title}长度不能少于 ${String(error.params.limit)} 个字符`
      else if (error.keyword === 'maxLength') mapped[path] = `${title}长度不能超过 ${String(error.params.limit)} 个字符`
      else if (error.keyword === 'minItems') mapped[path] = `${title}至少需要 ${String(error.params.limit)} 项`
      else if (error.keyword === 'maxItems') mapped[path] = `${title}最多允许 ${String(error.params.limit)} 项`
      else if (error.keyword === 'minimum') mapped[path] = `${title}不能小于 ${String(error.params.limit)}`
      else if (error.keyword === 'maximum') mapped[path] = `${title}不能大于 ${String(error.params.limit)}`
      else if (error.keyword === 'pattern') mapped[path] = `${title}格式不正确`
      else mapped[path] = `${title}内容无效`
    }
  }
  return mapped
}

function decodePointer(path: string): string[] {
  if (path === '') return []
  return path.slice(1).split('/').map((token) => token.replace(/~1/g, '/').replace(/~0/g, '~'))
}

function schemaAtPath(rootSchema: JSONSchema, tokens: string[]): JSONSchema | undefined {
  let current: JSONSchema | undefined = rootSchema
  for (const token of tokens) {
    if (!current) return undefined
    if (current.type === 'array') current = current.items
    else current = current.properties?.[token]
  }
  return current
}
