import { useEffect, useRef, useState } from "react"
import { AutoForm } from "@workspace/ui/components/auto-form"
import { AutoTable } from "@workspace/ui/components/auto-table"
import { Button } from "@workspace/ui/components/button"
import { useTranslations } from "@workspace/ui/i18n/intl-provider"
import { Alert } from "../../components/alert"
import {
  iamApi,
  type RequirementDetail,
  type WorkItemDetail,
} from "../../lib/iam-api"
import { useAllowed } from "../../lib/permissions"
import { type WorkResult } from "./results-api"

import {
  recoveryActions,
  type RecoveryWork,
  type Action,
} from "./recovery-policy"

export function WorkRecovery({
  entityID,
  detail,
  result,
  subject,
  onUpdated,
}: {
  entityID: string
  detail: RecoveryWork
  result?: WorkResult
  subject?: string
  onUpdated: (work: WorkItemDetail) => void
}) {
  const t = useTranslations("iam")
  const allowed = useAllowed()
  const actions = recoveryActions(detail, allowed, result, subject).filter(
    (a) =>
      result
        ? ["scope", "VERIFIED", "CLOSED"].includes(a)
        : ["resume", "baseline"].includes(a)
  )
  if (
    !result &&
    !detail.is_container &&
    ["BUG", "INCIDENT"].includes(detail.type) &&
    ["ACCEPTED", "CANCELLED"].includes(detail.state) &&
    allowed("work_item_read") &&
    allowed("work_item_write")
  )
    actions.push("reopen")
  const [action, setAction] = useState<Action | null>(null)
  const [requirement, setRequirement] = useState<RequirementDetail | null>(null)
  const [selected, setSelected] = useState<string[]>([])
  const [loading, setLoading] = useState(false)
  const [busy, setBusy] = useState(false)
  const [error, setError] = useState("")
  const [notice, setNotice] = useState("")
  const pending = useRef(false)
  const mounted = useRef(true)
  const attempt = useRef<{ identity: string; key: string } | null>(null)
  useEffect(() => {
    mounted.current = true
    return () => {
      mounted.current = false
    }
  }, [])
  useEffect(() => {
    if (action !== "baseline" || !detail.requirement_id) return
    const c = new AbortController()
    queueMicrotask(() => {
      if (!c.signal.aborted) {
        setLoading(true)
        setRequirement(null)
        setSelected([])
        setError("")
      }
    })
    void iamApi
      .requirement(entityID, detail.requirement_id, c.signal)
      .then(
        (value) => {
          if (!c.signal.aborted) setRequirement(value)
        },
        (cause) => {
          if (!c.signal.aborted)
            setError(
              cause instanceof Error ? cause.message : t("workUnavailable")
            )
        }
      )
      .finally(() => {
        if (!c.signal.aborted) setLoading(false)
      })
    return () => c.abort()
  }, [action, detail.requirement_id, detail.version, entityID, t])
  const visibleStage =
    allowed("work_item_read") &&
    result?.id === detail.current_claim_id &&
    ["BUG", "INCIDENT"].includes(detail.type) &&
    ["VERIFIED", "CLOSED"].includes(detail.defect_stage ?? "")
      ? detail.defect_stage
      : null
  if (!actions.length && !action && !visibleStage && !notice) return null
  const valid = !!action && actions.includes(action)
  const ready =
    valid &&
    (action !== "baseline" || (!loading && requirement?.status === "BASELINED"))
  return (
    <section
      className="grid gap-3 border-t pt-4"
      aria-label={t("workRecovery")}
    >
      <h2 className="font-medium">{t("workRecovery")}</h2>
      {visibleStage && (
        <p role="status">{t(`workRecoveryStatus_${visibleStage}`)}</p>
      )}
      {error && <Alert>{error}</Alert>}
      {notice && <p role="status">{notice}</p>}
      {!action && (
        <div className="flex flex-wrap gap-2">
          {actions.map((a) => (
            <Button
              key={a}
              variant="outline"
              onClick={() => {
                setAction(a)
                setError("")
                setNotice("")
              }}
            >
              {t(`workRecovery_${a}`)}
            </Button>
          ))}
        </div>
      )}
      {action && (
        <>
          <h3 className="font-medium">{t(`workRecovery_${action}`)}</h3>
          <p className="text-sm text-muted-foreground">
            {t(`workRecoveryHint_${action}`)}
          </p>
          {result && (
            <p className="text-sm break-all">
              {t("resultID")}: {result.id}
            </p>
          )}
          {!valid && <Alert>{t("workRecoveryChanged")}</Alert>}
          {action === "baseline" && (
            <>
              {loading && <p role="status">{t("loading")}</p>}
              {requirement && (
                <>
                  <p>
                    {requirement.title} · {t("workRecoveryVersion")}:{" "}
                    {requirement.current_version_no}
                  </p>
                  {requirement.status !== "BASELINED" && (
                    <Alert>{t("workRecoveryNotBaselined")}</Alert>
                  )}
                  <AutoTable
                    data={(requirement.criteria ?? []).filter((c) => c.id)}
                    rowKey="id"
                    pagination={false}
                    exportable={false}
                    columns={[
                      { field: "code", name: t("workRecoveryCriterion") },
                      { field: "text", name: t("workCriterionText") },
                      {
                        field: "id",
                        name: t("workRecoverySelect"),
                        format: (_, row) => (
                          <label className="flex items-center gap-2">
                            <input
                              type="checkbox"
                              disabled={busy || !ready}
                              checked={selected.includes(row.id!)}
                              onChange={(e) =>
                                setSelected((old) =>
                                  e.target.checked
                                    ? [...old, row.id!]
                                    : old.filter((id) => id !== row.id)
                                )
                              }
                            />
                            <span>{row.code}</span>
                          </label>
                        ),
                      },
                    ]}
                  />
                  <p className="text-sm text-muted-foreground">
                    {t("workRecoverySelection", { count: selected.length })}
                  </p>
                </>
              )}
            </>
          )}
          <AutoForm
            key={`${action}:${detail.version}:${requirement?.current_version_no ?? ""}:${result?.id ?? ""}`}
            mode="edit"
            initialData={{
              reason: "",
              feedback: "",
              version: "",
            }}
            fields={{
              reason: {
                name: t("resultReason"),
                type: "textarea",
                show: action !== "baseline",
                required: action !== "baseline",
                disabled: busy || !ready,
                config: { minLength: 5, maxLength: 2000 },
              },
              feedback: {
                name: t("workRecoveryFeedback"),
                type: "textarea",
                show: action === "scope",
                required: action === "scope",
                disabled: busy || !ready,
                config: { minLength: 5, maxLength: 2000 },
              },
              version: {
                name: t("workRecoveryVersionConfirm"),
                type: "text",
                show: action === "baseline",
                required: action === "baseline",
                disabled: busy || !ready,
              },
            }}
            onSubmit={async (values) => {
              if (pending.current || !ready) return
              if (
                action === "baseline" &&
                String(values.version).trim() !==
                  String(requirement?.current_version_no)
              ) {
                setError(t("workRecoveryVersionMismatch"))
                return
              }
              const reason = String(values.reason).trim(),
                feedback = String(values.feedback).trim()
              if (
                action !== "baseline" &&
                ([...reason].length < 5 ||
                  (action === "scope" && [...feedback].length < 5))
              ) {
                setError(t("workRecoveryReasonRequired"))
                return
              }
              const base = `/entities/${encodeURIComponent(entityID)}/work-items/${encodeURIComponent(detail.id!)}`
              const suffix =
                action === "baseline"
                  ? "/baseline"
                  : action === "resume"
                    ? "/resume"
                    : action === "scope"
                      ? `/results/${encodeURIComponent(result!.id)}/request-changes`
                      : action === "reopen"
                        ? "/defect-reopens"
                        : "/defect-stage"
              const body =
                action === "baseline"
                  ? {
                      requirement_version_no: requirement!.current_version_no,
                      criterion_ids: selected,
                    }
                  : action === "scope"
                    ? {
                        failure_category: "SCOPE_CHANGE",
                        reason,
                        feedback: [
                          {
                            kind: "FEEDBACK",
                            category: "SCOPE_CHANGE",
                            detail: feedback,
                          },
                        ],
                      }
                    : action === "resume" || action === "reopen"
                      ? { reason }
                      : { result_id: result!.id, stage: action, reason }
              const identity = JSON.stringify({
                url: base + suffix,
                version: detail.version,
                body,
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
                const receipt = await iamApi.deliveryResultRequest<{
                  id?: string
                }>(
                  {
                    method: action === "baseline" ? "PUT" : "POST",
                    url: base + suffix,
                  },
                  {
                    headers: {
                      "If-Match": `"${detail.version}"`,
                      "Idempotency-Key": key,
                    },
                    body: JSON.stringify(body),
                  }
                )
                const current = await iamApi.workItem(
                  entityID,
                  action === "reopen" ? receipt.id! : detail.id!
                )
                attempt.current = null
                if (mounted.current) {
                  setAction(null)
                  setNotice(t("resultSaved"))
                  onUpdated(current)
                }
              } catch (cause) {
                if (mounted.current)
                  setError(
                    cause instanceof Error
                      ? cause.message
                      : t("workUnavailable")
                  )
              } finally {
                pending.current = false
                if (mounted.current) setBusy(false)
              }
            }}
          >
            <div className="flex gap-2">
              <Button
                type="button"
                variant="outline"
                disabled={busy}
                onClick={() => setAction(null)}
              >
                {t("cancel")}
              </Button>
              <Button type="submit" disabled={busy || !ready}>
                {busy ? t("loading") : t(`workRecovery_${action}`)}
              </Button>
            </div>
          </AutoForm>
        </>
      )}
    </section>
  )
}
