import type { ErrorObject, ValidateFunction } from 'ajv'
import Ajv2020 from 'ajv/dist/2020.js'
import { useEffect, useMemo, useRef, useState, type FormEvent } from 'react'

import { Field } from './Field'
import { pointerChild, type FormValue, type JSONSchema } from './types'

const ajv = new Ajv2020({ allErrors: true, strict: false, useDefaults: true })
const validators = new WeakMap<object, ValidateFunction>()

export interface FormValidation {
  normalized: FormValue
  errors: Record<string, string>
  valid: boolean
}

export interface SchemaFormSecondarySubmit {
  label: string
  onSubmit: (value: FormValue) => void | Promise<void>
  disabled?: boolean
}

export interface SchemaFormResetAction {
  label: string
  onReset: () => void
  disabled?: boolean
}

interface SchemaFormProps {
  schema: JSONSchema
  value: FormValue
  onChange: (value: FormValue) => void
  onSubmit: (value: FormValue) => void | Promise<void>
  submitLabel: string
  secondarySubmit?: SchemaFormSecondarySubmit
  resetAction?: SchemaFormResetAction
  disabled?: boolean
  groupOptional?: boolean
  onValidationChange?: (validation: FormValidation) => void
  editablePaths?: ReadonlySet<string>
  requiredPaths?: ReadonlySet<string>
}

export function SchemaForm({ schema, value, onChange, onSubmit, submitLabel, secondarySubmit, resetAction, disabled, groupOptional, onValidationChange, editablePaths, requiredPaths }: SchemaFormProps) {
  const [draft, setDraft] = useState<FormValue>(value)
  const [errors, setErrors] = useState<Record<string, string>>({})
  const focusFrame = useRef<number | undefined>(undefined)
  useEffect(() => setDraft(value), [value])
  useEffect(() => () => {
    if (focusFrame.current !== undefined) window.cancelAnimationFrame(focusFrame.current)
  }, [])
  const properties = schema.properties ?? {}
  const order = [...(schema['x-ui-order'] ?? []), ...Object.keys(properties).filter((key) => !schema['x-ui-order']?.includes(key))]
  const validation = useMemo(() => validateFormValue(schema, draft, requiredPaths), [draft, schema, requiredPaths])
  useEffect(() => onValidationChange?.(validation), [onValidationChange, validation])

  const update = (name: string, nextValue: unknown) => {
    const next = { ...draft, [name]: nextValue }
    setDraft(next)
    setErrors((current) => {
      const nextErrors = { ...current }
      delete nextErrors[pointerChild('', name)]
      return nextErrors
    })
    onChange(next)
  }

  const submit = async (event: FormEvent<HTMLFormElement>) => {
    event.preventDefault()
    if (!validation.valid) {
      setErrors(validation.errors)
      const first = Object.keys(validation.errors)[0]
      if (focusFrame.current !== undefined) window.cancelAnimationFrame(focusFrame.current)
      focusFrame.current = window.requestAnimationFrame(() => {
        focusFrame.current = undefined
        document.getElementById(fieldID(first))?.focus()
      })
      return
    }
    const submitter = (event.nativeEvent as SubmitEvent).submitter as HTMLButtonElement | null
    const handler = submitter?.dataset.intent === 'secondary' ? secondarySubmit?.onSubmit : onSubmit
    setDraft(validation.normalized)
    setErrors({})
    onChange(validation.normalized)
    await handler?.(validation.normalized)
  }

  const renderField = (name: string) => {
    const fieldSchema = properties[name]
    if (!fieldSchema) return null
    return <Field key={name} path={pointerChild('', name)} name={name} schema={fieldSchema} value={draft[name]} required={schema.required?.includes(name) ?? false} errors={errors}
      isPathEditable={editablePaths ? (path) => editablePaths.has(path) : undefined} requiredPaths={requiredPaths} lockArrayShape={editablePaths !== undefined}
      autoFocusPath={editablePaths?.values().next().value} onChange={(next) => update(name, next)} onBlur={(path) => setErrors((current) => {
      const nextErrors = { ...current }
      if (validation.errors[path]) nextErrors[path] = validation.errors[path]
      else delete nextErrors[path]
      return nextErrors
    })} />
  }
  const required = order.filter((name) => schema.required?.includes(name))
  const optional = order.filter((name) => !schema.required?.includes(name))
  const [optionalOpen, setOptionalOpen] = useState(() => optional.some((name) => !isEmptyFormValue(value[name])))
  useEffect(() => {
    if (Object.keys(errors).some((path) => optional.some((name) => path === `/${name}` || path.startsWith(`/${name}/`)))) setOptionalOpen(true)
  }, [errors, optional])
  const firstError = Object.keys(errors)[0]
  const focusFirstError = () => document.getElementById(fieldID(firstError))?.focus()

  return (
    <form className="schema-form" noValidate onSubmit={submit}>
      {firstError && <button className="form-error-summary" type="button" onClick={focusFirstError}>{Object.keys(errors).length} 项需要处理</button>}
      {groupOptional ? <>
        {required.length > 0 && <section className="schema-section"><h3>必要配置</h3>{required.map(renderField)}</section>}
        {optional.length > 0 && <details className="schema-optional" open={optionalOpen} onToggle={(event) => setOptionalOpen(event.currentTarget.open)}><summary>可选配置</summary>{optional.map(renderField)}</details>}
      </> : order.map(renderField)}
      <div className="schema-form-actions">
        {resetAction && <button type="button" disabled={resetAction.disabled} onClick={resetAction.onReset}>{resetAction.label}</button>}
        <button className="primary-button" type="submit" disabled={disabled}>{submitLabel}</button>
        {secondarySubmit && <button type="submit" data-intent="secondary" disabled={disabled || secondarySubmit.disabled}>{secondarySubmit.label}</button>}
      </div>
    </form>
  )
}

