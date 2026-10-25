import { useEffect, useRef, useState } from "react"
import { Link } from "react-router"
import { Button } from "@workspace/ui/components/button"
import { Input } from "@workspace/ui/components/input"
import { useTranslations } from "@workspace/ui/i18n/intl-provider"
import { Alert } from "../../components/alert"
import { iamApi } from "../../lib/iam-api"
import type { components } from "../../lib/api-schema"
import { conversationHubApi, isActiveHubExecution } from "./hub-api"
import { ConversationMarkdown } from "./message"

export function HubActivityCollection({tenant, kind}: {tenant: string; kind: "MENTIONS" | "REPLIES"}) {
  const t = useTranslations("iam")
  const [items, setItems] = useState<components["schemas"]["HubActivity"][]>([])
  const [cursor, setCursor] = useState("")
  const [next, setNext] = useState("")
  const [revision, setRevision] = useState(0)
  const [loading, setLoading] = useState(true)
  const [error, setError] = useState("")
  useEffect(() => {
    const controller = new AbortController()
    void conversationHubApi.activity(tenant, kind, cursor, controller.signal).then(page => {if (!controller.signal.aborted) {setItems(page.items ?? []); setNext(page.next_cursor ?? ""); setError("")}}, cause => {if (!controller.signal.aborted) {setItems([]); setNext(""); setError(cause.message)}}).finally(() => {if (!controller.signal.aborted) setLoading(false)})
    return () => controller.abort()
  }, [tenant, kind, cursor, revision])
  return <section className="min-w-0 space-y-4">
    <Button variant="outline" disabled={loading} onClick={() => {setLoading(true); setRevision(value => value + 1)}}>{t("refresh")}</Button>
    {error && <Alert>{error}</Alert>}{loading && <p role="status">{t("loading")}</p>}
    <ol className="divide-y">{items.map(item => <li className="min-w-0 space-y-3 py-4" key={item.message.id}>
      <Link className="inline-flex min-h-11 items-center underline" to={item.channel.project_id ? `/admin/${encodeURIComponent(tenant)}/entities/${encodeURIComponent(item.channel.project_id)}/conversations?channel=${encodeURIComponent(item.channel.id ?? "")}&thread=${encodeURIComponent(item.message.reply_to_message_id ?? item.message.id ?? "")}` : `/admin/${encodeURIComponent(tenant)}/conversations?direct=${encodeURIComponent(item.channel.id ?? "")}`}>{item.channel.name}</Link>
      <p className="text-xs text-muted-foreground">{item.message.author_id} · {item.message.created_at}</p><ConversationMarkdown body={item.message.body} />
    </li>)}</ol>
    {!loading && !error && !items.length && <p>{t("conversationEmpty")}</p>}
    <div className="flex gap-3"><Button variant="outline" disabled={loading || !cursor} onClick={() => {setCursor(""); setLoading(true)}}>{t("conversationHubFirstPage")}</Button><Button variant="outline" disabled={loading || !next} onClick={() => {setCursor(next); setLoading(true)}}>{t("next")}</Button></div>
  </section>
}

export function HubToolApproval({approval, reason, busy, onReason, onDecide}: {approval: components["schemas"]["ApprovalView"]; reason: string; busy: boolean; onReason: (reason: string) => void; onDecide: (state: "ALLOW" | "DENY") => void}) {
  const t = useTranslations("iam")
  return <section className="grid gap-3 rounded-lg border p-4" aria-label={t("executionPermissionTitle")}>
    <h3 className="font-semibold">{t("executionPermissionTitle")}: {approval.tool}</h3>
    <pre aria-label={t("executionPermissionInput")} className="max-h-64 overflow-auto whitespace-pre-wrap break-words text-sm">{JSON.stringify(approval.input, null, 2)}</pre>
    <label className="grid gap-2 text-sm">{t("executionPermissionReason")}<textarea className="min-h-24 rounded-md border bg-background p-3" maxLength={2000} value={reason} disabled={busy} onChange={event => onReason(event.target.value)} /></label>
    <div className="flex flex-wrap gap-2">{(["ALLOW", "DENY"] as const).map(state => <Button key={state} variant={state === "DENY" ? "outline" : "default"} disabled={busy || !reason.trim()} onClick={() => onDecide(state)}>{t(state === "ALLOW" ? "executionPermissionAllow" : "executionPermissionDeny")}</Button>)}</div>
  </section>
}

