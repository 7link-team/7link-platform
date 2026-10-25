import { useEffect, useRef, useState } from "react"
import { Link, useSearchParams } from "react-router"
import { AutoForm, type FormConfig } from "@workspace/ui/components/auto-form"
import { Button } from "@workspace/ui/components/button"
import { useTranslations } from "@workspace/ui/i18n/intl-provider"
import { Alert } from "../../components/alert"
import { useAuth } from "../../components/auth"
import { agentLoops, type Loop, type LoopCreate, type LoopPatch, type LoopRun } from "./loops-api"

const periods = ["DAY", "WEEK", "MONTH", "QUARTER", "YEAR"] as const

export function AgentLoops({tenant, agent, canManage}: {tenant: string; agent: string; canManage: boolean}) {
  const t = useTranslations("iam")
  const {session} = useAuth()
  const [search, setSearch] = useSearchParams()
  const cursor = search.get("loop_cursor") || ""
  const selected = search.get("loop") || ""
  const [rows, setRows] = useState<Loop[]>([])
  const [next, setNext] = useState("")
  const [editing, setEditing] = useState<Loop | "new" | null>(null)
  const [revision, setRevision] = useState(0)
  const [busy, setBusy] = useState(false)
  const [error, setError] = useState("")
  const [loading, setLoading] = useState(true)
  const retry = useRef<{signature: string; key: string} | null>(null)
  const navigate = (key: string, value: string) => setSearch(previous => {const n = new URLSearchParams(previous); if (value) n.set(key, value); else n.delete(key); return n})
  useEffect(() => {
    const controller = new AbortController()
    void agentLoops.list(tenant, agent, cursor, controller.signal).then(page => {
      if (!controller.signal.aborted) {setRows(page.items || []); setNext(page.next_cursor || ""); setError(""); setLoading(false)}
    }, cause => {if (!controller.signal.aborted) {setRows([]); setError(String(cause.message || cause)); setLoading(false)}})
    return () => controller.abort()
  }, [tenant, agent, cursor, revision])
  const command = async (signature: string, perform: (key: string) => Promise<Loop>) => {
    const key = retry.current?.signature === signature ? retry.current.key : crypto.randomUUID()
    retry.current = {signature, key}
    setBusy(true); setError("")
    try {await perform(key); retry.current = null; setEditing(null); setRevision(v => v + 1)}
    catch (cause) {setError(cause instanceof Error ? cause.message : String(cause))}
    finally {setBusy(false)}
  }
  return <section className="min-w-0 space-y-4" aria-label={t("loopTitle")}>
    <div className="flex flex-wrap items-center justify-between gap-3"><h2 className="text-lg font-semibold">{t("loopTitle")}</h2><div className="flex gap-2"><Button type="button" variant="outline" disabled={busy} onClick={() => setRevision(v => v + 1)}>{t("refresh")}</Button>{session && session.subject !== agent && <Button type="button" disabled={busy} onClick={() => setEditing("new")}>{t("loopCreate")}</Button>}</div></div>
    <p className="text-sm text-muted-foreground">{t("loopHint")}</p>
    {error && <Alert>{error}</Alert>}
    {editing && <LoopEditor key={editing === "new" ? "new" : `${editing.id}:${editing.version}`} agent={agent} current={editing === "new" ? undefined : editing} busy={busy} onCancel={() => setEditing(null)} onSave={body => editing === "new" ? command(`create:${JSON.stringify(body)}`, key => agentLoops.create(tenant, body, key)) : command(`edit:${editing.id}:${editing.version}:${JSON.stringify(body)}`, key => {
      const patch: LoopPatch = {name: body.name, anchor: body.anchor, schedule_kind: body.schedule_kind, ...(body.schedule_kind === "INTERVAL" ? {interval_minutes: body.interval_minutes} : {period_kind: body.period_kind}), self_checks: body.self_checks || [], budget: body.budget, escalation: body.escalation, allow_overlap: body.allow_overlap, ...(editing.state === "DRAFT" && body.report_binding ? {report_binding: body.report_binding} : {})}
      return agentLoops.update(editing.id!, editing.version, patch, key)
    })}/>}
    {loading && <p role="status">{t("loading")}</p>}
    {!loading && !error && rows.length === 0 && <p>{t("agentEmpty")}</p>}
    <ul className="divide-y border-y">{rows.map(row => {
      const writable = !!session && session.subject !== agent && (canManage || row.owner_principal_id === session.subject)
      return <li key={row.id} className="space-y-3 py-4"><div className="flex flex-wrap items-center justify-between gap-3"><h3 className="font-medium">{row.name}</h3><span>{t(`loopState_${row.state}`)} · v{row.version}</span></div><p className="text-sm">{t(`loopKind_${row.kind}`)} · {row.schedule_kind === "INTERVAL" ? `${row.interval_minutes} ${t("loopMinutes")}` : t(`loopPeriod_${row.period_kind}`)} · {t("loopFailures")}: {row.consecutive_failures}</p><p className="break-all text-sm">{t("loopAnchor")}: {row.anchor.channel_id} / {row.anchor.thread_root_message_id}</p>
        {!!row.unavailable_capabilities?.length && <Alert>{t("loopUnavailable")}: {row.unavailable_capabilities.join(", ")}</Alert>}
        <div className="flex flex-wrap gap-2"><Button type="button" variant="outline" onClick={() => navigate("loop", selected === row.id ? "" : row.id!)}>{t("loopHistory")}</Button>{writable && row.state !== "ARCHIVED" && <><Button type="button" variant="outline" disabled={busy} onClick={() => setEditing(row)}>{t("edit")}</Button>{([...(row.state === "ACTIVE" ? ["PAUSED"] : ["ACTIVE"]), "ARCHIVED"] as const).map(state => <Button key={state} type="button" variant="outline" disabled={busy || state === "ACTIVE" && Boolean(row.unavailable_capabilities?.includes("ACTIVATION"))} onClick={() => {if (window.confirm(t("loopStateConfirm"))) void command(`state:${row.id}:${row.version}:${state}`, key => agentLoops.state(row.id!, row.version, state as "ACTIVE" | "PAUSED" | "ARCHIVED", key))}}>{t(`loopAction_${state}`)}</Button>)}</>}</div>
        {selected === row.id && <LoopHistory key={`${row.id}:${revision}`} tenant={tenant} id={row.id!}/>}
      </li>
    })}</ul>
    <div className="flex gap-2">{cursor && <Button type="button" variant="outline" onClick={() => navigate("loop_cursor", "")}>{t("loopFirst")}</Button>}{next && <Button type="button" variant="outline" onClick={() => navigate("loop_cursor", next)}>{t("nextPage")}</Button>}</div>
  </section>
}

