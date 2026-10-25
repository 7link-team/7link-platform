import { useCallback, useEffect, useRef, useState } from "react"
import { AutoForm } from "@workspace/ui/components/auto-form"
import { Button } from "@workspace/ui/components/button"
import { useLocale, useTranslations } from "@workspace/ui/i18n/intl-provider"
import { Alert } from "../../components/alert"
import {
  iamApi,
  type WorkAssignment,
  type WorkItemDetail,
} from "../../lib/iam-api"
import { useAllowed } from "../../lib/permissions"

export function WorkAssignmentControls({
  entityID,
  detail,
  onUpdated,
}: {
  entityID: string
  detail: WorkItemDetail
  onUpdated: (detail: WorkItemDetail) => void
}) {
  const t = useTranslations("iam")
  const locale = useLocale()
  const allowed = useAllowed()
  const [assignment, setAssignment] = useState<WorkAssignment | null>(null)
  const [subject, setSubject] = useState("")
  const [projectActive, setProjectActive] = useState(false)
  const [loading, setLoading] = useState(true)
  const [busy, setBusy] = useState(false)
  const [error, setError] = useState("")
  const [action, setAction] = useState<"claim" | "renew" | "release" | null>(
    null
  )
  const pending = useRef(false)
  const mounted = useRef(true)
  const controller = useRef<AbortController | null>(null)
  const attempt = useRef<{ identity: string; key: string } | null>(null)
  const load = useCallback(async () => {
    controller.current?.abort()
    const abort = new AbortController()
    controller.current = abort
    setLoading(true)
    setError("")
    try {
      const [session, project, assignments, current] = await Promise.all([
        iamApi.session(),
        iamApi.project(entityID, abort.signal),
        iamApi.workAssignments(detail.id!, abort.signal),
        iamApi.workItem(entityID, detail.id!, abort.signal),
      ])
      if (abort.signal.aborted) return
      if (current.version !== detail.version) onUpdated(current)
      setSubject(session.subject)
      setProjectActive(project.stage === "ACTIVE" && !project.archived_at)
      setAssignment(assignments.items[0] ?? null)
    } catch (cause) {
      if (!abort.signal.aborted)
        setError(cause instanceof Error ? cause.message : t("workUnavailable"))
    } finally {
      if (!abort.signal.aborted) setLoading(false)
    }
  }, [entityID, detail.id, detail.version, onUpdated, t])
  useEffect(() => {
    mounted.current = true
    let active = true
    queueMicrotask(() => {
      if (active) void load()
    })
    return () => {
      active = false
      mounted.current = false
      controller.current?.abort()
    }
  }, [load, detail.version])
  const own =
    assignment?.assignee_kind === "HUMAN" &&
    assignment.assignee_principal_id === subject
  const canClaim =
    !assignment &&
    projectActive &&
    !detail.paused_at &&
    !detail.is_container &&
    ["DRAFT", "READY"].includes(detail.state) &&
    !detail.archived_at
  return (
    <section
      className="grid gap-3 border-t pt-4"
      aria-label={t("assignmentTitle")}
    >
      <div className="flex flex-wrap items-center justify-between gap-2">
        <h2 className="font-medium">{t("assignmentTitle")}</h2>
        <Button
          size="sm"
          variant="outline"
          disabled={loading || busy}
          onClick={() => {
            void load()
          }}
        >
          {t("refresh")}
        </Button>
      </div>
      {error && <Alert>{error}</Alert>}
      {loading && <p role="status">{t("loading")}</p>}
      {detail.readiness_override_by && (
        <Alert>{t("assignmentOverrideWarning")}</Alert>
      )}
      {!loading && !error && (
        <>
          {assignment ? (
            <AutoForm
              mode="detail"
              initialData={{
                holder: assignment.assignee_principal_id,
                lease: assignment.lease_expires_at
                  ? new Date(assignment.lease_expires_at).toLocaleString(locale)
                  : t("assignmentSuspended"),
                version: assignment.version,
              }}
              fields={{
                holder: { name: t("assignmentHolder"), type: "text" },
                lease: { name: t("assignmentExpires"), type: "text" },
                version: { name: t("projectVersion"), type: "number" },
              }}
            />
          ) : (
            <p className="text-sm text-muted-foreground">
              {projectActive
                ? t("assignmentUnclaimed")
                : t("assignmentActiveRequired")}
            </p>
          )}
          {!action && allowed("work_item_execute") && (
            <div className="flex flex-wrap gap-2">
              {canClaim && (
                <Button disabled={busy} onClick={() => setAction("claim")}>
                  {t("assignmentClaim")}
                </Button>
              )}
              {own && (
                <>
                  <Button
                    disabled={busy}
                    variant="outline"
                    onClick={() => setAction("renew")}
                  >
                    {t("assignmentRenew")}
                  </Button>
                  <Button
                    disabled={busy}
                    variant="outline"
                    onClick={() => setAction("release")}
                  >
                    {t("assignmentRelease")}
                  </Button>
                </>
              )}
            </div>
          )}
        </>
      )}
      {action && (
        <div className="grid gap-3 rounded-md border p-3">
          <p className="text-sm">{t(`assignmentConfirm_${action}`)}</p>
          {action === "claim" && detail.state === "DRAFT" && (
            <Alert>{t("assignmentOverrideWarning")}</Alert>
          )}
          <AutoForm
            key={`${action}:${detail.version}:${assignment?.version}`}
            mode="edit"
            initialData={{ acknowledge: false }}
            fields={{
              acknowledge: {
                name: t("assignmentAcknowledge"),
                type: "checkbox",
                show: action === "claim" && detail.state === "DRAFT",
                required: action === "claim" && detail.state === "DRAFT",
              },
            }}
            onSubmit={async (values) => {
              if (pending.current || !subject || !allowed("work_item_execute"))
                return
              if (
                action === "claim" &&
                detail.state === "DRAFT" &&
                !values.acknowledge
              ) {
                setError(t("assignmentAcknowledge"))
                return
              }
              const identity = JSON.stringify({
                action,
                work: detail.id!,
                version: detail.version,
                assignment: assignment?.id,
                assignmentVersion: assignment?.version,
                acknowledge: !!values.acknowledge,
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
                if (action === "claim")
                  await iamApi.claimWork(
                    detail.id!,
                    detail.version,
                    subject,
                    !!values.acknowledge,
                    key
                  )
                else if (assignment && own) {
                  if (action === "renew")
                    await iamApi.renewWorkAssignment(
                      assignment.id!,
                      assignment.version,
                      key
                    )
                  else
                    await iamApi.releaseWorkAssignment(
                      assignment.id!,
                      assignment.version,
                      key
                    )
                } else return
                const current = await iamApi.workItem(entityID, detail.id!)
                if (mounted.current) {
                  setAction(null)
                  onUpdated(current)
                  void load()
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
              <Button type="submit" disabled={busy}>
                {busy
                  ? t("saving")
                  : t(
                      `assignment${action[0]!.toUpperCase()}${action.slice(1)}`
                    )}
              </Button>
            </div>
          </AutoForm>
        </div>
      )}
    </section>
  )
}
