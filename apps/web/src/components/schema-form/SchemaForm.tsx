import type { ErrorObject, ValidateFunction } from 'ajv'
import Ajv2020 from 'ajv/dist/2020.js'
import { useEffect, useMemo, useState, type FormEvent } from 'react'

import { Field } from './Field'
import type { FormValue, JSONSchema } from './types'

const ajv = new Ajv2020({ allErrors: true, strict: false, useDefaults: true })
const validators = new WeakMap<object, ValidateFunction>()

export interface FormValidation {
  normalized: FormValue
  errors: Record<string, string>
  valid: boolean
}

interface SchemaFormProps {
  schema: JSONSchema
  value: FormValue
  onChange: (value: FormValue) => void
  onSubmit: (value: FormValue) => void | Promise<void>
  submitLabel: string
  disabled?: boolean
  groupOptional?: boolean
  onValidationChange?: (validation: FormValidation) => void
}

export function SchemaForm({ schema, value, onChange, onSubmit, submitLabel, disabled, groupOptional, onValidationChange }: SchemaFormProps) {
  const [draft, setDraft] = useState<FormValue>(value)
  const [errors, setErrors] = useState<Record<string, string>>({})
  useEffect(() => setDraft(value), [value])
  const properties = schema.properties ?? {}
  const order = [...(schema['x-ui-order'] ?? []), ...Object.keys(properties).filter((key) => !schema['x-ui-order']?.includes(key))]
  const validation = useMemo(() => validateFormValue(schema, draft), [draft, schema])
  useEffect(() => onValidationChange?.(validation), [onValidationChange, validation])

  const update = (name: string, nextValue: unknown) => {
    const next = { ...draft, [name]: nextValue }
    setDraft(next)
    setErrors((current) => {
      const nextErrors = { ...current }
      delete nextErrors[`/${name}`]
      return nextErrors
    })
    onChange(next)
  }

  const submit = async (event: FormEvent) => {
    event.preventDefault()
    if (!validation.valid) {
      setErrors(validation.errors)
      return
    }
    setDraft(validation.normalized)
    setErrors({})
    onChange(validation.normalized)
    await onSubmit(validation.normalized)
  }

  const renderField = (name: string) => {
    const fieldSchema = properties[name]
    if (!fieldSchema) return null
    return <Field key={name} path={name} name={name} schema={fieldSchema} value={draft[name]} required={schema.required?.includes(name) ?? false} errors={errors} onChange={(next) => update(name, next)} onBlur={(path) => setErrors((current) => {
      const nextErrors = { ...current }
      const pathKey = `/${path}`
      if (validation.errors[pathKey]) nextErrors[pathKey] = validation.errors[pathKey]
      else delete nextErrors[pathKey]
      return nextErrors
    })} />
  }
  const required = order.filter((name) => schema.required?.includes(name))
  const optional = order.filter((name) => !schema.required?.includes(name))
  const firstError = Object.keys(errors)[0]
  const focusFirstError = () => document.getElementById(`field-${firstError.slice(1).replace(/[^a-zA-Z0-9_-]/g, '-')}`)?.focus()

  return (
    <form className="schema-form" noValidate onSubmit={submit}>
      {firstError && <button className="form-error-summary" type="button" onClick={focusFirstError}>{Object.keys(errors).length} 项需要处理</button>}
      {groupOptional ? <>
        {required.length > 0 && <section className="schema-section"><h3>必要配置</h3>{required.map(renderField)}</section>}
        {optional.length > 0 && <details className="schema-optional"><summary>可选配置</summary>{optional.map(renderField)}</details>}
      </> : order.map(renderField)}
      <button className="primary-button" type="submit" disabled={disabled}>{submitLabel}</button>
    </form>
  )
}

export function validateFormValue(schema: JSONSchema, value: FormValue): FormValidation {
  const normalized = structuredClone(value)
  const jsonErrors: Record<string, string> = {}
  normalizeJSONWidgets(schema, normalized, [], jsonErrors)
  if (Object.keys(jsonErrors).length > 0) return { normalized, errors: jsonErrors, valid: false }
  let validate = validators.get(schema)
  if (!validate) {
    validate = ajv.compile(schema as object)
    validators.set(schema, validate)
  }
  const valid = validate(normalized)
  const errors = valid ? {} : mapErrors(validate.errors ?? [], schema)
  return { normalized, errors, valid: valid && Object.keys(errors).length === 0 }
}

function normalizeJSONWidgets(schema: JSONSchema, value: unknown, path: string[], errors: Record<string, string>) {
  if (!value || typeof value !== 'object' || Array.isArray(value)) return
  const object = value as Record<string, unknown>
  for (const [name, child] of Object.entries(schema.properties ?? {})) {
    const childPath = [...path, name]
    if (child['x-ui-widget'] === 'json' && typeof object[name] === 'string') {
      try { object[name] = JSON.parse(object[name] as string) as unknown } catch { errors[`/${childPath.join('/')}`] = `${child.title ?? name}必须是合法 JSON` }
    } else if (child.type === 'object') normalizeJSONWidgets(child, object[name], childPath, errors)
  }
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