export function isEmptyFormValue(value: unknown): boolean {
  if (value === undefined || value === null || value === '') return true
  if (Array.isArray(value)) return value.length === 0
  if (typeof value === 'object') return Object.keys(value).length === 0
  return false
}

function fieldID(path: string) {
  return `field-${path.slice(1).replace(/[^a-zA-Z0-9_-]/g, '-')}`
}

export function validateFormValue(schema: JSONSchema, value: FormValue, requiredPaths?: ReadonlySet<string>): FormValidation {
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
  for (const path of requiredPaths ?? []) {
    if (isPathValueEmpty(pointerValue(normalized, path))) {
      const tokens = decodePointer(path)
      const title = schemaAtPath(schema, tokens)?.title ?? tokens.at(-1) ?? ''
      if (!errors[path]) errors[path] = `${title}为必填项`
    }
  }
  return { normalized, errors, valid: valid && Object.keys(errors).length === 0 }
}

function normalizeJSONWidgets(schema: JSONSchema, value: unknown, path: string[], errors: Record<string, string>) {
  if (!value || typeof value !== 'object' || Array.isArray(value)) return
  const object = value as Record<string, unknown>
  for (const [name, child] of Object.entries(schema.properties ?? {})) {
    const childPath = [...path, name]
    if (child['x-ui-widget'] === 'json' && typeof object[name] === 'string') {
      try { object[name] = JSON.parse(object[name] as string) as unknown } catch { errors[childPath.reduce(pointerChild, '')] = `${child.title ?? name}必须是合法 JSON` }
    } else if (child.type === 'object') normalizeJSONWidgets(child, object[name], childPath, errors)
  }
}

function mapErrors(errors: ErrorObject[], rootSchema: JSONSchema) {
  const mapped: Record<string, string> = {}
  for (const error of errors) {
    const requiredName = error.keyword === 'required' ? String(error.params.missingProperty) : undefined
    const tokens = decodePointer(error.instancePath)
    if (requiredName !== undefined) tokens.push(requiredName)
    const path = tokens.length > 0 ? tokens.reduce(pointerChild, '') : '/'
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

function pointerValue(value: unknown, pointer: string): unknown {
  let current = value
  for (const token of decodePointer(pointer)) {
    if (Array.isArray(current)) {
      if (!/^0$|^[1-9]\d*$/.test(token)) return undefined
      current = current[Number(token)]
    } else if (current && typeof current === 'object') current = (current as Record<string, unknown>)[token]
    else return undefined
  }
  return current
}

function isPathValueEmpty(value: unknown): boolean {
  return value === undefined || value === null || value === '' || value === '[REDACTED]'
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
