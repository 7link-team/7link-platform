import { useEffect, useRef, useState } from "react"
import { Link, useParams } from "react-router"
import { AutoForm } from "@workspace/ui/components/auto-form"
import { Button } from "@workspace/ui/components/button"
import { useTranslations } from "@workspace/ui/i18n/intl-provider"
import { Alert } from "../../components/alert"
import type { ProjectDetail } from "../../lib/iam-api"
import { useAllowed } from "../../lib/permissions"
import { archiveResourceHref, projectArchiveApi, type ArchivePreflight } from "./archive-api"

export function ProjectArchive({project, onChanged}: {project: ProjectDetail; onChanged: () => void}) {
  const t = useTranslations("iam"), allowed = useAllowed(), {tenantID = ""} = useParams()
  const [preflight, setPreflight] = useState<ArchivePreflight | null>(null)
  const [error, setError] = useState(""), [busy, setBusy] = useState(false), [loading, setLoading] = useState(false), [reload, setReload] = useState(0)
  const mounted = useRef(true), changing = useRef(false), attempt = useRef<{identity: string; key: string} | null>(null)
  const restore = !!project.archived_at, canManage = allowed("project_manage")
  useEffect(() => {mounted.current = true; return () => {mounted.current = false}}, [])
  useEffect(() => {
    const controller = new AbortController()
    if (!canManage || restore) return () => controller.abort()
    queueMicrotask(() => {
    if (controller.signal.aborted) return
    setLoading(true); setPreflight(null); setError("")
    void projectArchiveApi.preflight(project.project_id!, controller.signal).then(value => {
      if (!controller.signal.aborted) setPreflight(value)
    }, cause => {if (!controller.signal.aborted) setError(cause instanceof Error ? cause.message : t("projectUnavailable"))}).finally(() => {if (!controller.signal.aborted) setLoading(false)})
    })
    return () => controller.abort()
  }, [project.project_id, project.version, canManage, restore, reload, t])
  return <section className="grid gap-3 border-b py-4" aria-label={t("archiveTitle")} aria-busy={busy || loading}>
    <h2 className="font-medium">{t(restore ? "archiveReadOnly" : "archiveTitle")}</h2>
    <p className="text-sm text-muted-foreground">{t(restore ? "archiveRestoreNotice" : "archiveNotice")}</p>
    {error && <Alert>{error}</Alert>}
    {canManage && !restore && <>
      <Button className="min-h-11 justify-self-start" variant="outline" disabled={busy || loading} onClick={() => setReload(value => value + 1)}>{t("archiveRefresh")}</Button>
      {loading && <p role="status">{t("loading")}</p>}
      {preflight && <>
        <p role="status">{t(preflight.ready ? "archiveReady" : "archiveBlocked")}</p>
        {preflight.unavailable_owners.length > 0 && <Alert>{t("archiveOwnerUnavailable")}: {preflight.unavailable_owners.join(", ")}</Alert>}
        <ul className="grid gap-2">{preflight.resources.map(resource => {
          const href = archiveResourceHref(tenantID, project.project_id!, resource)
          return <li key={`${resource.kind}:${resource.id}`} className="flex flex-wrap items-center justify-between gap-2 border-b py-3 text-sm">
            <div className="min-w-0 break-words"><p>{resource.kind} · {resource.id}</p><p>{resource.state} · v{resource.version} · {t(resource.ready ? "archiveSettled" : "archivePending")}</p></div>
            {!resource.ready && (href ? <Link className="inline-flex min-h-11 items-center underline" to={href}>{t("archiveDisposition")}</Link> : <span>{t("archiveOwnerAction")}</span>)}
          </li>
        })}</ul>
      </>}
    </>}
    {canManage && <fieldset disabled={busy || (!restore && (loading || !!error || !preflight?.ready || preflight.version !== project.version))}>
      <AutoForm key={`${project.project_id}:${project.version}`} mode="edit" fields={{reason: {name: t("archiveReason"), type: "textarea", required: true, config: {rules: [value => Array.from(String(value ?? "").trim()).length < 10 || Array.from(String(value ?? "")).length > 2000 ? t("archiveReasonRequired") : undefined]}}, confirm: {name: t(restore ? "archiveRestoreConfirm" : "archiveConfirm"), type: "checkbox", required: true}}} onSubmit={async values => {
        if (changing.current || values.confirm !== true || (!restore && !preflight?.ready)) return
        const reason = String(values.reason ?? ""), identity = JSON.stringify({project: project.project_id, version: project.version, restore, reason, hash: preflight?.hash})
        if (attempt.current?.identity !== identity) attempt.current = {identity, key: crypto.randomUUID()}
        changing.current = true; setBusy(true); setError("")
        try {
          await projectArchiveApi.change(project.project_id!, project.version, restore, reason, attempt.current.key, preflight?.hash)
          if (mounted.current) {setPreflight(null); onChanged()}
        } catch (cause) {
          if (mounted.current) setError(cause instanceof Error ? cause.message : t("projectUnavailable"))
          throw cause
        } finally {changing.current = false; if (mounted.current) setBusy(false)}
      }}><Button className="min-h-11" type="submit" variant={restore ? "default" : "outline"}>{t(restore ? "archiveRestore" : "archiveApply")}</Button></AutoForm>
    </fieldset>}
  </section>
}
