import { useEffect, useRef, useState } from "react"
import { useSearchParams } from "react-router"
import { Button } from "@workspace/ui/components/button"
import { Input } from "@workspace/ui/components/input"
import { Textarea } from "@workspace/ui/components/textarea"
import { useLocale, useTranslations } from "@workspace/ui/i18n/intl-provider"
import { iamApi, type WorkItem, type WorkItemDetail } from "../../lib/iam-api"
import { useAllowed } from "../../lib/permissions"
import { Alert } from "../../components/alert"
import { calendarRange, localDateTime, overlapsDay } from "./schedule-dates"

type CalendarRow = { work: WorkItem; assignee_kind: string; assignee_id?: string | null }
export function WorkCalendar({ entityID, onOpen, revision }: { entityID: string; onOpen: (work: WorkItem) => void; revision: number }) {
  const t = useTranslations("iam")
  const locale = useLocale()
  const [search, setSearch] = useSearchParams()
  const mode = ["month", "week", "agenda"].includes(search.get("cal_mode") ?? "") ? search.get("cal_mode")! : "month"
  const day = search.get("cal_day") ?? localDateTime(new Date().toISOString()).slice(0, 10)
  const kind = search.get("cal_kind") ?? ""
  const assignee = search.get("cal_assignee") ?? ""
  const unscheduled = search.get("cal_unscheduled") === "true"
  const rawOffset = Number(search.get("cal_offset") ?? 0)
  const offset = Number.isSafeInteger(rawOffset) && rawOffset >= 0 ? rawOffset : 0
  const range = calendarRange(day, mode)
  const [rows, setRows] = useState<CalendarRow[]>([])
  const [loading, setLoading] = useState(true)
  const [error, setError] = useState("")
  const update = (key: string, value: string) => setSearch(previous => { const next = new URLSearchParams(previous); next.set(key, value); if (key !== "cal_offset") next.delete("cal_offset"); return next })
  const query = new URLSearchParams({ start: range.start.toISOString(), end: range.end.toISOString(), unscheduled: String(unscheduled), offset: String(offset), limit: "200" })
  if (kind) query.set("assignee_kind", kind)
  if (/^[1-9][0-9]*$/.test(assignee)) query.set("assignee_id", assignee)
  const url = `/entities/${encodeURIComponent(entityID)}/work-items/calendar?${query}`
  useEffect(() => {
    const c = new AbortController()
    queueMicrotask(() => { if (!c.signal.aborted) { setLoading(true); setError("") } })
    void iamApi.deliveryResultRequest<CalendarRow[]>({ method: "GET", url }, { signal: c.signal })
      .then(v => { if (!c.signal.aborted) setRows(v) })
      .catch(e => { if (!c.signal.aborted) setError(e instanceof Error ? e.message : String(e)) })
      .finally(() => { if (!c.signal.aborted) setLoading(false) })
    return () => c.abort()
  }, [url, revision])
  const task = (row: CalendarRow) => <Button key={row.work.id} variant="ghost" className="h-auto min-h-11 w-full justify-start whitespace-normal text-left" onClick={() => onOpen(row.work)}>#{row.work.number} {row.work.title}<span className="sr-only">{row.assignee_kind} {row.assignee_id}</span></Button>
  const agenda = <ul>{rows.map(row => <li key={row.work.id} className="border-b py-2">{task(row)}<p className="px-4 text-sm text-muted-foreground">{row.work.scheduled_start ? `${new Date(row.work.scheduled_start).toLocaleString(locale)} – ${new Date(row.work.scheduled_end!).toLocaleString(locale)}` : t("scheduleUnscheduled")}</p></li>)}</ul>
  return <section className="space-y-4" aria-label={t("workView_calendar")}>
    <div className="flex flex-wrap items-end gap-3">
      <label>{t("scheduleDay")}<Input type="date" value={day} onChange={e => update("cal_day", e.target.value)} /></label>
      <label>{t("scheduleMode")}<select className="block min-h-11 border bg-background p-2" value={mode} onChange={e => update("cal_mode", e.target.value)}>{["month", "week", "agenda"].map(v => <option key={v} value={v}>{t(`schedule_${v}`)}</option>)}</select></label>
      <label>{t("scheduleAssigneeKind")}<select className="block min-h-11 border bg-background p-2" value={kind} onChange={e => update("cal_kind", e.target.value)}>{["", "HUMAN", "AGENT"].map(v => <option key={v} value={v}>{t(`scheduleKind_${v || "ALL"}`)}</option>)}</select></label>
      <label>{t("scheduleAssignee")}<Input inputMode="numeric" value={assignee} onChange={e => update("cal_assignee", e.target.value)} /></label>
      <label className="flex min-h-11 items-center gap-2"><input type="checkbox" checked={unscheduled} onChange={e => update("cal_unscheduled", String(e.target.checked))} />{t("scheduleUnscheduled")}</label>
    </div>
    <p className="text-sm text-muted-foreground">{t("scheduleTimezone")}: {Intl.DateTimeFormat().resolvedOptions().timeZone}</p>
    {error && <Alert>{error}</Alert>}
    {loading ? <p role="status">{t("loading")}</p> : rows.length === 0 ? <p>{t("scheduleEmpty")}</p> : <>
      {mode === "agenda" || unscheduled ? agenda : <><div className="hidden grid-cols-7 gap-px border bg-border md:grid">{range.days.map((date, index) => <div key={date.toISOString()} style={index === 0 ? { gridColumnStart: ((date.getDay() + 6) % 7) + 1 } : undefined} className="min-h-28 min-w-0 bg-background p-2"><h3 className="text-sm">{date.toLocaleDateString(locale, { weekday: "short", day: "numeric" })}</h3>{rows.filter(row => overlapsDay(row.work.scheduled_start, row.work.scheduled_end, date)).map(task)}</div>)}</div><div className="md:hidden">{agenda}</div></>}
      <div className="flex items-center gap-3"><Button disabled={offset === 0} variant="outline" onClick={() => update("cal_offset", String(Math.max(0, offset - 200)))}>{t("schedulePrevious")}</Button><span>{offset + 1}–{offset + rows.length}</span><Button disabled={rows.length < 200} variant="outline" onClick={() => update("cal_offset", String(offset + 200))}>{t("scheduleNext")}</Button></div>
    </>}
  </section>
}

