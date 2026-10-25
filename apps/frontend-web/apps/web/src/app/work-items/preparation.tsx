import { useEffect, useMemo, useRef, useState } from "react"
import {
  AutoForm,
  type FieldRendererProps,
} from "@workspace/ui/components/auto-form"
import { Button } from "@workspace/ui/components/button"
import { Input } from "@workspace/ui/components/input"
import { Textarea } from "@workspace/ui/components/textarea"
import { useTranslations } from "@workspace/ui/i18n/intl-provider"
import { Alert } from "../../components/alert"
import { apiRoute } from "../../lib/api-route"
import {
  iamApi,
  type WorkItemDetail,
  type WorkPreparationInput,
} from "../../lib/iam-api"
import { useAllowed } from "../../lib/permissions"
import { useRequestContract } from "../../lib/use-request-contract"

type ContextRef = NonNullable<WorkPreparationInput["context_refs"]>[number]
type VerificationStep = NonNullable<
  WorkPreparationInput["verification_plan"]
>[number]
const selectClass = "h-9 w-full rounded-md border bg-background px-3 text-sm"

function ContextField(props: FieldRendererProps) {
  const t = useTranslations("iam")
  const rows = (Array.isArray(props.value) ? props.value : []) as ContextRef[]
  return (
    <div
      role="group"
      aria-labelledby={props.labelId}
      aria-describedby={props.describedBy}
      className="space-y-2"
    >
      {rows.map((row, index) => (
        <fieldset
          key={index}
          disabled={props.disabled || props.readonly}
          className="space-y-2 rounded-md border p-3"
        >
          <legend className="px-1 text-sm">
            {t("workContextNumber", { number: index + 1 })}
          </legend>
          <label className="grid gap-1 text-sm">
            {t("workContextKind")}
            <select
              className={selectClass}
              value={row.kind}
              onChange={(e) =>
                props.onChange(
                  rows.map((r, i) =>
                    i === index ? { ...r, kind: e.target.value } : r
                  )
                )
              }
            >
              <option value="URL">{t("workContextURL")}</option>
              <option value="WORK_ITEM">{t("workContextWork")}</option>
              <option value="KNOWLEDGE">{t("workContextKnowledge")}</option>
            </select>
          </label>
          <label className="grid gap-1 text-sm">
            {row.kind === "URL" ? t("workContextURL") : row.kind === "KNOWLEDGE" ? t("workContextKnowledgeID") : t("workContextWorkID")}
            <Input
              value={row.ref}
              required
              maxLength={2000}
              aria-invalid={Boolean(props.error)}
              onChange={(e) =>
                props.onChange(
                  rows.map((r, i) =>
                    i === index ? { ...r, ref: e.target.value } : r
                  )
                )
              }
            />
          </label>
          <Button
            type="button"
            size="sm"
            variant="outline"
            onClick={() => props.onChange(rows.filter((_, i) => i !== index))}
          >
            {t("delete")}
          </Button>
        </fieldset>
      ))}
      <Button
        type="button"
        variant="outline"
        disabled={props.disabled || props.readonly || rows.length >= 50}
        onClick={() => props.onChange([...rows, { kind: "URL", ref: "" }])}
      >
        {t("workAddContext")}
      </Button>
      {props.error && (
        <p id={props.errorId} role="alert" className="text-sm text-destructive">
          {props.error}
        </p>
      )}
    </div>
  )
}

function ScopeField(props: FieldRendererProps) {
  const t = useTranslations("iam")
  const rows = (Array.isArray(props.value) ? props.value : []) as string[]
  return (
    <div
      role="group"
      aria-labelledby={props.labelId}
      aria-describedby={props.describedBy}
      className="space-y-2"
    >
      {rows.map((row, index) => (
        <div key={index} className="flex gap-2">
          <Textarea
            aria-label={t("workScopeNumber", { number: index + 1 })}
            value={row}
            disabled={props.disabled || props.readonly}
            required
            maxLength={2000}
            onChange={(e) =>
              props.onChange(
                rows.map((r, i) => (i === index ? e.target.value : r))
              )
            }
          />
          <Button
            type="button"
            variant="outline"
            disabled={props.disabled || props.readonly}
            onClick={() => props.onChange(rows.filter((_, i) => i !== index))}
          >
            {t("delete")}
          </Button>
        </div>
      ))}
      <Button
        type="button"
        variant="outline"
        disabled={props.disabled || props.readonly || rows.length >= 20}
        onClick={() => props.onChange([...rows, ""])}
      >
        {t("workAddScope")}
      </Button>
      {props.error && (
        <p id={props.errorId} role="alert" className="text-sm text-destructive">
          {props.error}
        </p>
      )}
    </div>
  )
}

