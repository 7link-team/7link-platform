import { useEffect, useMemo, useRef, useState } from "react"
import { useTranslations } from "use-intl"
import { toast } from "sonner"

import { Button } from "../button"
import { AutoForm } from "../auto-form/auto-form"
import type { FormMode } from "../auto-form/types"
import { CrudEditButton } from "./crud-edit-button"
import type { FormFactory, Row } from "./types"

export interface CrudFormProps {
  factory: FormFactory
  mode: FormMode
  /** Entity id (edit/detail). */
  id?: string
  /** Source id to clone from (add?copyId=). */
  copyId?: string
  /** Called after a successful commit (page navigates back). */
  onSubmitted?: () => void
  /** Detail mode: navigate to the edit page. */
  onEdit?: () => void
  /** Optional success message override. */
  successMessage?: string
  /** When true, don't render the action button (page manages it externally). */
  hideActions?: boolean
  /** HTML id for the <form> element, for external submit buttons via form= attribute. */
  formId?: string
  /** Reports submitting state changes to parent. */
  onSubmittingChange?: (submitting: boolean) => void
}

/**
 * CrudForm is the factory-driven create/edit/detail/copy form — the React equivalent of
 * `CommonForm.vue`. It loads the entity (edit/detail/copy) via the factory, maps it to the
 * form shape, renders AutoForm in the right mode, and commits via the factory on submit.
 */
export function CrudForm({
  factory,
  mode,
  id,
  copyId,
  onSubmitted,
  onEdit,
  successMessage,
  hideActions,
  formId,
  onSubmittingChange,
}: CrudFormProps) {
  const t = useTranslations("crud")
  const fields = useMemo(() => factory.getFormDef(), [factory])

  const isCopy = mode === "copy" || (mode === "add" && !!copyId)
  const loadId = isCopy ? (copyId ?? id) : mode === "add" ? undefined : id
  const missingId = mode !== "add" && !loadId
  const [attempt, setAttempt] = useState(0)
  const [loaded, setLoaded] = useState<{
    factory: FormFactory
    id: string
    attempt: number
    data?: Row
    failed: boolean
  }>()
  const context = useMemo(
    () => ({ factory, loadId, mode, attempt }),
    [factory, loadId, mode, attempt]
  )
  const activeContext = useRef<object | null>(null)
  const [committing, setCommitting] = useState<object | null>(null)
  const submitting = committing === context
  useEffect(() => {
    activeContext.current = context
    return () => {
      activeContext.current = null
    }
  }, [context])
  const current =
    loaded?.factory === factory &&
    loaded.id === loadId &&
    loaded.attempt === attempt
      ? loaded
      : undefined

  useEffect(() => {
    onSubmittingChange?.(submitting)
  }, [submitting, onSubmittingChange])

  useEffect(() => {
    if (!loadId) return
    let cancelled = false
    void Promise.resolve()
      .then(() => factory.getInfo(loadId))
      .then(async (raw) => {
        const data = factory.toLocalData ? await factory.toLocalData(raw) : raw
        if (!cancelled)
          setLoaded({ factory, id: loadId, attempt, data, failed: false })
      })
      .catch(() => {
        if (!cancelled)
          setLoaded({ factory, id: loadId, attempt, failed: true })
      })
    return () => {
      cancelled = true
    }
  }, [factory, loadId, attempt])

  const effectiveMode: FormMode = isCopy ? "copy" : mode

  const handleCommit = async (values: Row) => {
    if (
      activeContext.current !== context ||
      factory.editable === false ||
      mode === "detail" ||
      missingId
    )
      return
    const body = factory.toServerData ? factory.toServerData(values) : values
    setCommitting(context)
    try {
      if (mode === "add" || isCopy) {
        await factory.commitAdd(body)
      } else {
        await factory.commitEdit(id ?? "", body)
      }
      if (activeContext.current !== context) return
      toast.success(successMessage ?? t("success"))
      factory.commitCallback?.onSuccess?.()
      onSubmitted?.()
    } catch (error) {
      if (activeContext.current !== context) return
      factory.commitCallback?.onError?.(error)
      // AutoForm owns inline field/global feedback; transport callbacks may also notify.
      throw error
    } finally {
      setCommitting((current) => (current === context ? null : current))
    }
  }

  if (missingId || current?.failed) {
    return (
      <div role="alert" className="space-y-3 p-6 text-sm">
        <p>{t("loadError")}</p>
        {!missingId && (
          <Button
            type="button"
            variant="outline"
            onClick={() => setAttempt((value) => value + 1)}
          >
            {t("retry")}
          </Button>
        )}
      </div>
    )
  }

  if (loadId && !current) {
    return (
      <div className="p-6 text-sm text-muted-foreground">{t("loading")}</div>
    )
  }

  return (
    <AutoForm
      key={`${effectiveMode}:${loadId ?? ""}`}
      id={formId}
      fields={fields}
      mode={effectiveMode}
      initialData={current?.data}
      readonly={factory.editable === false}
      contract={factory.contract}
      onSubmit={handleCommit}
    >
      {!hideActions && (
        <CrudEditButton
          mode={mode}
          editable={factory.editable}
          submitting={submitting}
          onEdit={onEdit}
        />
      )}
    </AutoForm>
  )
}