export function WorkSchedule({ entityID, detail, onUpdated }: { entityID: string; detail: WorkItemDetail; onUpdated: (work: WorkItemDetail) => void }) {
  const t = useTranslations("iam")
  const allowed = useAllowed()
  const [start, setStart] = useState(localDateTime(detail.scheduled_start))
  const [end, setEnd] = useState(localDateTime(detail.scheduled_end))
  const [reason, setReason] = useState("")
  const [busy, setBusy] = useState(false)
  const [error, setError] = useState("")
  const attempt = useRef<{ body: string; key: string } | null>(null)
  const pending = useRef(false)
  const zone = Intl.DateTimeFormat().resolvedOptions().timeZone
  const save = async (clear: boolean) => {
    if (pending.current || reason.trim().length < 5) return
    pending.current = true; setBusy(true); setError("")
    try {
      const body = JSON.stringify({ version: detail.version, start: clear ? null : new Date(start).toISOString(), end: clear ? null : new Date(end).toISOString(), timezone: zone, reason: reason.trim() })
      if (attempt.current?.body !== body) attempt.current = { body, key: crypto.randomUUID() }
      await iamApi.deliveryResultRequest({ method: "PUT", url: `/entities/${encodeURIComponent(entityID)}/work-items/${encodeURIComponent(detail.id!)}/schedule` }, { body, headers: { "Idempotency-Key": attempt.current.key } })
      attempt.current = null; setReason("")
      if (clear) { setStart(""); setEnd("") }
      onUpdated(await iamApi.workItem(entityID, detail.id!))
    } catch (e) { setError(e instanceof Error ? e.message : String(e)) }
    finally { pending.current = false; setBusy(false) }
  }
  if (!allowed("work_item_write") || ["ACCEPTED", "CANCELLED"].includes(detail.state)) return null
  return <section className="space-y-3 border-t pt-4">
    <h3 className="font-medium">{t("scheduleEdit")}</h3>
    <p className="text-sm">{t("scheduleTimezone")}: {zone}</p>
    {error && <Alert>{error}</Alert>}
    <form className="grid gap-3" onSubmit={e => { e.preventDefault(); void save(false) }}>
      <label>{t("scheduleStart")}<Input required type="datetime-local" value={start} disabled={busy} onChange={e => setStart(e.target.value)} /></label>
      <label>{t("scheduleEnd")}<Input required type="datetime-local" value={end} min={start} disabled={busy} onChange={e => setEnd(e.target.value)} /></label>
      <label>{t("scheduleReason")}<Textarea required minLength={5} maxLength={2000} value={reason} disabled={busy} onChange={e => setReason(e.target.value)} /></label>
      <div className="flex flex-wrap gap-3"><Button type="submit" disabled={busy || !start || !end || reason.trim().length < 5}>{t("scheduleSave")}</Button><Button type="button" variant="outline" disabled={busy || !detail.scheduled_start || reason.trim().length < 5} onClick={() => void save(true)}>{t("scheduleClear")}</Button></div>
    </form>
  </section>
}