export function LoopEditor({agent, current, busy, onSave, onCancel}: {agent: string; current?: Loop; busy: boolean; onSave: (body: LoopCreate) => Promise<void>; onCancel: () => void}) {
  const t = useTranslations("iam")
  const [checkCount, setCheckCount] = useState(current?.self_checks?.length || 0)
  const options = (values: readonly string[], prefix: string) => values.map(value => ({value, label: t(`${prefix}${value}`)}))
  const fields: FormConfig = {
    name: {name: t("name"), type: "text", required: true, config: {maxLength: 200}},
    kind: {name: t("loopKind"), type: "select", required: true, disabled: !!current, config: {options: options(["CHECK", "REPORT"], "loopKind_")}},
    project: {name: t("loopProject"), type: "text", disabled: !!current, config: {pattern: "[1-9][0-9]*", inputMode: "numeric", maxLength: 19}},
    channel: {name: t("loopChannel"), type: "text", required: true, config: {pattern: "[1-9][0-9]*", inputMode: "numeric", maxLength: 19}},
    thread: {name: t("loopThread"), type: "text", required: true, config: {pattern: "[1-9][0-9]*", inputMode: "numeric", maxLength: 19}},
    schedule: {name: t("loopSchedule"), type: "select", required: true, config: {options: options(["INTERVAL", "PERIOD"], "loopSchedule_")}},
    interval: {name: t("loopInterval"), type: "number", required: true, show: v => v?.schedule === "INTERVAL", config: {min: 15, max: 10080, step: 1}},
    period: {name: t("loopPeriod"), type: "select", required: true, show: v => v?.schedule === "PERIOD", config: {options: options(periods, "loopPeriod_")}},
    level: {name: t("loopReportLevel"), type: "select", required: true, disabled: !!current && current.state !== "DRAFT", show: v => v?.kind === "REPORT", config: {options: options(["MEMBER", "PROJECT"], "loopLevel_")}},
    report_period: {name: t("loopReportPeriod"), type: "select", required: true, disabled: !!current && current.state !== "DRAFT", show: v => v?.kind === "REPORT", config: {options: options(periods, "loopPeriod_")}},
    max_cost: {name: t("loopMaxCost"), type: "number", required: true, config: {min: 0, step: 1}},
    duration: {name: t("loopDuration"), type: "number", required: true, config: {min: 5, max: 240, step: 1}},
    monthly: {name: t("loopMonthly"), type: "number", config: {min: 0, step: 1}},
    failures: {name: t("loopFailureLimit"), type: "number", required: true, config: {min: 1, max: 10, step: 1}},
    budget_escalation: {name: t("loopBudgetEscalation"), type: "checkbox"},
    overlap: {name: t("loopOverlap"), type: "checkbox"},
  }
  for (let i = 0; i < checkCount; i++) {
    fields[`check_key_${i}`] = {name: `${t("loopCheckKey")} ${i + 1}`, type: "text", required: true, config: {maxLength: 32}}
    fields[`check_text_${i}`] = {name: `${t("loopCheckDescription")} ${i + 1}`, type: "text", required: true, config: {maxLength: 500}}
    fields[`check_required_${i}`] = {name: `${t("loopCheckRequired")} ${i + 1}`, type: "checkbox"}
  }
  const initial: Record<string, unknown> = {name: current?.name || "", kind: current?.kind || "CHECK", project: current?.project_id || "", channel: current?.anchor.channel_id || "", thread: current?.anchor.thread_root_message_id || "", schedule: current?.schedule_kind || "INTERVAL", interval: current?.interval_minutes || 15, period: current?.period_kind || "DAY", level: current?.report_binding?.level || "MEMBER", report_period: current?.report_binding?.period_kind || "DAY", max_cost: current?.budget.max_cost_micros_per_run || 0, duration: current?.budget.max_duration_minutes || 30, monthly: current?.budget.monthly_cost_cap_micros ?? "", failures: current?.escalation.consecutive_failures_to_pause || 3, budget_escalation: current?.escalation.on_budget_exceeded || false, overlap: current?.allow_overlap || false, ...Object.fromEntries((current?.self_checks || []).flatMap((c, i) => [[`check_key_${i}`, c.key], [`check_text_${i}`, c.description], [`check_required_${i}`, c.required]]))}
  return <div className="space-y-3 rounded border p-4"><h3 className="font-medium">{t(current ? "loopEdit" : "loopCreate")}</h3><p className="text-sm text-muted-foreground">{t("loopDraftHint")}</p><AutoForm mode="edit" fields={fields} initialData={initial} onSubmit={async v => {
    const body: LoopCreate = {activate: false, agent_id: agent, owner_principal_id: current?.owner_principal_id || null, name: String(v.name), kind: v.kind as LoopCreate["kind"], ...(v.project ? {project_id: String(v.project)} : {}), anchor: {channel_id: String(v.channel), thread_root_message_id: String(v.thread)}, schedule_kind: v.schedule as LoopCreate["schedule_kind"], ...(v.schedule === "INTERVAL" ? {interval_minutes: Number(v.interval)} : {period_kind: String(v.period)}), ...(v.kind === "REPORT" ? {report_binding: {level: v.level as "MEMBER" | "PROJECT", period_kind: v.report_period as typeof periods[number]}} : {}), self_checks: Array.from({length: checkCount}, (_, i) => ({key: String(v[`check_key_${i}`]), description: String(v[`check_text_${i}`]), required: Boolean(v[`check_required_${i}`])})), budget: {max_cost_micros_per_run: Number(v.max_cost), max_duration_minutes: Number(v.duration), ...(v.monthly !== "" && v.monthly != null ? {monthly_cost_cap_micros: Number(v.monthly)} : {})}, escalation: {on_required_check_failed: true, on_budget_exceeded: Boolean(v.budget_escalation), consecutive_failures_to_pause: Number(v.failures)}, output_format: v.kind === "REPORT" ? "REPORT_DRAFT" : "CHECK_RESULT", allow_overlap: Boolean(v.overlap)}
    await onSave(body)
  }}><div className="flex flex-wrap gap-2"><Button type="button" variant="outline" disabled={busy || checkCount >= 20} onClick={() => setCheckCount(v => v + 1)}>{t("loopAddCheck")}</Button><Button type="button" variant="outline" disabled={busy || checkCount === 0} onClick={() => setCheckCount(v => v - 1)}>{t("loopRemoveCheck")}</Button><Button type="submit" disabled={busy}>{t("save")}</Button><Button type="button" variant="outline" disabled={busy} onClick={onCancel}>{t("cancel")}</Button></div></AutoForm></div>
}

