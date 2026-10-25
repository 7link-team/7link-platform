import { useEffect, useRef, useState } from "react"
import { useParams } from "react-router"
import { Button } from "@workspace/ui/components/button"
import { Input } from "@workspace/ui/components/input"
import { useLocale } from "@workspace/ui/i18n/intl-provider"
import { Alert } from "../../components/alert"
import { useAuth } from "../../components/auth"
import { useAllowed } from "../../lib/permissions"
import { basePath, read, write, type Page, type Summary, type Worklog } from "../work-management/api"
import { messages } from "../work-management/messages"
import { WorklogApproval } from "../work-management/page"
import { effortDraft, effortMessages, type EffortDraft, type EffortHistory } from "./effort-data"

export function EffortFields({ value, onChange, disabled, locale }: { value: EffortDraft; onChange: (value: EffortDraft) => void; disabled: boolean; locale: string }) {
  const labels = messages[locale as keyof typeof messages] ?? messages["en-US"]
  const text = effortMessages[locale as keyof typeof effortMessages] ?? effortMessages["en-US"]
  return <fieldset className="grid min-w-0 gap-3 sm:grid-cols-2" disabled={disabled}>
    <label className="grid gap-1 text-sm">{labels.workItem}<Input readOnly value={value.work_item_id} /></label>
    <label className="grid gap-1 text-sm">{labels.date}<Input required type="date" value={value.work_date} onChange={e => onChange({ ...value, work_date: e.target.value })} /></label>
    <label className="grid gap-1 text-sm">{text.timezone}<Input required maxLength={64} value={value.timezone} onChange={e => onChange({ ...value, timezone: e.target.value })} /></label>
    <label className="grid gap-1 text-sm">{labels.category}<select className="min-h-11 rounded-md border bg-background px-3" value={value.category} onChange={e => onChange({ ...value, category: e.target.value })}>{["DEVELOPMENT", "TEST", "REVIEW", "DESIGN", "DOC", "MEETING", "OPS", "OTHER"].map(v => <option key={v}>{v}</option>)}</select></label>
    <label className="grid gap-1 text-sm">{labels.minutes}<Input required type="number" min={1} max={1440} step={1} value={value.minutes} onChange={e => onChange({ ...value, minutes: Number(e.target.value) })} /></label>
    <label className="grid gap-1 text-sm">{labels.note}<Input maxLength={500} value={value.note} onChange={e => onChange({ ...value, note: e.target.value })} /></label>
    <label className="grid gap-1 text-sm sm:col-span-2">{text.reason}<Input required maxLength={500} value={value.reason} onChange={e => onChange({ ...value, reason: e.target.value })} /></label>
  </fieldset>
}