export function HubExecutionCollection({tenant}: {tenant: string}) {
  const t = useTranslations("iam")
  const [project, setProject] = useState("")
  const [draft, setDraft] = useState("")
  const [page, setPage] = useState<{identity: string; value: components["schemas"]["HubExecutionSessionPage"]} | null>(null)
  const [cursor, setCursor] = useState("")
  const [selected, setSelected] = useState("")
  const [detail, setDetail] = useState<{identity: string; value: components["schemas"]["ExecutionDetail"]} | null>(null)
  const [revision, setRevision] = useState(0)
  const [error, setError] = useState("")
  const [loading, setLoading] = useState(false)
  const [busyIdentity, setBusyIdentity] = useState("")
  const [reasons, setReasons] = useState<Record<string, string>>({})
  const [savedIdentity, setSavedIdentity] = useState("")
  const decision = useRef<AbortController | null>(null)
  const pageIdentity = `${tenant}:${project}:${cursor}:${revision}`
  const detailIdentity = `${tenant}:${project}:${selected}:${revision}`
  const busy = busyIdentity === detailIdentity
  useEffect(() => {
    if (!project) return
    const controller = new AbortController()
    void conversationHubApi.sessions(tenant, project, cursor, controller.signal).then(value => {if (!controller.signal.aborted) {setPage({identity: pageIdentity, value}); setError("")}}, cause => {if (!controller.signal.aborted) {setPage(null); setDetail(null); setSelected(""); setError(cause.message)}}).finally(() => {if (!controller.signal.aborted) setLoading(false)})
    return () => controller.abort()
  }, [tenant, project, cursor, revision, pageIdentity])
  useEffect(() => {
    decision.current?.abort()
    if (!selected) return
    const controller = new AbortController()
    void iamApi.execution(selected, controller.signal).then(value => {if (!controller.signal.aborted) {if (value.execution.project_id !== project) {setDetail(null); setError(t("executionWrongProject")); return}; setDetail({identity: detailIdentity, value})}}, cause => {if (!controller.signal.aborted) {setDetail(null); setError(cause.message)}})
    return () => {controller.abort(); decision.current?.abort()}
  }, [tenant, selected, project, revision, detailIdentity, t])
  const currentPage = page?.identity === pageIdentity ? page.value : null
  const currentDetail = detail?.identity === detailIdentity ? detail.value : null
  const running = currentPage?.items?.filter(row => isActiveHubExecution(row.state)) ?? []
  const selectedSession = running.find(row => row.id === selected)
  const decide = async (approval: string, state: "ALLOW" | "DENY") => {
    if (decision.current && !decision.current.signal.aborted || !selectedSession?.current_responder || !(reasons[`${detailIdentity}:${approval}`] ?? "").trim()) return
    const controller = new AbortController(); decision.current = controller
    setBusyIdentity(detailIdentity); setError(""); setSavedIdentity("")
    try {
      await iamApi.decideExecutionPermission(selected, approval, state, reasons[`${detailIdentity}:${approval}`] ?? "")
      if (controller.signal.aborted) return
      // Invalidate the old approval immediately, then reload authoritative detail.
      setDetail(null)
      const [value, refreshedPage] = await Promise.all([iamApi.execution(selected, controller.signal), conversationHubApi.sessions(tenant, project, cursor, controller.signal)])
      if (controller.signal.aborted) return
      if (value.execution.project_id !== project) {setError(t("executionWrongProject")); return}
      setDetail({identity: detailIdentity, value}); setPage({identity: pageIdentity, value: refreshedPage}); setSavedIdentity(detailIdentity)
    } catch (cause) {
      if (!controller.signal.aborted) {setDetail(null); setError(cause instanceof Error ? cause.message : t("executionUnavailable"))}
    } finally {if (!controller.signal.aborted) {setBusyIdentity(""); decision.current = null}}
  }
  return <section className="min-w-0 space-y-4">
    <form className="flex flex-wrap items-end gap-3" onSubmit={event => {event.preventDefault(); if (!/^[1-9][0-9]{0,18}$/.test(draft)) return; decision.current?.abort(); setProject(draft); setCursor(""); setSelected(""); setDetail(null); setPage(null); setError(""); setLoading(true); setRevision(value => value + 1)}}><label className="grid gap-2 text-sm">{t("auditProjectID")}<Input value={draft} onChange={event => setDraft(event.target.value)} required pattern="[1-9][0-9]{0,18}" /></label><Button disabled={loading} type="submit">{t("refresh")}</Button></form>
    {error && <Alert>{error}</Alert>}{loading && <p role="status">{t("loading")}</p>}
    <ul className="divide-y">{running.map(row => <li className="flex flex-wrap items-center gap-3 py-3" key={row.id}><Button variant="outline" disabled={busy} onClick={() => {decision.current?.abort(); setSelected(row.id ?? ""); setDetail(null); setError("")}}>{row.id}</Button><span>{t(`executionState_${row.state}`)}</span><span>{row.plan.runtime} / {row.plan.model}</span>{row.pending_interaction_count > 0 && <span>{t("executionPermissionTitle")}: {row.pending_interaction_count}</span>}<Link className="inline-flex min-h-11 items-center underline" to={`/admin/${encodeURIComponent(tenant)}/entities/${encodeURIComponent(project)}/executions/${encodeURIComponent(row.id ?? "")}`}>{t("fleetDetails")}</Link></li>)}</ul>
    {currentPage && !loading && !running.length && <p>{t("executionEmpty")}</p>}
    {currentPage && <div className="flex gap-3"><Button variant="outline" disabled={loading || busy || !cursor} onClick={() => {setCursor(""); setLoading(true); setSelected(""); setDetail(null)}}>{t("conversationHubFirstPage")}</Button><Button variant="outline" disabled={loading || busy || !currentPage.has_more || !currentPage.next_cursor} onClick={() => {setCursor(currentPage.next_cursor ?? ""); setLoading(true); setSelected(""); setDetail(null)}}>{t("next")}</Button></div>}
    {currentDetail && selectedSession && <section className="min-w-0 space-y-3 rounded border p-4"><h2 className="break-all font-medium">{currentDetail.execution.id} / {t(`executionState_${currentDetail.execution.state}`)}</h2><p className="whitespace-pre-wrap break-words">{currentDetail.execution.plan.objective}</p>{currentDetail.has_more_logs && <Alert>{t("executionLogsTruncated")}</Alert>}<pre className="max-h-[50dvh] overflow-auto whitespace-pre-wrap break-words text-xs">{currentDetail.logs?.map(row => `[${row.dispatch_epoch}:${row.seq}] ${row.content}`).join("\n") || t("executionNoLogs")}</pre>
      {selectedSession.current_responder && (currentDetail.approvals ?? []).filter((approval): approval is components["schemas"]["ApprovalView"] & {id: string} => approval.state === "PENDING" && typeof approval.id === "string" && approval.id.length > 0).map(approval => <HubToolApproval key={approval.id} approval={approval} reason={reasons[`${detailIdentity}:${approval.id}`] ?? ""} busy={busy} onReason={reason => setReasons(previous => ({...previous, [`${detailIdentity}:${approval.id}`]: reason}))} onDecide={state => void decide(approval.id, state)} />)}
    </section>}
    {savedIdentity === detailIdentity && <p role="status">{t("executionPermissionSaved")}</p>}
  </section>
}