function BudgetField(props: FieldRendererProps) {
  const t = useTranslations("iam")
  const budget = props.value as WorkPreparationInput["budget"]
  return (
    <div
      role="group"
      aria-labelledby={props.labelId}
      aria-describedby={props.describedBy}
      className="space-y-3"
    >
      <label className="flex items-center gap-2 text-sm">
        <input
          type="checkbox"
          disabled={props.disabled || props.readonly}
          checked={Boolean(budget)}
          onChange={(e) =>
            props.onChange(
              e.target.checked ? { max_internal_rounds: 5 } : undefined
            )
          }
        />
        {t("workBudgetEnable")}
      </label>
      {budget && (
        <fieldset
          disabled={props.disabled || props.readonly}
          className="grid gap-3 sm:grid-cols-2"
        >
          {(
            [
              "tokens",
              "usd_micros",
              "wall_clock_minutes",
              "max_internal_rounds",
            ] as const
          ).map((key) => (
            <label key={key} className="grid gap-1 text-sm">
              {t(`workBudget_${key}`)}
              <Input
                type={
                  key === "tokens" || key === "usd_micros" ? "text" : "number"
                }
                inputMode="numeric"
                pattern={
                  key === "tokens" || key === "usd_micros"
                    ? "[0-9]+"
                    : undefined
                }
                min={1}
                max={key === "max_internal_rounds" ? 32767 : 2147483647}
                required={key === "max_internal_rounds"}
                value={budget[key] ?? ""}
                onChange={(e) => {
                  const text = e.target.value
                  const next = { ...budget }
                  if (key === "tokens" || key === "usd_micros") {
                    if (text === "") delete next[key]
                    else next[key] = text
                  } else if (key === "wall_clock_minutes") {
                    if (text === "") delete next[key]
                    else next[key] = Number(text)
                  } else next[key] = text === "" ? 0 : Number(text)
                  props.onChange(next)
                }}
              />
            </label>
          ))}
        </fieldset>
      )}
      {props.error && (
        <p id={props.errorId} role="alert" className="text-sm text-destructive">
          {props.error}
        </p>
      )}
    </div>
  )
}

