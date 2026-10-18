import { useCallback, useEffect, useMemo, useRef, useState } from "react"
import { useTranslations } from "use-intl"
import {
  normalizeValidationIssues,
  validationFormErrors,
  type ValidationIssue,
} from "../../lib/request-validation"
import { compileRequestContract } from "./contract"

import {
  evaluate,
  isRequired,
  isVisible,
  type FormConfig,
  type FormMode,
  type UseAutoFormOptions,
  type UseAutoFormReturn,
} from "./types"

function isEmpty(value: unknown): boolean {
  if (value === undefined || value === null) return true
  if (typeof value === "string") return value.trim() === ""
  if (Array.isArray(value)) return value.length === 0
  return false
}

function buildInitialValues(
  fields: FormConfig,
  initialData?: Record<string, unknown>,
  mode: FormMode = "add"
): Record<string, unknown> {
  // add mode: ignore initialData, use each field's default value (blank new record)
  const usePrefill = mode !== "add" && initialData != null
  const values: Record<string, unknown> = {}
  for (const [key, field] of Object.entries(fields)) {
    if (field.type === "title") continue
    // copy mode: fields marked copy:false (e.g. a unique id) reset to their default value
    if (mode === "copy" && field.copy === false) {
      values[key] = field.value
      continue
    }
    values[key] = usePrefill
      ? initialData[key] !== undefined
        ? initialData[key]
        : field.value
      : field.value
  }
  return values
}