export function LoopRunFacts({tenant, run}: {tenant: string; run: LoopRun}) {
  const t = useTranslations("iam")
  return <article className="min-w-0 space-y-2 border-t py-3"><h4 className="font-medium">#{run.fire_no} · {t(`loopRun_${run.state}`)}</h4><p className="text-sm">{t("loopScheduled")}: {new Date(run.scheduled_for).toLocaleString()}</p>{run.completed_at && <p className="text-sm">{t("loopCompleted")}: {new Date(run.completed_at).toLocaleString()}</p>}{run.report_id && <Link className="inline-flex min-h-11 items-center underline" to={`/admin/${encodeURIComponent(tenant)}/reports?record_id=${encodeURIComponent(run.report_id)}`}>{t("loopReport")}: {run.report_id}</Link>}{run.note && <p className="whitespace-pre-wrap break-words text-sm">{run.note}</p>}{run.escalation_reason && <p className="text-sm">{run.escalation_reason}</p>}<p className="text-sm">{t("loopCost")}: {run.cost_micros === undefined ? t("loopUnknown") : run.cost_micros}</p>{!!run.unavailable_capabilities?.length && <p className="text-sm text-muted-foreground">{t("loopUnavailable")}: {run.unavailable_capabilities.join(", ")}</p>}<details><summary className="min-h-11 cursor-pointer py-2">{t("loopEvidence")}</summary><pre className="max-h-64 overflow-auto whitespace-pre-wrap break-words text-xs">{JSON.stringify({self_checks: run.self_check_results, check_result: run.check_result}, null, 2)}</pre></details></article>
}

function LoopHistory({tenant, id}: {tenant: string; id: string}) {
  const t = useTranslations("iam")
  const [rows, setRows] = useState<LoopRun[]>([])
  const [cursor, setCursor] = useState("")
  const [next, setNext] = useState("")
  const [error, setError] = useState("")
  useEffect(() => {
    const controller = new AbortController()
    void agentLoops.runs(id, cursor, controller.signal).then(page => {if (!controller.signal.aborted) {setRows(page.items || []); setNext(page.next_cursor || ""); setError("")}}, cause => {if (!controller.signal.aborted) {setRows([]); setError(String(cause.message || cause))}})
    return () => controller.abort()
  }, [id, cursor])
  return <section aria-label={t("loopHistory")} className="space-y-2">{error && <Alert>{error}</Alert>}{!error && rows.length === 0 && <p>{t("agentEmpty")}</p>}{rows.map(run => <LoopRunFacts key={run.id} tenant={tenant} run={run}/>)}{next && <Button type="button" variant="outline" onClick={() => setCursor(next)}>{t("nextPage")}</Button>}{cursor && <Button type="button" variant="outline" onClick={() => setCursor("")}>{t("loopFirst")}</Button>}</section>
}