export function WorkEffort({ workID }: { workID: string }) {
  const locale = useLocale(), { tenantID = "", entityID = "" } = useParams()
  const { session } = useAuth(), allowed = useAllowed()
  const text = effortMessages[locale as keyof typeof effortMessages] ?? effortMessages["en-US"]
  const labels = messages[locale as keyof typeof messages] ?? messages["en-US"]
  const base = basePath(tenantID, entityID), path = `${base}/worklogs`
  const [page, setPage] = useState<Page<Worklog> | null>(null), [summary, setSummary] = useState<Summary | null>(null)
  const [cursor, setCursor] = useState(""), [trail, setTrail] = useState<string[]>([]), [reload, setReload] = useState(0)
  const [error, setError] = useState(""), [busy, setBusy] = useState(false)
  const [editing, setEditing] = useState<Worklog | "new" | null>(null), [draft, setDraft] = useState(() => effortDraft(workID))
  const [history, setHistory] = useState<EffortHistory | null>(null), [historyID, setHistoryID] = useState("")
  const [after, setAfter] = useState(""), [historyTrail, setHistoryTrail] = useState<string[]>([])
  const pending = useRef(false), attempt = useRef<{ signature: string; key: string } | null>(null)
  const canWrite = allowed("worklog_submit")
  useEffect(() => {
    const controller = new AbortController()
    queueMicrotask(() => { if (!controller.signal.aborted) { setPage(null); setSummary(null); setError("") } })
    const query = `work_item_id=${encodeURIComponent(workID)}`
    void Promise.all([read<Page<Worklog>>(`${path}?${query}&limit=25&cursor=${encodeURIComponent(cursor)}`, controller.signal), read<Summary>(`${base}/worklog-summary?${query}`, controller.signal)])
      .then(([rows, totals]) => { if (!controller.signal.aborted) { setPage(rows); setSummary(totals) } })
      .catch(cause => { if (!controller.signal.aborted) setError(String(cause.message ?? cause)) })
    return () => controller.abort()
  }, [base, path, workID, cursor, reload])
  useEffect(() => {
    if (!historyID) return
    const controller = new AbortController()
    queueMicrotask(() => { if (!controller.signal.aborted) setHistory(null) })
    void read<EffortHistory>(`${path}/${historyID}/history?limit=25&cursor=${encodeURIComponent(after)}`, controller.signal).then(value => { if (!controller.signal.aborted) setHistory(value) }).catch(cause => { if (!controller.signal.aborted) setError(String(cause.message ?? cause)) })
    return () => controller.abort()
  }, [path, historyID, after, reload])
  async function command(url: string, body: unknown, version?: number) {
    if (pending.current) return
    pending.current = true; setBusy(true); setError("")
    const signature = JSON.stringify([url, version, body])
    if (attempt.current?.signature !== signature) attempt.current = { signature, key: crypto.randomUUID() }
    try {
      await write(url, version === undefined ? "POST" : "PUT", body, attempt.current.key, version)
      attempt.current = null; setEditing(null); setReload(n => n + 1)
    } catch (cause) { setError(cause instanceof Error ? cause.message : String(cause)) }
    finally { pending.current = false; setBusy(false) }
  }
  return <section className="grid min-w-0 gap-3 border-t pt-4" aria-label={text.title}>
    <h3 className="font-semibold">{text.title}</h3><p className="text-sm text-muted-foreground">{text.hint}</p>
    {error && <Alert>{error}</Alert>}
    <Button type="button" variant="outline" disabled={busy} onClick={() => setReload(n => n + 1)}>{labels.reload}</Button>
    {!page && !error && <p role="status">{labels.loading}</p>}
    {summary && <dl className="grid gap-2 text-sm">{[[text.confirmed, summary.human_confirmed_minutes], [text.estimate, summary.human_unconfirmed_minutes], [text.agent, summary.agent_wall_minutes]].map(([label, value]) => <div key={String(label)}><dt>{label}</dt><dd>{value ?? text.unknown}</dd></div>)}</dl>}
    <p className="text-sm">{text.telemetry}</p>
    {canWrite && <Button type="button" disabled={busy} onClick={() => { setEditing("new"); setDraft(effortDraft(workID)) }}>{text.calibrate}</Button>}
    {editing && canWrite && <form className="grid gap-3" onSubmit={e => { e.preventDefault(); if (!draft.reason.trim()) return; const { reason, ...body } = draft; void command(editing === "new" ? path : `${path}/${editing.id}/calibration`, editing === "new" ? { ...body, calibration_reason: reason } : draft, editing === "new" ? undefined : editing.version) }}>
      <p>{text.source}</p><EffortFields value={draft} onChange={setDraft} disabled={busy} locale={locale} />
      <div className="flex flex-wrap gap-2"><Button type="submit" disabled={busy}>{labels.save}</Button><Button type="button" variant="outline" disabled={busy} onClick={() => setEditing(null)}>{labels.cancel}</Button></div>
    </form>}
    {page?.items.map(row => <article className="grid min-w-0 gap-2 border-t pt-3 text-sm" key={row.id}>
      <p>{row.actor_kind} · {row.principal_id} · {row.work_date} · {row.timezone ?? text.unknown} · {row.category}</p>
      <p>{labels.minutes}: {row.minutes}{row.actor_kind === "AGENT" && <> · {labels.cost}: {row.cost_micros ?? text.unknown}</>}</p><p className="break-words">{row.note}</p>
      <p>{row.confirmed_at ? `${text.confirmed}: ${row.confirmed_by} · ${row.confirmed_at}` : row.actor_kind === "HUMAN" ? text.legacy : text.agent}</p>
      <p>{labels.state}: {row.state}</p><WorklogApproval row={row} labels={labels} />
      {row.execution_id && <a className="underline" href={`/admin/${tenantID}/entities/${entityID}/executions/${encodeURIComponent(row.execution_id)}`}>Execution {row.execution_id}</a>}
      <div className="flex flex-wrap gap-2">
        <Button type="button" variant="outline" disabled={busy} onClick={() => { setHistoryID(row.id); setAfter(""); setHistoryTrail([]) }}>{text.history}</Button>
        {canWrite && row.principal_id === session?.subject && row.actor_kind === "HUMAN" && !row.execution_id && <>
          <Button type="button" variant="outline" disabled={busy} onClick={() => { setEditing(row); setDraft(effortDraft(workID, row)) }}>{text.calibrate}</Button>
          {!row.confirmed_at && <Button type="button" disabled={busy || !row.timezone} onClick={() => void command(`${path}/${row.id}/confirmation`, {}, row.version)}>{text.confirm}</Button>}
          {row.state === "DRAFT" && <Button type="button" disabled={busy} onClick={() => void command(`${path}/${row.id}/state`, { state: "SUBMITTED", reason: "" }, row.version)}>{labels.submit}</Button>}
        </>}
        {allowed("worklog_approve") && row.principal_id !== session?.subject && row.actor_kind === "HUMAN" && row.state === "SUBMITTED" && <Button type="button" disabled={busy} onClick={() => void command(`${path}/${row.id}/state`, { state: "APPROVED", reason: "" }, row.version)}>{labels.approve}</Button>}
      </div>
    </article>)}
    {page && !page.items.length && <p>{labels.empty}</p>}
    <div className="flex flex-wrap gap-2"><Button type="button" variant="outline" disabled={busy || !page || !trail.length} onClick={() => { setCursor(trail.at(-1) ?? ""); setTrail(v => v.slice(0, -1)) }}>{labels.previous}</Button><Button type="button" variant="outline" disabled={busy || !page?.has_more} onClick={() => { setTrail(v => [...v, cursor]); setCursor(page?.next_cursor ?? "") }}>{labels.next}</Button></div>
    {historyID && <section aria-label={text.history}><h4 className="font-medium">{text.history}</h4>{!history ? <p role="status">{labels.loading}</p> : <><ol className="grid gap-3">{history.items.map(item => <li className="break-words" key={item.version}>v{item.version} · {item.snapshot.work_date} · {item.snapshot.timezone ?? text.unknown} · {item.snapshot.category} · {item.snapshot.minutes} · {item.snapshot.note} · {item.reason}<p>{item.actor_id ?? text.unknown} · {item.recorded_at ?? text.unknown}</p><p>{item.snapshot.confirmed_at ? `${text.confirmed}: ${item.snapshot.confirmed_by} · ${item.snapshot.confirmed_at}` : text.legacy}</p></li>)}</ol><div className="flex flex-wrap gap-2"><Button type="button" variant="outline" disabled={!historyTrail.length} onClick={() => { setAfter(historyTrail.at(-1) ?? ""); setHistoryTrail(v => v.slice(0, -1)) }}>{text.previous}</Button><Button type="button" variant="outline" disabled={!history.has_more} onClick={() => { setHistoryTrail(v => [...v, after]); setAfter(history.next_cursor ?? "") }}>{text.next}</Button></div></>}</section>}
  </section>
}
