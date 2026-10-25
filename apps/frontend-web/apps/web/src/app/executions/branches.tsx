import { useCallback, useRef, useState } from "react"
import { Link, useSearchParams } from "react-router"
import { AutoForm } from "@workspace/ui/components/auto-form"
import { Button } from "@workspace/ui/components/button"
import { useLocale, useTranslations } from "@workspace/ui/i18n/intl-provider"
import { Alert } from "../../components/alert"
import { useAllowed } from "../../lib/permissions"
import { collaborationID } from "../../lib/clientwork-api"
import { ApiError, iamApi } from "../../lib/iam-api"
import { useFacts } from "../runtime/use-facts"
import { ProvenanceFacts } from "./provenance"
import { provenanceApi } from "./provenance-api"
import { branchApi, branchMessages, type Branch, type BranchPage, type StrategyInput } from "./branches-data"

export function BranchHistoryFacts({page, tenantID, source}: {page: BranchPage; tenantID: string; source: Branch}) {
  const locale = useLocale(), t = useTranslations("iam"), [search, setSearch] = useSearchParams()
  const l = branchMessages[locale as keyof typeof branchMessages] ?? branchMessages["en-US"]
  const path = (id: string | null) => `/admin/${encodeURIComponent(tenantID)}/entities/${encodeURIComponent(collaborationID(source.project_id))}/executions/${encodeURIComponent(collaborationID(id))}`
  return <ol className="grid min-w-0 gap-3" aria-label={l.title}>{page.items.map(row => <li key={row.id} className="grid min-w-0 gap-2 border-l-2 pl-3 text-sm" aria-current={row.id === source.id ? "true" : undefined}>
    <div className="flex flex-wrap items-center gap-3"><Link className="min-h-11 break-all underline" to={path(row.id)}>{row.id}</Link><span>{row.branch_reason || l.unknown} · {t(`executionState_${row.state}`)}</span></div>
    <p className="break-all">{row.parent_execution_id ? <>{l.parent}: <Link className="underline" to={path(row.parent_execution_id)}>{row.parent_execution_id}</Link> → {row.id}</> : l.root}</p>
    <p className="break-words">{row.plan.objective}</p><p className="break-all">{t("executionBaseCommit")}: {row.plan.base_commit}</p>
    {row.id !== source.id && <Button className="w-fit" variant="outline" aria-pressed={search.get("compare_branch") === row.id} onClick={() => setSearch(previous => {const next = new URLSearchParams(previous); next.set("compare_branch", collaborationID(row.id)); return next})}>{l.compare}</Button>}
  </li>)}{!page.items.length && <li>{l.empty}</li>}</ol>
}
export function ExecutionBranches({tenantID, source}: {tenantID: string; source: Branch}) {
  const locale = useLocale(), t = useTranslations("iam"), allowed = useAllowed(), [search, setSearch] = useSearchParams()
  const l = branchMessages[locale as keyof typeof branchMessages] ?? branchMessages["en-US"]
  const cursor = search.get("branch_cursor") ?? "", compare = search.get("compare_branch") ?? ""
  const load = useCallback((signal: AbortSignal) => branchApi.list(collaborationID(source.id), cursor, signal), [source.id, cursor])
  const history = useFacts(`${source.id}:${cursor}`, load)
  const loadComparison = useCallback(async (signal: AbortSignal) => {
    if (!compare) return null
    const detail = await iamApi.execution(compare, signal)
    if (detail.execution.id === source.id || detail.execution.project_id !== source.project_id || detail.execution.work_item_id !== source.work_item_id) throw new Error(l.mismatch)
    const [current, alternative] = await Promise.all([provenanceApi.list(collaborationID(source.id), "", compare, signal), provenanceApi.list(compare, "", collaborationID(source.id), signal)])
    return {current, alternative, execution: detail.execution}
  }, [compare, source.id, source.project_id, source.work_item_id, l.mismatch])
  const comparison = useFacts(`${source.id}:${compare}`, loadComparison)
  const [error, setError] = useState(""), [busy, setBusy] = useState(false), [created, setCreated] = useState<Branch | null>(null)
  const pending = useRef<{body: StrategyInput; key: string; version: number} | null>(null)
  const [uncertain, setUncertain] = useState(false)
  const send = async (body?: StrategyInput) => {
    if (busy) return
    if (!pending.current && body) pending.current = {body, key: crypto.randomUUID(), version: source.version}
    const request = pending.current; if (!request) return
    setBusy(true); setError("")
    try {const row = await branchApi.create(collaborationID(source.id), request.version, request.body, request.key); setCreated(row); pending.current = null; setUncertain(false); history.refresh()}
    catch (cause) {const definitive = cause instanceof ApiError && cause.status >= 400 && cause.status < 500; if (definitive) pending.current = null; setUncertain(!definitive); setError(cause instanceof Error ? cause.message : t("executionUnavailable"))}
    finally {setBusy(false)}
  }
  const go = (cursor: string) => setSearch(previous => {const next = new URLSearchParams(previous); if (cursor) next.set("branch_cursor", cursor); else next.delete("branch_cursor"); return next})
  return <section className="grid min-w-0 gap-4" aria-label={l.title}>
    <h2 className="font-semibold">{l.title}</h2><p className="text-sm text-muted-foreground">{l.hint}</p>
    {history.error && <Alert>{history.error}</Alert>}{history.loading && <p role="status">{t("loading")}</p>}
    {history.data && <BranchHistoryFacts page={history.data} tenantID={tenantID} source={source}/>}
    <div className="flex flex-wrap gap-3"><Button variant="outline" disabled={!cursor || history.loading} onClick={() => go("")}>{l.first}</Button><Button variant="outline" disabled={!history.data?.has_more || history.loading} onClick={() => go(history.data?.next_cursor ?? "")}>{l.more}</Button></div>
    {compare && <section className="grid min-w-0 gap-3" aria-label={l.compare}><h3 className="font-semibold">{l.compare}</h3>{comparison.error && <Alert>{comparison.error}</Alert>}{comparison.loading && <p role="status">{t("loading")}</p>}{comparison.data && <div className="grid min-w-0 gap-6 lg:grid-cols-2"><section className="min-w-0"><h4 className="break-all font-semibold">{l.current}: {source.id}</h4><ProvenanceFacts page={comparison.data.current}/></section><section className="min-w-0"><h4 className="break-all font-semibold">{l.alternative}: {comparison.data.execution.id}</h4><ProvenanceFacts page={comparison.data.alternative}/></section></div>}</section>}
    {allowed("execution_start") && !["ENSEMBLE", "SKILL_DISTILLATION"].includes(source.branch_reason) && <section className="grid gap-3 border-t pt-4"><h3 className="font-semibold">{l.create}</h3><p className="text-sm text-muted-foreground">{l.baseline}</p>
      {error && <Alert>{error}</Alert>}{uncertain ? <><p role="status">{l.pending}</p><Button disabled={busy} onClick={() => void send()}>{l.retry}</Button></> : <AutoForm mode="edit" initialData={{reason: "", objective: source.plan.objective, machine_id: ""}} fields={{reason: {name: l.reason, type: "textarea", required: true}, objective: {name: l.objective, type: "textarea", required: true}, machine_id: {name: l.machine, type: "text"}}} onSubmit={values => send({reason: String(values.reason ?? "").trim(), objective: String(values.objective ?? "").trim(), ...(values.machine_id ? {machine_id: String(values.machine_id).trim()} : {})})}><Button type="submit" disabled={busy}>{l.create}</Button></AutoForm>}
      {created && <p role="status">{l.saved}: <Link className="break-all underline" to={`/admin/${encodeURIComponent(tenantID)}/entities/${encodeURIComponent(collaborationID(source.project_id))}/executions/${encodeURIComponent(collaborationID(created.id))}`}>{created.id}</Link></p>}
    </section>}
  </section>
}
