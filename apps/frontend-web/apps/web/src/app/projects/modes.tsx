import { useEffect, useRef, useState } from "react"
import { AutoForm } from "@workspace/ui/components/auto-form"
import { Button } from "@workspace/ui/components/button"
import { PaginationPages } from "@workspace/ui/components/pagination"
import { useLocale, useTranslations } from "@workspace/ui/i18n/intl-provider"
import { Alert } from "../../components/alert"
import { ReauthenticationDialog } from "../../components/reauthentication-dialog"
import type { ProjectDetail } from "../../lib/iam-api"
import { useAllowed } from "../../lib/permissions"
import { modeNeedsProof, modeTargets, projectModesApi, type ModeCriterion, type ModeHistory, type TeamMode } from "./modes-api"

export function ModeFacts({criteria, history}: {criteria: ModeCriterion[]; history: ModeHistory[]}) {
  const t = useTranslations("iam"), locale = useLocale()
  return <>
    <h3 className="font-medium">{t("modePrerequisites")}</h3>
    <ul className="grid gap-2 text-sm">{criteria.map(c => <li key={c.key}>{c.passed ? "✓" : "✗"} {t.has(`modeCriterion_${c.key}`) ? t(`modeCriterion_${c.key}`) : c.detail}</li>)}</ul>
    <h3 className="font-medium">{t("modeHistory")}</h3>
    <ol className="grid gap-3 text-sm">{history.map(row => <li key={row.id} className="grid gap-1 border-l-2 pl-3"><p>{row.from_mode} → {row.to_mode} · {row.trigger}</p><p className="whitespace-pre-wrap break-words">{row.reason}</p><p>{row.actor_id} · {row.authority_source} · {new Date(row.occurred_at).toLocaleString(locale)}</p><p>{t("modeRevoked")}: {row.revoked_delegation_ids?.length ?? 0} / {row.revoked_spawn_grant_ids?.length ?? 0}</p></li>)}</ol>
  </>
}

export function ProjectModes({project, onChanged}: {project: ProjectDetail; onChanged: () => void}) {
  const t = useTranslations("iam"), allowed = useAllowed()
  const [criteria, setCriteria] = useState<ModeCriterion[]>([]), [history, setHistory] = useState<ModeHistory[]>([])
  const [offset, setOffset] = useState(0), [total, setTotal] = useState(0), [reload, setReload] = useState(0)
  const [loading, setLoading] = useState(true), [busy, setBusy] = useState(false), [error, setError] = useState("")
  const [confirmation, setConfirmation] = useState<{target: TeamMode; reason: string; takeover: boolean} | null>(null)
  const attempt = useRef<{identity: string; key: string} | null>(null), changing = useRef(false)
  const mounted = useRef(true)
  const readOnly = !!project.archived_at || ["CLOSED", "CANCELLED"].includes(project.stage)
  useEffect(() => {
    mounted.current = true
    const controller = new AbortController()
    queueMicrotask(() => { if (!controller.signal.aborted) { setLoading(true); setError("") } })
    void Promise.all([readOnly ? Promise.resolve([]) : projectModesApi.criteria(project.project_id!, controller.signal), projectModesApi.history(project.project_id!, offset, controller.signal)]).then(([facts, page]) => {
      if (!controller.signal.aborted) { setCriteria(facts); setHistory(page.items); setTotal(page.total) }
    }, cause => { if (!controller.signal.aborted) { setCriteria([]); setHistory([]); setError(cause instanceof Error ? cause.message : t("workUnavailable")) } }).finally(() => { if (!controller.signal.aborted) setLoading(false) })
    return () => { mounted.current = false; controller.abort() }
  }, [project.project_id, project.version, offset, reload, readOnly, t])
  const submit = async (value: {target: TeamMode; reason: string; takeover: boolean}, proof?: string) => {
    if (changing.current) return
    const identity = JSON.stringify({project: project.project_id, version: project.version, ...value})
    if (attempt.current?.identity !== identity) attempt.current = {identity, key: crypto.randomUUID()}
    changing.current = true; setBusy(true); setError("")
    try {
      await projectModesApi.change(project.project_id!, project.version, value.target, value.reason, value.takeover, attempt.current.key, proof)
      if (mounted.current) { setConfirmation(null); setReload(v => v + 1); onChanged() }
    } catch (cause) {
      if (mounted.current) setError(cause instanceof Error ? cause.message : t("workUnavailable"))
      throw cause
    } finally { changing.current = false; if (mounted.current) setBusy(false) }
  }
  return <section className="grid gap-3 border-t py-4" aria-label={t("modeTitle")} aria-busy={loading || busy}>
    <h2 className="font-medium">{t("modeTitle")}</h2><p className="text-sm text-muted-foreground">{t("modeNotice")}</p>
    {error && <Alert>{error}</Alert>}
    <Button className="min-h-11 justify-self-start" variant="outline" disabled={busy || loading} onClick={() => setReload(v => v + 1)}>{t("refresh")}</Button>
    {loading ? <p role="status">{t("loading")}</p> : <ModeFacts criteria={criteria} history={history}/>}
    <PaginationPages page={Math.floor(offset / 10) + 1} totalPages={Math.max(1, Math.ceil(total / 10))} onPageChange={page => setOffset((page - 1) * 10)}/>
    {!readOnly && allowed("project_mode_change") && <fieldset disabled={busy || loading || !!error}><AutoForm key={`${project.project_id}:${project.version}`} mode="edit" fields={{target: {name: t("modeTarget"), type: "select", required: true, config: {options: modeTargets(project.team_mode).map(value => ({value, label: t(`project_team_${value}`)}))}}, reason: {name: t("stageReason"), type: "textarea", required: true, config: {rules: [v => Array.from(String(v ?? "").trim()).length < 10 ? t("modeReasonRequired") : undefined]}}}} onSubmit={async values => {
      const value = {target: values.target as TeamMode, reason: String(values.reason), takeover: false}
      if (modeNeedsProof(project.team_mode, value.target, false)) setConfirmation(value)
      else await submit(value)
    }}><Button type="submit" className="min-h-11">{t("modeApply")}</Button></AutoForm></fieldset>}
    {!readOnly && allowed("project_takeover") && <fieldset disabled={busy || loading || !!error}><AutoForm mode="edit" fields={{reason: {name: t("stageReason"), type: "textarea", required: true, config: {rules: [v => Array.from(String(v ?? "").trim()).length < 10 ? t("modeReasonRequired") : undefined]}}}} onSubmit={async values => setConfirmation({target: "HYBRID", reason: String(values.reason), takeover: true})}><Button type="submit" className="min-h-11" variant="outline">{t("modeTakeover")}</Button></AutoForm></fieldset>}
    {confirmation && <ReauthenticationDialog purpose={confirmation.takeover ? "project_takeover" : "team_mode_change"} target={`project:${project.project_id}`} onClose={() => {if (!busy) setConfirmation(null)}} onVerified={proof => submit(confirmation, proof)}/>}
  </section>
}