export function WorkPreparation({
  entityID,
  detail,
  onUpdated,
}: {
  entityID: string
  detail: WorkItemDetail
  onUpdated: (detail: WorkItemDetail) => void
}) {
  const t = useTranslations("iam")
  const allowed = useAllowed()
  const [busy, setBusy] = useState(false)
  const [error, setError] = useState("")
  const pending = useRef(false)
  const active = useRef(true)
  useEffect(() => {
    active.current = true
    return () => {
      active.current = false
    }
  }, [])
  const attempt = useRef<{ identity: string; key: string } | null>(null)
  const errorSummary = useRef<HTMLDivElement>(null)
  const route = useMemo(
    () =>
      apiRoute("patch", "/entities/{entity_id}/work-items/{work_item_id}", {
        path: { entity_id: entityID, work_item_id: detail.id ?? "" },
      }),
    [entityID, detail.id]
  )
  const metadata = useRequestContract(route)
  const prep = detail.preparation
  const editable = ["DRAFT", "READY", "BLOCKED"].includes(detail.state)
  const canEdit = editable && allowed("work_item_write")
  const VerificationField = useMemo(
    () =>
      function VerificationField(props: FieldRendererProps) {
        const rows = (
          Array.isArray(props.value) ? props.value : []
        ) as VerificationStep[]
        return (
          <div
            role="group"
            aria-labelledby={props.labelId}
            aria-describedby={props.describedBy}
            className="space-y-3"
          >
            {(detail.criteria ?? []).map((criterion, index) => {
              const step = rows.find((row) => row.criterion_id === criterion.id)
              const update = (change: Partial<VerificationStep>) =>
                props.onChange([
                  ...rows.filter((row) => row.criterion_id !== criterion.id),
                  {
                    criterion_id: criterion.id,
                    method: step?.method ?? "HUMAN",
                    detail: step?.detail ?? "",
                    ...change,
                  },
                ])
              return (
                <fieldset
                  key={criterion.id}
                  disabled={props.disabled || props.readonly}
                  className="space-y-2 rounded-md border p-3"
                >
                  <legend className="px-1 text-sm">
                    {t("workClauseNumber", { number: index + 1 })}
                  </legend>
                  <p className="text-sm whitespace-pre-wrap">
                    {criterion.text}
                  </p>
                  <label className="grid gap-1 text-sm">
                    {t("workPrimaryMethod")}
                    <select
                      className={selectClass}
                      value={step?.method ?? ""}
                      onChange={(e) => {
                        if (!e.target.value)
                          props.onChange(
                            rows.filter(
                              (row) => row.criterion_id !== criterion.id
                            )
                          )
                        else
                          update({
                            method: e.target
                              .value as VerificationStep["method"],
                          })
                      }}
                    >
                      <option value="">{t("workMethodUnset")}</option>
                      {[
                        "TEST",
                        "COMMAND",
                        "AGENT_JUDGE",
                        "HUMAN",
                        "VISUAL",
                      ].map((method) => (
                        <option key={method} value={method}>
                          {t(`workMethod_${method}`)}
                        </option>
                      ))}
                    </select>
                  </label>
                  {step && (
                    <label className="grid gap-1 text-sm">
                      {t("workMethodDetail")}
                      <Textarea
                        value={step.detail}
                        required
                        maxLength={2000}
                        onChange={(e) => update({ detail: e.target.value })}
                      />
                    </label>
                  )}
                </fieldset>
              )
            })}
            {props.error && (
              <p
                id={props.errorId}
                role="alert"
                className="text-sm text-destructive"
              >
                {props.error}
              </p>
            )}
          </div>
        )
      },
    [detail.criteria, t]
  )

  const showError = (cause: unknown) => {
    if (!active.current) return
    setError(cause instanceof Error ? cause.message : t("workUnavailable"))
    queueMicrotask(() => errorSummary.current?.focus())
  }
  const submit = async (body?: WorkPreparationInput) => {
    if (
      pending.current ||
      !editable ||
      (body ? !canEdit || !metadata.contract : !allowed("work_item_read"))
    )
      return
    const identity = JSON.stringify({
      version: detail.version,
      body: body ?? null,
    })
    const key =
      attempt.current?.identity === identity
        ? attempt.current.key
        : crypto.randomUUID()
    attempt.current = { identity, key }
    pending.current = true
    setBusy(true)
    setError("")
    try {
      if (body)
        await iamApi.prepareWorkItem(
          entityID,
          detail.id!,
          detail.version,
          body,
          key
        )
      else
        await iamApi.checkWorkReadiness(
          entityID,
          detail.id!,
          detail.version,
          key
        )
      // Keep the same key if reading the committed result fails, too.
      const current = await iamApi.workItem(entityID, detail.id!)
      if (active.current) onUpdated(current)
    } finally {
      pending.current = false
      if (active.current) setBusy(false)
    }
  }
  return (
    <section
      className="space-y-4 border-t pt-4"
      aria-label={t("workPreparation")}
    >
      <div className="flex flex-wrap items-center justify-between gap-2">
        <h3 className="font-medium">{t("workPreparation")}</h3>
        <span className="text-sm">
          {t.has(`workState_${detail.state}`)
            ? t(`workState_${detail.state}`)
            : detail.state}{" "}
          · v{detail.version}
        </span>
      </div>
      <p className="text-sm text-muted-foreground">
        {t("workPreparationHint")}
      </p>
      <div aria-live="polite" className="space-y-2 text-sm">
        <p>{prep.ok ? t("workReadinessPassed") : t("workReadinessPending")}</p>
        {(prep.issues ?? []).length > 0 && (
          <ul className="list-disc space-y-1 pl-5">
            {(prep.issues ?? []).map((issue, i) => (
              <li key={`${issue.code}-${i}`}>
                {t(`workIssue_${issue.code}`)}
                {issue.field ? ` · ${t(`workField_${issue.field}`)}` : ""}
                {issue.criterion_id
                  ? ` · ${t("workClauseNumber", { number: (detail.criteria ?? []).findIndex((c) => c.id === issue.criterion_id) + 1 })}`
                  : ""}
              </li>
            ))}
          </ul>
        )}
        {(prep.block_reasons ?? []).length > 0 && (
          <ul className="list-disc space-y-1 pl-5">
            {(prep.block_reasons ?? []).map((reason) => (
              <li key={reason}>
                {t.has(`workBlock_${reason}`)
                  ? t(`workBlock_${reason}`)
                  : reason}
              </li>
            ))}
          </ul>
        )}
        {prep.last_check && (
          <p className="text-muted-foreground">
            {t("workLastCheck", {
              time: prep.last_check.created_at,
              version: prep.last_check.work_item_version,
            })}
          </p>
        )}
      </div>
      {error && (
        <div ref={errorSummary} tabIndex={-1}>
          <Alert>{error}</Alert>
        </div>
      )}
      {metadata.failed && (
        <Alert>
          {t("projectContractUnavailable")}{" "}
          <Button
            type="button"
            size="sm"
            variant="outline"
            onClick={metadata.retry}
          >
            {t("retryNow")}
          </Button>
        </Alert>
      )}
      <AutoForm
        key={`${detail.id}:${detail.version}`}
        id="work-preparation-form"
        mode="edit"
        readonly={!canEdit}
        contract={metadata.contract}
        initialData={{
		  confirm_contract: false,
          estimate_minutes: prep.estimate_minutes,
          risk: prep.risk,
          autonomy_level: prep.autonomy_level,
          out_of_scope: prep.out_of_scope,
          out_of_scope_confirmed: prep.out_of_scope_confirmed,
          context_refs: prep.context_refs,
          verification_plan: prep.verification_plan,
          budget: prep.budget,
        }}
        fields={{
		  confirm_contract: {
			name: t("workConfirmDraftContract"),
			type: "checkbox",
			disabled: busy || Boolean(detail.contract),
		  },
          estimate_minutes: {
            name: t("workEstimate"),
            type: "custom",
            component: (props: FieldRendererProps) => (
              <div>
                <Input
                  type="number"
                  min={15}
                  max={4800}
                  step={1}
                  value={typeof props.value === "number" ? props.value : ""}
                  disabled={props.disabled || props.readonly}
                  aria-labelledby={props.labelId}
                  aria-describedby={props.describedBy}
                  aria-invalid={Boolean(props.error)}
                  onChange={(event) =>
                    props.onChange(
                      event.target.value === ""
                        ? undefined
                        : Number(event.target.value)
                    )
                  }
                />
                {props.error && (
                  <p
                    id={props.errorId}
                    role="alert"
                    className="text-sm text-destructive"
                  >
                    {props.error}
                  </p>
                )}
              </div>
            ),
            disabled: busy,
          },
          risk: {
            name: t("workRisk"),
            type: "select",
            disabled: busy,
            config: {
              options: ["LOW", "MEDIUM", "HIGH"].map((value) => ({
                value,
                label: t(`workLevel_${value}`),
              })),
            },
          },
          autonomy_level: {
            name: t("workAutonomy"),
            type: "select",
            disabled: busy,
            config: {
              options: ["LOW", "MEDIUM", "HIGH"].map((value) => ({
                value,
                label: t(`workLevel_${value}`),
              })),
            },
          },
          out_of_scope: {
            name: t("workOutOfScope"),
            type: "custom",
            component: ScopeField,
            disabled: busy,
          },
          out_of_scope_confirmed: {
            name: t("workScopeConfirmed"),
            type: "checkbox",
            disabled: busy,
          },
          context_refs: {
            name: t("workContexts"),
            type: "custom",
            component: ContextField,
            disabled: busy,
          },
          verification_plan: {
            name: t("workVerificationPlan"),
            type: "custom",
            component: VerificationField,
            disabled: busy,
          },
          budget: {
            name: t("workBudget"),
            type: "custom",
            component: BudgetField,
            disabled: busy,
          },
        }}
        onSubmit={(values) => submit(values as unknown as WorkPreparationInput)}
        onError={showError}
      />
      <div className="flex flex-wrap gap-2">
        {canEdit && (
          <Button
            type="submit"
            form="work-preparation-form"
            disabled={busy || !metadata.contract}
          >
            {busy ? t("saving") : t("workSavePreparation")}
          </Button>
        )}
        {editable && allowed("work_item_read") && (
          <Button
            type="button"
            variant="outline"
            disabled={busy}
            onClick={() => {
              void submit().catch(showError)
            }}
          >
            {t("workRecheck")}
          </Button>
        )}
        <Button
          type="button"
          variant="outline"
          disabled={busy}
          onClick={() => {
            void iamApi
              .workItem(entityID, detail.id!)
              .then(onUpdated, showError)
          }}
        >
          {t("refresh")}
        </Button>
      </div>
    </section>
  )
}