export function useAutoForm(options: UseAutoFormOptions): UseAutoFormReturn {
  const {
    fields: configuredFields,
    contract,
    schema,
    initialData,
    mode = "add",
    readonly = false,
    onSubmit,
    onError,
  } = options
  const t = useTranslations("form")
  const compiledContract = useMemo(
    () =>
      contract
        ? compileRequestContract(contract, (key, params) =>
            t.has(`contract.${key}`)
              ? t(`contract.${key}`, params)
              : t("invalid")
          )
        : undefined,
    [contract, t]
  )
  const fields = useMemo(() => {
    if (!compiledContract) return configuredFields
    return Object.fromEntries(
      Object.entries(configuredFields).map(([key, field]) => [
        key,
        {
          ...field,
          // PATCH may omit a property, while a rendered string value still
          // cannot be cleared. Derive the indicator from the same schema;
          // validateOne keeps presence and non-empty validation separate.
          required:
            compiledContract.requiredFields.has(key) ||
            field.required ||
            ((values: Record<string, unknown>) =>
              typeof values[key] === "string" &&
              compiledContract.fields[key] !== undefined &&
              !compiledContract.fields[key]!.safeParse("").success),
          config: { ...field.config, ...compiledContract.inputLimits[key] },
        },
      ])
    ) as FormConfig
  }, [configuredFields, compiledContract])
  const effectiveReadonly = readonly || mode === "detail"
  const readonlyRef = useRef(effectiveReadonly)
  const submittingRef = useRef(false)
  const valueRevision = useRef(0)
  const serverErrors = useRef<Record<string, string>>({})
  const serverIssues = useRef<ValidationIssue[]>([])
  const mounted = useRef(true)
  useEffect(() => {
    mounted.current = true
    return () => {
      mounted.current = false
    }
  }, [])
  const contractRef = useRef(contract)
  const contractGenerationRef = useRef(0)
  useEffect(() => {
    contractRef.current = contract
    contractGenerationRef.current++
  }, [contract])
  useEffect(() => {
    readonlyRef.current = effectiveReadonly
  }, [effectiveReadonly])

  const initialValues = useMemo(
    () => buildInitialValues(fields, initialData, mode),
    [fields, initialData, mode]
  )

  const [values, setValuesState] =
    useState<Record<string, unknown>>(initialValues)
  const [errors, setErrors] = useState<Record<string, string | undefined>>({})
  const [touched, setTouched] = useState<Record<string, boolean>>({})
  const [isSubmitting, setIsSubmitting] = useState(false)

  const originalDataRef = useRef<Record<string, unknown> | null>(null)

  useEffect(() => {
    originalDataRef.current = initialData ? { ...initialData } : null
  }, [initialData])

  const setValue = useCallback(
    (name: string, value: unknown) => {
      if (effectiveReadonly || readonlyRef.current) return
      valueRevision.current++
      delete serverErrors.current[name]
      setValuesState((prev) => ({ ...prev, [name]: value }))
      setErrors((prev) => ({ ...prev, [name]: undefined }))
      setTouched((prev) => ({ ...prev, [name]: true }))
    },
    [effectiveReadonly]
  )

  const setValues = useCallback(
    (data: Record<string, unknown>, options?: { isCopy?: boolean }) => {
      const next: Record<string, unknown> = {}
      for (const [key, field] of Object.entries(fields)) {
        if (field.type === "title") continue
        if (options?.isCopy && field.copy === false) {
          next[key] = field.value
          continue
        }
        next[key] = data[key] !== undefined ? data[key] : field.value
      }
      valueRevision.current++
      serverErrors.current = {}
      serverIssues.current = []
      setValuesState(next)
      setErrors({})
      setTouched({})
      originalDataRef.current = data ? { ...data } : null
    },
    [fields]
  )

  const reset = useCallback(() => {
    valueRevision.current++
    serverErrors.current = {}
    serverIssues.current = []
    setValuesState(initialValues)
    setErrors({})
    setTouched({})
    originalDataRef.current = initialData ? { ...initialData } : null
  }, [initialValues, initialData])

  // Validate a single visible field, returning an error string (supports async rules/validate)
  const validateOne = useCallback(
    async (
      key: string,
      vals: Record<string, unknown>
    ): Promise<string | undefined> => {
      const field = fields[key]
      if (!field || field.type === "title") return undefined
      if (!isVisible(field, vals)) return undefined

      const value = vals[key]
      // Schema required means property presence, not a legacy non-empty rule.
      // Keep the merged required flag for the UI indicator; the schema owns
      // remote validation, including nullable and empty values.
      if (isRequired(configuredFields[key]!, vals) && isEmpty(value)) {
        const configuredMessage = field.config?.requiredMessage
        return configuredMessage
          ? evaluate(configuredMessage, vals)
          : t("required")
      }

      const contractField = compiledContract?.fields[key]
      if (contractField) {
        const checked = contractField.safeParse(value)
        if (!checked.success)
          return checked.error.issues[0]?.message ?? t("invalid")
      }
      const config = field.config ?? {}
      // Explicit legacy input constraints survive migration to config-driven fields.
      // Contract-backed fields continue to use the server schema's semantics.
      if (
        !contractField &&
        value !== undefined &&
        value !== null &&
        value !== ""
      ) {
        const text = String(value)
        if (config.minLength !== undefined && text.length < config.minLength)
          return t("invalid")
        if (config.maxLength !== undefined && text.length > config.maxLength)
          return t("invalid")
        if (config.pattern) {
          try {
            if (!new RegExp(`^(?:${config.pattern})$`, "v").test(text))
              return t("invalid")
          } catch {
            // Invalid HTML patterns have no browser constraint either.
          }
        }
        if (field.type === "email" || config.type === "email") {
          if (
            !/^[a-zA-Z0-9.!#$%&'*+/=?^_`{|}~-]+@[a-zA-Z0-9](?:[a-zA-Z0-9-]*[a-zA-Z0-9])?(?:\.[a-zA-Z0-9](?:[a-zA-Z0-9-]*[a-zA-Z0-9])?)*$/.test(
              text
            )
          )
            return t("invalid")
        }
        if (field.type === "number" || config.type === "number") {
          const number = Number(value)
          if (!Number.isFinite(number)) return t("invalid")
          if (config.min !== undefined && number < Number(config.min))
            return t("invalid")
          if (config.max !== undefined && number > Number(config.max))
            return t("invalid")
          const step = config.step ?? 1
          const base = Number(
            config.min ?? originalDataRef.current?.[key] ?? field.value ?? 0
          )
          const steps = (number - base) / step
          if (step > 0 && Math.abs(steps - Math.round(steps)) > 1e-8)
            return t("invalid")
        }
        if (config.type === "url") {
          try {
            new URL(text)
          } catch {
            return t("invalid")
          }
        }
        if (["date", "datetime-local", "time"].includes(String(config.type))) {
          if (config.min !== undefined && text < String(config.min))
            return t("invalid")
          if (config.max !== undefined && text > String(config.max))
            return t("invalid")
        }
      }
      if (config.rules) {
        for (const rule of config.rules) {
          const message = await rule(value, vals)
          if (message) return message
        }
      }
      if (config.validate) {
        const message = await config.validate(
          value,
          originalDataRef.current?.[key],
          vals
        )
        if (message) return message
      }
      return undefined
    },
    [fields, configuredFields, t, compiledContract]
  )

  const validate = useCallback(async (): Promise<boolean> => {
    if (effectiveReadonly || readonlyRef.current) return true
    const contractGeneration = contractGenerationRef.current
    serverErrors.current = {}
    serverIssues.current = []
    const nextErrors: Record<string, string | undefined> = {}

    const entries = Object.keys(fields)
    const messages = await Promise.all(
      entries.map((key) => validateOne(key, values))
    )
    entries.forEach((key, i) => {
      if (messages[i]) nextErrors[key] = messages[i]
    })

    if (contract === null) nextErrors._form = t("contract.loading")
    if (compiledContract) {
      const payload = Object.fromEntries(
        Object.entries(values).filter(
          ([key]) =>
            fields[key] &&
            fields[key]?.type !== "title" &&
            fields[key]?.submit !== false &&
            isVisible(fields[key], values)
        )
      )
      const checked = compiledContract.schema.safeParse(payload)
      if (!checked.success) {
        for (const issue of checked.error.issues) {
          const key = String(issue.path[0] ?? "_form")
          if (!nextErrors[key]) nextErrors[key] = issue.message
        }
      }
    }
    if (schema) {
      const result = schema.safeParse(values)
      if (!result.success) {
        const flattened = (
          result.error as {
            flatten?: () => {
              fieldErrors: Record<string, string[] | undefined>
            }
          }
        ).flatten?.()
        if (flattened) {
          for (const [key, messages] of Object.entries(flattened.fieldErrors)) {
            if (messages?.length && !nextErrors[key]) {
              nextErrors[key] = messages[0]
            }
          }
        }
      }
    }

    if (readonlyRef.current) return true
    if (
      contractRef.current !== contract ||
      contractGeneration !== contractGenerationRef.current
    )
      return false
    setErrors(nextErrors)
    return Object.values(nextErrors).every((e) => !e)
  }, [
    fields,
    schema,
    values,
    validateOne,
    effectiveReadonly,
    contract,
    compiledContract,
    t,
  ])

  // Real-time single-field validation on blur
  const validateField = useCallback(
    async (name: string): Promise<void> => {
      if (effectiveReadonly || readonlyRef.current) return
      const contractGeneration = contractGenerationRef.current
      const message = await validateOne(name, values)
      if (
        readonlyRef.current ||
        contractRef.current !== contract ||
        contractGeneration !== contractGenerationRef.current
      )
        return
      setErrors((prev) => ({
        ...prev,
        [name]: message ?? serverErrors.current[name],
      }))
    },
    [validateOne, values, effectiveReadonly, contract]
  )

  const previousMessages = useRef({ contract, t })
  const messageGeneration = useRef(0)
  useEffect(() => {
    // Wait for the new language's contract; never reset user input while loading.
    if (contract === null || effectiveReadonly) return
    if (
      previousMessages.current.contract === contract &&
      previousMessages.current.t === t
    )
      return
    previousMessages.current = { contract, t }
    const generation = ++messageGeneration.current
    const revision = valueRevision.current
    const visible = new Set(
      Object.keys(fields).filter(
        (name) =>
          fields[name]?.type !== "title" && isVisible(fields[name]!, values)
      )
    )
    const translated = validationFormErrors(
      serverIssues.current,
      visible,
      contract?.issue_messages
    )
    const currentServer = Object.keys(serverErrors.current)
    for (const name of currentServer) {
      serverErrors.current[name] =
        translated[name] ??
        (name === "_form" && !serverIssues.current.length
          ? t("submitFailed")
          : serverErrors.current[name]!)
    }
    const clientFields = Object.keys(errors).filter(
      (name) =>
        errors[name] && name !== "_form" && !currentServer.includes(name)
    )
    void Promise.all(
      clientFields.map(
        async (name) => [name, await validateOne(name, values)] as const
      )
    ).then(
      (checked) => {
        if (
          !mounted.current ||
          readonlyRef.current ||
          revision !== valueRevision.current ||
          generation !== messageGeneration.current ||
          contractRef.current !== contract
        )
          return
        setErrors((previous) => ({
          ...previous,
          ...Object.fromEntries(checked),
          ...serverErrors.current,
        }))
      },
      () => {
        // Custom async validators can fail. Preserve the existing visible error;
        // language refresh must neither submit nor discard that feedback.
      }
    )
  }, [contract, t, effectiveReadonly, fields, values, errors, validateOne])

  const getValues = useCallback((): Record<string, unknown> => {
    const data: Record<string, unknown> = {}
    for (const [key, field] of Object.entries(fields)) {
      if (field.type === "title") continue
      if (!isVisible(field, values)) continue
      if (field.submit === false) continue
      data[key] = values[key]
    }
    return data
  }, [fields, values])

  const handleSubmit = useCallback(
    async (e?: React.FormEvent) => {
      e?.preventDefault()
      if (
        effectiveReadonly ||
        readonlyRef.current ||
        submittingRef.current ||
        contractRef.current !== contract ||
        !onSubmit
      )
        return
      const contractGeneration = contractGenerationRef.current
      const revision = valueRevision.current
      // Acquire before async validation: repeated submit events must not race.
      submittingRef.current = true
      setIsSubmitting(true)
      try {
        const ok = await validate()
        if (
          !ok ||
          readonlyRef.current ||
          contractRef.current !== contract ||
          contractGeneration !== contractGenerationRef.current
        )
          return
        const payload = getValues()
        const parsed = compiledContract?.schema.safeParse(payload)
        if (parsed && !parsed.success) return
        await onSubmit(
          parsed ? (parsed.data as Record<string, unknown>) : payload
        )
      } catch (err) {
        if (
          !mounted.current ||
          readonlyRef.current ||
          revision !== valueRevision.current ||
          contractGeneration !== contractGenerationRef.current
        )
          return
        const issues = normalizeValidationIssues({
          validation_errors:
            typeof err === "object" && err !== null && "validationErrors" in err
              ? err.validationErrors
              : undefined,
        })
        const visible = new Set(
          Object.keys(fields).filter(
            (name) =>
              fields[name]?.type !== "title" && isVisible(fields[name]!, values)
          )
        )
        serverIssues.current = issues
        const mapped = validationFormErrors(
          issues,
          visible,
          contract?.issue_messages
        )
        serverErrors.current = Object.keys(mapped).length
          ? mapped
          : { _form: t("submitFailed") }
        setErrors({ ...serverErrors.current })
        onError?.(err)
      } finally {
        submittingRef.current = false
        setIsSubmitting(false)
      }
    },
    [
      getValues,
      onSubmit,
      onError,
      validate,
      effectiveReadonly,
      compiledContract,
      contract,
      fields,
      values,
      t,
    ]
  )

  return {
    fields,
    values,
    errors,
    touched,
    setValue,
    setValues,
    reset,
    validate,
    validateField,
    getValues,
    handleSubmit,
    isSubmitting,
  }
}
