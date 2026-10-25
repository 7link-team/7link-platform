import { useEffect, useRef, useState } from "react"
import { Link, useNavigate, useParams, useSearchParams } from "react-router"
import { Eye } from "lucide-react"
import { AutoForm, type FormConfig, type UseAutoFormReturn } from "@workspace/ui/components/auto-form"
import { AutoTable } from "@workspace/ui/components/auto-table"
import { Button } from "@workspace/ui/components/button"
import { Input } from "@workspace/ui/components/input"
import { useLocale, useTranslations } from "@workspace/ui/i18n/intl-provider"
import { PageHeader } from "../../components/page-header"
import { useAuth } from "../../components/auth"
import { useAllowed } from "../../lib/permissions"
import { planningApi, planPath, planItems, isSystemAggregateDraft, type GoalInput, type PlanItem, type PlanningCheck, type PlanningFacts, type PlanningKind, type PlanningRecord, type PlanningWrite, type ReportInput, type ReportScope } from "./api"
import { toDateTimeLocal } from "../../lib/membership-presentation"
import { planningCarryLabels, planningActionLabels, planningLabels, type PlanningLabels } from "./labels"
import {reportLabels} from "../reports/labels"
import {ReportChecks} from "./report-checks"
import {reportSectionLabels, retrospectiveValues} from "./report-sections"
import {GoalTree} from "./tree"
import {goalViewLabels} from "./tree-model"
import {GoalAlignmentPanel} from "./alignment"

function itemFields(labels: PlanningLabels, count: number): FormConfig {
 const fields: FormConfig = {}
 for (let i = 0; i < count; i++) {
  fields[`item_heading_${i}`] = {name: `${labels.item} ${i + 1}`, type: "title"}
  for (const [name, label] of [["title", labels.title], ["goal", labels.goal], ["kr", labels.kr], ["work", labels.work]] as const) fields[`item_${name}_${i}`] = {name: label, type: "text", required: name !== "work", config: {maxLength: name === "title" ? 200 : 64}}
  fields[`item_weight_${i}`] = {name: labels.weight, type: "number", value: 1, config: {min: 0, max: 10, step: 0.1}}
  fields[`item_notes_${i}`] = {name: labels.notes, type: "text", config: {maxLength: 2000}}
  fields[`item_hours_${i}`] = {name: labels.estimateHours, type: "number", config: {min: 0, step: 0.1}}
  fields[`item_cost_${i}`] = {name: labels.estimateCost, type: "text", config: {maxLength: 19}}
  fields[`item_due_${i}`] = {name: labels.itemDue, type: "datetime"}
  fields[`item_completed_${i}`] = {name: labels.itemCompleted, type: "datetime", show: values => !values?.[`item_work_${i}`]}
  fields[`item_evidence_${i}`] = {name: labels.completionEvidence, type: "text", show: values => !values?.[`item_work_${i}`], config: {maxLength: 2000}}
  fields[`item_category_${i}`] = {name: labels.category, type: "select", value: "OTHER", show: values => !values?.[`item_work_${i}`], config: {options: [["LEARNING", labels.learning], ["OPS", labels.ops], ["REVIEW", labels.review], ["SUPPORT", labels.support], ["OTHER", labels.other]].map(([value, label]) => ({value: value!, label}))}}
 }
 return fields
}
function itemsInitial(items: PlanItem[]): Record<string, unknown> {return Object.fromEntries(items.flatMap((item, i) => [[`item_notes_${i}`, item.notes ?? ""], [`item_hours_${i}`, item.estimate_hours ?? ""], [`item_cost_${i}`, item.estimate_cost_micros ?? ""], [`item_title_${i}`, item.title], [`item_goal_${i}`, item.goal_id], [`item_kr_${i}`, item.kr], [`item_work_${i}`, item.work_item_id ?? ""], [`item_category_${i}`, item.category ?? "OTHER"], [`item_weight_${i}`, item.weight ?? 1], [`item_due_${i}`, toDateTimeLocal(item.due_at)], [`item_completed_${i}`, toDateTimeLocal(item.completed_at)], [`item_evidence_${i}`, item.completion_evidence ?? ""]]))}

function Facts({facts, labels}: {facts?: PlanningFacts; labels: PlanningLabels}) {
 return <section className="space-y-2 rounded-md border p-4"><h3 className="font-medium">{labels.facts}</h3>{!facts ? <p>{labels.noFacts}</p> : <><p className="text-sm">{labels.resultCount}: {facts.result_count} · {labels.handoffCount}: {facts.handoff_count}</p>{facts.execution_cost_micros !== undefined && <p className="text-sm">{labels.settledCost}: {facts.execution_cost_micros}</p>}{facts.missing_sources.length > 0 && <p className="text-sm text-muted-foreground">{labels.missing}: {facts.missing_sources.join(", ")}</p>}<ul className="space-y-1 text-sm">{facts.work.map(work => <li key={work.id}>{work.title} · {work.state}{work.accepted_at ? ` · ${work.accepted_at}` : ""}</li>)}</ul></>}</section>
}

export function PlanConfirmationNotice({record, labels}: {record: PlanningRecord; labels: ReturnType<typeof planningLabels>}) {
 return record.kind === "plan" && record.state === "SUBMITTED" && record.unconfirmed ? <p role="status" className="text-sm">{labels.unconfirmed}</p> : null
}

export function PlanningPage({kind, mine = false}: {kind: PlanningKind; mine?: boolean}) {
 const {tenantID = "", projectID = "", entityID = "", planID = ""} = useParams()
 const [search] = useSearchParams()
 return <PlanningContent key={`${tenantID}:${projectID || entityID}:${kind}:${mine}:${planID}:${search}`} kind={kind} mine={mine}/>
}
function PlanningContent({kind, mine}: {kind: PlanningKind; mine: boolean}) {
 const {tenantID = "", projectID = "", entityID = "", planID = ""} = useParams()
 const navigate = useNavigate()
 const routeProject = projectID || entityID
 const [search, setSearch] = useSearchParams()
 const initialScope: ReportScope = search.get("scope") === "TEAM" ? "TEAM" : search.get("scope") === "PROJECT" ? "PROJECT" : "MEMBER"
 const {session} = useAuth()
 const locale = useLocale()
 const labels = planningLabels(locale)
 const actionLabels = planningActionLabels(locale)
 const allowed = useAllowed()
 const [filters, setFilters] = useState({project: initialScope === "TEAM" ? "" : routeProject, principal: mine || initialScope !== "MEMBER" ? "" : search.get("principal_id") ?? "", scope: initialScope})
 const [cursor, setCursor] = useState("")
 const [next, setNext] = useState("")
 const [rows, setRows] = useState<PlanningRecord[]>([])
 const [selected, setSelected] = useState<PlanningRecord | null>(null)
 const [creating, setCreating] = useState(false)
 const [error, setError] = useState("")
 const [notice, setNotice] = useState("")
 const [loading, setLoading] = useState(true)
 const [busy, setBusy] = useState(false)
 const [refresh, setRefresh] = useState(0)
 const [reason, setReason] = useState("")
 const [check, setCheck] = useState<PlanningCheck | null>(null)
 const [count, setCount] = useState(1)
 const generation = useRef(0)
 const pending = useRef<{body: string; key: string} | null>(null)
 const title = mine ? labels.myPlans : labels[kind]
 const permission = kind.slice(0, -1)
 useEffect(() => {const abort = new AbortController();generation.current++;queueMicrotask(() => {if (!abort.signal.aborted) {setLoading(true);setError("");setSelected(null);setCheck(null)}});planningApi.list(tenantID, kind, {project: filters.project, principal: mine || filters.scope !== "MEMBER" ? "" : filters.principal, cursor, ...(kind === "reports" ? {scope: filters.scope} : {})}, abort.signal).then(page => {if (!abort.signal.aborted) {setRows(page.items);setNext(page.next_cursor ?? "")}}).catch(error => {if (!abort.signal.aborted) setError(error instanceof Error ? error.message : String(error))}).finally(() => {if (!abort.signal.aborted) setLoading(false)});return () => abort.abort()}, [tenantID, kind, filters.project, filters.principal, cursor, refresh, mine, filters.scope])
 const recordID = kind === "plans" && planID ? planID : search.get("record_id") ?? ""
 useEffect(() => {
  if (!/^[1-9][0-9]{0,18}$/.test(recordID)) return
  const abort = new AbortController()
  planningApi.get(tenantID, kind, recordID, abort.signal).then(record => {if (!abort.signal.aborted) {setSelected(record);setCreating(false)}}).catch(cause => {if (!abort.signal.aborted) setError(cause instanceof Error ? cause.message : String(cause))})
  return () => abort.abort()
 }, [tenantID, kind, recordID, refresh])
 const reportLink = (id: string) => {const next = new URLSearchParams(search);next.set("record_id", id);return `?${next}`}
 const run = async (payload: unknown, action: (key: string) => Promise<PlanningRecord>) => {if (busy) return;const activeGeneration = generation.current;const body = JSON.stringify(payload);if (pending.current?.body !== body) pending.current = {body, key: crypto.randomUUID()};setBusy(true);setError("");setNotice("");try {const record = await action(pending.current.key);if (activeGeneration !== generation.current) return;pending.current = null;setSelected(record);setCreating(false);setCheck(null);setNotice(labels.saved);setRows(current => current.some(r => r.id === record.id) ? current.map(r => r.id === record.id ? record : r) : [record, ...current])}catch (error) {setError(error instanceof Error ? error.message : String(error))}finally {setBusy(false)}}
 const open = (record: PlanningRecord) => {setSelected(null);setCreating(false);setCheck(null);if (kind === "plans") {const next = new URLSearchParams(search);next.delete("record_id");navigate({pathname: planPath(tenantID, routeProject, record.id), search: next.toString()});return}setSearch(previous => {const next = new URLSearchParams(previous);next.set("record_id", record.id);return next})}
 const create = async (values: Record<string, unknown>) => {
  if (kind === "reports" && values.scope !== "MEMBER") {
   const scope = values.scope === "TEAM" ? "TEAM" as const : "PROJECT" as const
   const aggregate = {scope, project_id: scope === "TEAM" ? "0" : String(values.project_id || routeProject), period_kind: String(values.period_kind), period_start: String(values.period_start)}
   await run(aggregate, key => planningApi.aggregate(tenantID, aggregate, key));return
  }
  const body: PlanningWrite = {project_id: String(values.project_id || "0"), principal_id: String(values.principal_id), period_kind: String(values.period_kind), period_start: String(values.period_start)}
  if (kind === "goals") body.goal = {level: String(values.level), title: String(values.title), weight: Number(values.weight ?? 1), ...(values.parent_goal_id ? {parent_goal_id: String(values.parent_goal_id), parent_kr: String(values.parent_kr ?? "")} : {}), key_results: Array.from({length: count}, (_, i) => ({key: String(values[`kr_key_${i}`] ?? ""), title: String(values[`kr_title_${i}`] ?? ""), weight: Number(values[`kr_weight_${i}`] ?? 1), source: values[`kr_source_${i}`] === "AUTO" ? "AUTO" : "MANUAL", ...(values[`kr_source_${i}`] === "AUTO" ? {metric: "ACCEPTED_WORK_ITEMS" as const} : {}), baseline: Number(values[`kr_baseline_${i}`] ?? 0), target: Number(values[`kr_target_${i}`] ?? 0), current: Number(values[`kr_current_${i}`] ?? 0), ...(values[`kr_evidence_${i}`] ? {evidence: String(values[`kr_evidence_${i}`])} : {})}))}
  if (kind === "plans") body.plan = {items: planItems(values, count)}
  await run({kind, body}, key => planningApi.create(tenantID, kind, body, key))
 }
 const fields: FormConfig = {project_id: {name: labels.project, type: "text", show: values => kind !== "reports" || values?.scope !== "TEAM", value: routeProject || "0", required: kind === "reports", config: {inputMode: "numeric"}}, principal_id: {name: labels.principal, type: "text", show: values => kind !== "reports" || values?.scope === "MEMBER", value: /^\d+$/.test(session?.subject ?? "") ? session?.subject : "", required: true, config: {inputMode: "numeric"}}, period_kind: {name: labels.period, type: "select", value: kind === "goals" ? "QUARTER" : "DAY", required: true, config: {options: [["DAY", labels.day], ["WEEK", labels.week], ["MONTH", labels.month], ["QUARTER", labels.quarter], ["YEAR", labels.year]].filter(([value]) => kind !== "goals" || ["MONTH", "QUARTER", "YEAR"].includes(value!)).map(([value, label]) => ({value: value!, label}))}}, period_start: {name: labels.start, type: "date", required: true}}
 if (kind === "reports") fields.scope = {name: labels.level, type: "select", value: filters.scope, config: {options: [{value: "MEMBER", label: labels.member}, {value: "PROJECT", label: labels.projectLevel}, {value: "TEAM", label: labels.team}]}}
 if (kind === "goals") {
  Object.assign(fields, {weight: {name: labels.weight, type: "number", value: 1, config: {min: 0, max: 10, step: 0.1}}, level: {name: labels.level, type: "select", value: routeProject ? "PROJECT" : "TEAM", required: true, config: {options: [{value: "TEAM", label: labels.team}, {value: "PROJECT", label: labels.projectLevel}, {value: "MEMBER", label: labels.member}]}}, title: {name: labels.title, type: "text", required: true}, parent_goal_id: {name: labels.parentGoal, type: "text"}, parent_kr: {name: labels.parentKR, type: "text"}} satisfies FormConfig)
  for (let i = 0; i < count; i++) {fields[`kr_heading_${i}`] = {name: `${labels.krTitle} ${i + 1}`, type: "title"};fields[`kr_key_${i}`] = {name: labels.krKey, type: "text", required: true};fields[`kr_title_${i}`] = {name: labels.krTitle, type: "text", required: true};fields[`kr_source_${i}`] = {name: labels.source, type: "select", value: "MANUAL", config: {options: [{value: "MANUAL", label: labels.manual}, {value: "AUTO", label: labels.automatic}]}};for (const name of ["baseline", "target", "current"] as const) fields[`kr_${name}_${i}`] = {name: labels[name], type: "number", value: 0, required: true};fields[`kr_weight_${i}`] = {name: labels.weight, type: "number", value: 1, config: {min: 0, max: 10, step: 0.1}};fields[`kr_evidence_${i}`] = {name: labels.evidence, type: "text", required: false}}
 } else if (kind === "plans") Object.assign(fields, itemFields(labels, count))
 return <div className="flex min-h-0 min-w-0 flex-1 flex-col gap-3 overflow-auto"><PageHeader title={title} action={(kind === "goals" ? allowed("goal_manage") || allowed("goal_member_manage") : allowed(`${permission}_submit`)) && <Button disabled={busy} onClick={() => {setCreating(!creating);setSelected(null);setCount(1)}}>{kind === "reports" ? labels.generate : labels.create}</Button>}/>{kind === "reports" && <p className="text-sm text-muted-foreground">{labels.notice}</p>}{error && <p role="alert" className="rounded-md border p-3 text-sm">{error} <Button variant="outline" size="sm" onClick={() => setRefresh(refresh + 1)}>{labels.retry}</Button></p>}{notice && <p role="status">{notice}</p>}
 {creating && <section className="space-y-3 rounded-md border p-4"><p className="text-sm text-muted-foreground">{labels.periodHint}</p><AutoForm mode="edit" fields={fields} onSubmit={create}><div className="flex flex-wrap gap-2">{kind !== "reports" && <><Button type="button" variant="outline" disabled={count >= (kind === "goals" ? 5 : 200)} onClick={() => setCount(count + 1)}>{kind === "goals" ? labels.addKR : labels.addItem}</Button><Button type="button" variant="outline" disabled={count <= 1} onClick={() => setCount(count - 1)}>{labels.remove}</Button></>}<Button type="submit" disabled={busy}>{kind === "reports" ? labels.generate : labels.create}</Button></div></AutoForm></section>}
 {kind === "goals" && <div className="flex gap-2">{(["tree","list"] as const).map(view=><Button key={view} variant={(search.get("goal_view")??"tree")===view?"default":"outline"} onClick={()=>setSearch(previous=>{const next=new URLSearchParams(previous);next.set("goal_view",view);return next})}>{goalViewLabels(locale)[view]}</Button>)}</div>}
 {kind === "goals" && search.get("goal_view")!=="list" ? <GoalTree open={open}/> : <>
 <AutoForm mode="edit" fields={{project: {name: labels.filterProject, type: "text", value: routeProject}, ...(!mine ? {scope: {name: labels.level, type: "select", show: kind === "reports", config: {options: [{value: "MEMBER", label: labels.member}, {value: "PROJECT", label: labels.projectLevel}, {value: "TEAM", label: labels.team}]}}, principal: {name: labels.filterPrincipal, type: "text" as const}} : {})}} onSubmit={values => {setFilters({project: values.scope === "TEAM" ? "" : String(values.project ?? routeProject), principal: mine || values.scope !== "MEMBER" ? "" : String(values.principal ?? ""), scope: values.scope === "PROJECT" ? "PROJECT" : values.scope === "TEAM" ? "TEAM" : "MEMBER"});setCursor("")}} className="md:grid-cols-3"><Button type="submit" disabled={loading}>{labels.search}</Button></AutoForm>
 <AutoTable data={loading ? [] : rows} loading={loading} rowKey="id" pagination={false} emptyContent={labels.empty} columns={[{field: "period_start", name: labels.start}, {field: "period_kind", name: labels.period}, {field: "state", name: labels.state}, {field: "project_id", name: labels.project}, {field: "principal_id", name: labels.principal}, {field: "version", name: labels.version}]} actions={[{name: labels.view, icon: Eye, onclick: row => {void open(row)}}]} onRefresh={() => setRefresh(refresh + 1)}/><div className="flex gap-2"><Button variant="outline" disabled={loading || !cursor} onClick={() => setCursor("")}>{labels.first}</Button><Button variant="outline" disabled={loading || !next} onClick={() => setCursor(next)}>{labels.next}</Button></div>
 </>}
 {selected && <section className="space-y-4 rounded-md border p-4"><PlanConfirmationNotice record={selected} labels={labels}/><div className="flex items-center justify-between"><h2 className="font-medium">{selected.period_start} · {selected.state}</h2><Button variant="outline" onClick={() => {setSelected(null);const next = new URLSearchParams(search);next.delete("record_id");if (kind === "plans" && planID) navigate({pathname: planPath(tenantID, routeProject), search: next.toString()});else setSearch(next)}}>{labels.close}</Button></div><p className="break-all font-mono text-xs">{labels.hash}: {selected.content_hash}</p>{selected.due_at && <p className="text-sm">{actionLabels.due}: {new Date(selected.due_at).toLocaleString(locale)}{selected.submitted_late ? ` ? ${actionLabels.late}` : ""}</p>}{kind === "goals" ? <GoalDetails record={selected} labels={labels}/> : kind === "plans" ? <ItemList items={(selected.content as {items: PlanItem[]}).items} labels={labels}/> : <><Facts facts={check?.facts ?? selected.facts} labels={labels}/>{(selected.content as ReportInput).children?.length ? <ul className="break-all font-mono text-xs">{(selected.content as ReportInput).children!.map(child => <li key={child.id}><Link className="underline" to={reportLink(child.id)}>{actionLabels.children}: {child.id}</Link> ? {child.content_hash}</li>)}</ul> : null}{isSystemAggregateDraft(selected) && <p className="text-sm text-muted-foreground">{labels.adoption}</p>}<ReportEditor key={`${selected.id}:${selected.version}`} record={selected} labels={labels} readonly={busy || ((selected.author_id ?? selected.principal_id) !== session?.subject && !(isSystemAggregateDraft(selected) && /^[1-9][0-9]*$/.test(session?.subject ?? ""))) || !allowed("report_submit") || !["DRAFT", "RETURNED", "BLOCKED", "READY", "OVERDUE"].includes(selected.state)} save={content => run({record: selected.id, version: selected.version, content}, key => planningApi.editReport(tenantID, selected, content, key))}/><Button variant="outline" disabled={busy} onClick={async () => {setBusy(true);setError("");try {setCheck(await planningApi.reconcile(tenantID, selected.id))}catch (error) {setError(error instanceof Error ? error.message : String(error))}finally {setBusy(false)}}}>{labels.reconcile}</Button>{check && <ReportChecks check={check} labels={labels} tenant={tenantID} project={selected.project_id}/>}</>}
 {kind === "goals" && (allowed("goal_manage") || allowed("goal_member_manage")) && <GoalControls key={`${selected.id}:${selected.version}`} record={selected} labels={labels} busy={busy} save={change => run({id: selected.id, version: selected.version, ...change}, key => planningApi.changeGoal(tenantID, selected, change, key))}/>}
 {kind === "goals" && <GoalAlignmentPanel key={`alignment:${selected.id}:${selected.version}`} record={selected} busy={busy} save={change=>run({id:selected.id,version:selected.version,...change},key=>planningApi.changeGoal(tenantID,selected,change,key))}/>}
 {kind === "plans" && selected.principal_id === session?.subject && allowed("plan_submit") && <PlanEditor key={`${selected.id}:${selected.version}`} record={selected} labels={labels} busy={busy} save={items => run({id: selected.id, version: selected.version, items}, key => planningApi.editPlan(tenantID, selected, items, key))}/>}
 {kind === "reports" && <ReportReopenControl record={selected} subject={session?.subject} allowed={allowed("report_confirm")} busy={busy} reason={reason} setReason={setReason} labels={labels} reopen={() => run({id: selected.id, version: selected.version, hash: selected.content_hash, action: "REOPEN", reason}, key => planningApi.decide(tenantID, "reports", selected, "REOPEN", reason, key))}/>}
 {kind !== "goals" && <div className="space-y-3 border-t pt-3"><label className="block text-sm">{labels.reason}<Input value={reason} onChange={e => setReason(e.target.value)} maxLength={2000}/></label><div className="flex flex-wrap gap-2">{(["SUBMIT", "CONFIRM", "RETURN"] as const).filter(action => action === "SUBMIT" ? allowed(kind === "plans" ? "plan_submit" : "report_submit") : allowed(`${permission}_confirm`)).map(action => <Button key={action} variant={action === "RETURN" ? "outline" : "default"} disabled={busy || (action === "SUBMIT" && (selected.author_id ?? selected.principal_id) !== session?.subject) || (action !== "SUBMIT" && (selected.author_id ?? selected.principal_id) === session?.subject) || (action === "SUBMIT" ? !["DRAFT", "RETURNED", "BLOCKED", "READY", "OVERDUE"].includes(selected.state) : selected.state !== "SUBMITTED") || (action === "RETURN" && reason.trim().length < 4) || (kind === "reports" && action !== "RETURN" && (!check || check.result === "BLOCKED" || check.content_hash !== selected.content_hash))} onClick={() => void run({id: selected.id, version: selected.version, hash: selected.content_hash, action, reason}, key => planningApi.decide(tenantID, kind, selected, action, reason, key))}>{action === "SUBMIT" ? labels.submit : action === "CONFIRM" ? labels.confirm : labels.return}</Button>)}</div></div>}
 </section>}</div>
}

function GoalDetails({record, labels}: {record: PlanningRecord; labels: PlanningLabels}) {
 const goal = record.content as GoalInput
 const t = useTranslations("iam")
 return <div className="grid min-w-0 gap-4 lg:grid-cols-12">
  <section className="min-w-0 space-y-4 rounded-md border p-4 lg:col-span-8"><h3 className="text-xl font-semibold">{goal.title}</h3><dl className="grid grid-cols-[auto_minmax(0,1fr)] gap-x-4 gap-y-2 text-sm"><dt>{labels.level}</dt><dd>{goal.level}</dd><dt>{labels.principal}</dt><dd className="break-all">{record.principal_id}</dd><dt>{labels.project}</dt><dd className="break-all">{record.project_id}</dd><dt>{labels.period}</dt><dd>{record.period_kind} · {record.period_start}</dd>{goal.parent_goal_id && <><dt>{labels.parentGoal}</dt><dd className="break-all">{goal.parent_goal_id} / {goal.parent_kr}</dd></>}</dl></section>
  <section className="min-w-0 space-y-3 rounded-md border p-4 lg:col-span-4"><h3 className="font-medium">{t("reportComputedProgress")}</h3><p className="text-3xl font-semibold tabular-nums">{record.progress === undefined ? "—" : `${Math.round(record.progress * 100)}%`}</p>{record.progress !== undefined && <progress aria-label={t("reportComputedProgress")} className="h-2 w-full accent-primary" max={1} value={Math.max(0, Math.min(1, record.progress))}/>}</section>
  <section className="min-w-0 space-y-3 rounded-md border p-4 lg:col-span-12"><h3 className="font-medium">{labels.krTitle}</h3><AutoTable data={goal.key_results} rowKey="key" pagination={false} exportable={false} columns={[
   {field: "title", name: labels.krTitle, format: (_, kr) => <div className="space-y-1"><strong>{kr.title}</strong><p className="text-xs text-muted-foreground">{kr.key} · {kr.source === "AUTO" ? labels.automatic : labels.manual}</p></div>},
   {field: "baseline", name: labels.baseline}, {field: "current", name: labels.current}, {field: "target", name: labels.target}, {field: "weight", name: labels.weight, format: (_, kr) => kr.weight ?? 1},
   {field: "evidence", name: labels.evidence, format: (_, kr) => <p className="break-words text-sm">{kr.source === "AUTO" ? kr.metric : kr.evidence || "—"}</p>},
  ]}/></section>
 </div>
}
export function ItemList({items, labels}: {items: PlanItem[]; labels: PlanningLabels}) {const carry=planningCarryLabels(useLocale());return <ul className="space-y-2 text-sm">{items.map((item, i) => <li key={`${item.goal_id}:${item.kr}:${i}`} className="rounded-md border p-3"><strong>{item.title}</strong><p>{labels.goal}: {item.goal_id} · {labels.kr}: {item.kr}{item.work_item_id ? ` · ${labels.work}: ${item.work_item_id}` : ` · ${item.category}`}</p>{item.carried_from_report_id && <p>{carry.source}: {item.carried_from_report_id} · {item.carried_from_key}</p>}{(!item.goal_id || item.goal_id === "0" || !item.kr) && <p role="status">{carry.needsLink}</p>}</li>)}</ul>}
export function ReportEditor({record, labels, readonly, save}: {record: PlanningRecord; labels: PlanningLabels; readonly: boolean; save: (content: ReportInput) => Promise<void>}) {
 const t = useTranslations("iam")
 const content = record.content as ReportInput
 const sectionLabels = reportSectionLabels(useLocale())
 const memoryLabels = reportLabels(useLocale())
 const carry = planningCarryLabels(useLocale())
 const [unfinishedCount, setUnfinishedCount] = useState(content.uncompleted_items?.length ?? 0)
 const unfinishedFields: FormConfig = {}
 const unfinishedInitial: Record<string, unknown> = {}
 for (let i=0;i<unfinishedCount;i++) {
  const item=content.uncompleted_items?.[i]
  unfinishedFields[`unfinished_heading_${i}`]={name: `${labels.uncompleted} ${i+1}`, type: "title"}
  for (const [field,name] of [["key",carry.key],["title",labels.title],["goal",labels.goal],["kr",labels.kr],["work",labels.work]] as const) unfinishedFields[`unfinished_${field}_${i}`]={name,type:"text",required:field==="key" || field==="title",value:field==="key" ? `task-${i+1}` : "",config:{maxLength:field==="title"?200:64}}
  unfinishedFields[`unfinished_reason_${i}`]={name:labels.reason,type:"select",required:true,value:"OTHER",config:{options:["BLOCKED_DEPENDENCY","SCOPE_CHANGED","UNDERESTIMATED","REPRIORITIZED","WAITING_CLIENT","QUALITY_REWORK","RESOURCE","OTHER"].map((value,index)=>({value,label:carry.reasons[index]!}))}}
  unfinishedFields[`unfinished_detail_${i}`]={name:carry.detail,type:"textarea",required:true,config:{maxLength:2000}}
  if(item?.detail_ai_drafted) unfinishedFields[`unfinished_confirm_${i}`]={name:sectionLabels.confirmAI,type:"checkbox",value:false}
  unfinishedFields[`unfinished_carry_${i}`]={name:carry.disposition,type:"select",required:true,value:"CARRY_TO_NEXT",config:{options:[{value:"CARRY_TO_NEXT",label:carry.carry},{value:"DROP",label:carry.drop},{value:"ESCALATE",label:carry.escalate}]}}
  if(item) Object.assign(unfinishedInitial,{[`unfinished_key_${i}`]:item.key,[`unfinished_title_${i}`]:item.item.title,[`unfinished_goal_${i}`]:item.item.goal_id,[`unfinished_kr_${i}`]:item.item.kr,[`unfinished_work_${i}`]:item.item.work_item_id??"",[`unfinished_reason_${i}`]:item.reason,[`unfinished_detail_${i}`]:item.detail,[`unfinished_carry_${i}`]:item.carry_over})
 }
 const [count, setCount] = useState(Math.max(1, content.next_plan.length))
 const [improvementCount, setImprovementCount] = useState(content.improvement_plan?.length ?? 0)
 const improvementFields: FormConfig = {}
 const improvementInitial: Record<string, string | boolean> = {}
 for (let i=0;i<improvementCount;i++) {
  improvementFields[`improvement_memory_${i}`] = {name: memoryLabels.experience, type: "checkbox"}
  improvementFields[`improvement_project_${i}`] = {name: memoryLabels.memoryProject, type: "text"}
  improvementFields[`improvement_action_${i}`] = {name: `${labels.improvementAction} ${i+1}`, type: "textarea", required: true}
  improvementFields[`improvement_owner_${i}`] = {name: labels.improvementOwner, type: "text", required: true}
  improvementFields[`improvement_due_${i}`] = {name: labels.improvementDue, type: "text", required: true}
  improvementFields[`improvement_kr_${i}`] = {name: labels.kr, type: "text"}
  const item=content.improvement_plan?.[i]
  improvementInitial[`improvement_memory_${i}`]=item?.propose_memory ?? false
  improvementInitial[`improvement_project_${i}`]=item?.memory_project_id ?? ""
  improvementInitial[`improvement_action_${i}`]=item?.action ?? ""
  improvementInitial[`improvement_owner_${i}`]=item?.owner_principal_id ?? ""
  improvementInitial[`improvement_due_${i}`]=item?.due_date ?? ""
  improvementInitial[`improvement_kr_${i}`]=item?.kr_id ?? ""
 }
 const fields: FormConfig = {status_explanation: {name: t("reportStatusExplanation"), type: "textarea", config: {maxLength: 2000}}, uncompleted: {name: labels.uncompleted, type: "textarea", required: unfinishedCount === 0, config:{id:"report-uncompleted"}}, ...unfinishedFields, retro_went_well:{name:sectionLabels.wentWell,type:"textarea",required:true,config:{id:"report-retrospective",maxLength:25050}},retro_problems:{name:sectionLabels.problems,type:"textarea",required:true,config:{maxLength:25050}},retro_root_causes:{name:sectionLabels.rootCauses,type:"textarea",required:true,config:{maxLength:25050}},...(content.retrospective_ai_drafted?{retrospective_confirmed:{name:sectionLabels.confirmAI,type:"checkbox" as const,value:false}}:{}), declared_confidence: {name: labels.confidence, type: "select", required: true, config: {options: [{value: "GREEN", label: labels.green}, {value: "YELLOW", label: labels.yellow}, {value: "RED", label: labels.red}]}}, improvement_heading: {name: labels.improvement, type: "title"}, ...improvementFields, next_heading: {name: labels.nextPlan, type: "title"}, ...itemFields(labels, count)}
 fields.item_title_0={...fields.item_title_0!,config:{id:"report-next-plan",maxLength:200}}
 return <section className="space-y-3"><h3 id="report-completed" tabIndex={-1} className="font-medium">{labels.completed}</h3>{content.completed.length ? <ul className="text-sm">{content.completed.map(item => <li key={item.work_item_id}>{labels.work}: {item.work_item_id}{item.note ? ` · ${item.note}` : ""}</li>)}</ul> : <p className="text-sm text-muted-foreground">{labels.noCompletion}</p>}<AutoForm mode="edit" readonly={readonly} fields={fields} initialData={{status_explanation: content.status_explanation ?? "", uncompleted: content.uncompleted, retro_went_well:content.retrospective_sections?.went_well.join("\n")??"",retro_problems:content.retrospective_sections?.problems.join("\n")??content.retrospective,retro_root_causes:content.retrospective_sections?.root_causes.join("\n")??"", declared_confidence: content.declared_confidence, ...unfinishedInitial, ...improvementInitial, ...itemsInitial(content.next_plan)}} onSubmit={async values => {await save({...content, status_explanation: String(values.status_explanation ?? ""), uncompleted: String(values.uncompleted ?? ""), ...retrospectiveValues(values), declared_confidence: String(values.declared_confidence ?? ""), improvement_plan: Array.from({length: improvementCount}, (_, i) => ({propose_memory: Boolean(values[`improvement_memory_${i}`]), ...(values[`improvement_project_${i}`] ? {memory_project_id: String(values[`improvement_project_${i}`])} : {}), action: String(values[`improvement_action_${i}`] ?? ""), owner_principal_id: String(values[`improvement_owner_${i}`] ?? ""), due_date: String(values[`improvement_due_${i}`] ?? ""), ...(values[`improvement_kr_${i}`] ? {kr_id: String(values[`improvement_kr_${i}`])} : {})})), uncompleted_items: Array.from({length:unfinishedCount},(_,i)=>({key:String(values[`unfinished_key_${i}`]??""),detail_confirmed:Boolean(values[`unfinished_confirm_${i}`]),reason:String(values[`unfinished_reason_${i}`]??"OTHER"),detail:String(values[`unfinished_detail_${i}`]??""),carry_over:String(values[`unfinished_carry_${i}`]??"CARRY_TO_NEXT") as "CARRY_TO_NEXT"|"DROP"|"ESCALATE",item:{...(content.uncompleted_items?.[i]?.item??{}),title:String(values[`unfinished_title_${i}`]??""),goal_id:String(values[`unfinished_goal_${i}`]??"0"),kr:String(values[`unfinished_kr_${i}`]??""),...(values[`unfinished_work_${i}`]?{work_item_id:String(values[`unfinished_work_${i}`]),category:undefined}:{work_item_id:undefined,category:content.uncompleted_items?.[i]?.item.category??"OTHER"})}})), next_plan: planItems(values, count, content.next_plan)})}}>{!readonly && <div className="flex flex-wrap gap-2"><Button type="button" variant="outline" disabled={count >= 100} onClick={() => setCount(count + 1)}>{labels.addItem}</Button><Button type="button" variant="outline" disabled={count <= 1} onClick={() => setCount(count - 1)}>{labels.remove}</Button><Button type="button" variant="outline" disabled={improvementCount >= 10} onClick={() => setImprovementCount(improvementCount + 1)}>{labels.improvement} +</Button><Button type="button" variant="outline" disabled={improvementCount === 0} onClick={() => setImprovementCount(improvementCount - 1)}>{labels.improvement} ?</Button><Button type="button" variant="outline" disabled={unfinishedCount >= 100} onClick={() => setUnfinishedCount(unfinishedCount+1)}>{carry.addUncompleted}</Button><Button type="button" variant="outline" disabled={unfinishedCount === 0} onClick={() => setUnfinishedCount(unfinishedCount-1)}>{labels.uncompleted} −</Button><Button type="submit">{labels.save}</Button></div>}</AutoForm></section>
}

export default function GoalsPage() {return <PlanningPage kind="goals"/>}

function PlanEditor({record, labels, busy, save}: {record: PlanningRecord; labels: PlanningLabels; busy: boolean; save: (items: PlanItem[]) => Promise<void>}) {
 const actions = planningActionLabels(useLocale())
 const items = (record.content as {items: PlanItem[]}).items
 const suggestions = (record.content as {suggestions?: PlanItem[]}).suggestions ?? []
 const carry=planningCarryLabels(useLocale())
 const formRef=useRef<UseAutoFormReturn>(null)
 const limit = record.period_kind === "DAY" ? 30 : 200
 const [count, setCount] = useState(items.length)
 return <section className="space-y-3 border-t pt-3"><h3>{actions.plan}</h3><PlanSuggestions suggestions={suggestions} labels={carry} busy={busy || count>=limit} add={item=>{const values=itemsInitial([item]);for(const [key,value] of Object.entries(values)) formRef.current?.setValue(key.replace(/_0$/,`_${count}`),value);setCount(count+1)}}/><AutoForm formRef={formRef} fields={itemFields(labels, count)} mode="edit" readonly={busy} initialData={itemsInitial(items)} onSubmit={values => save(planItems(values, count, items))}><div className="flex flex-wrap gap-2"><Button type="button" variant="outline" disabled={busy || count >= limit} onClick={() => setCount(count + 1)}>{labels.addItem}</Button><Button type="button" variant="outline" disabled={busy || count <= 1} onClick={() => setCount(count - 1)}>{labels.remove}</Button><Button type="submit" disabled={busy}>{actions.save}</Button></div></AutoForm></section>
}
function GoalControls({record, labels, busy, save}: {record: PlanningRecord; labels: PlanningLabels; busy: boolean; save: (change: Parameters<typeof planningApi.changeGoal>[2]) => Promise<void>}) {
 const actions = planningActionLabels(useLocale())
 const goal = record.content as GoalInput
 const [reason, setReason] = useState("")
 if (!["DRAFT", "ACTIVE", "AT_RISK"].includes(record.state)) return null
 const fields: FormConfig = {title: {name: labels.title, type: "text", required: true}, weight: {name: labels.weight, type: "number", config: {min: 0, max: 10, step: 0.1}}}
 const initial: Record<string, unknown> = {title: goal.title, weight: goal.weight ?? 1}
 for (const [i, kr] of goal.key_results.entries()) {
  fields[`title_${i}`] = {name: `${labels.krTitle} ${i + 1}`, type: "text", required: true}
  fields[`target_${i}`] = {name: labels.target, type: "number", required: true}
  fields[`weight_${i}`] = {name: labels.weight, type: "number", config: {min: 0, max: 10, step: 0.1}}
  initial[`weight_${i}`] = kr.weight ?? 1
  initial[`title_${i}`] = kr.title;initial[`target_${i}`] = kr.target
 }
 return <section className="space-y-4 border-t pt-3">{record.state === "DRAFT" && <><AutoForm fields={fields} initialData={initial} mode="edit" readonly={busy} onSubmit={values => save({action: "EDIT", content: {...goal, title: String(values.title), weight: Number(values.weight), key_results: goal.key_results.map((kr, i) => ({...kr, title: String(values[`title_${i}`]), target: Number(values[`target_${i}`]), weight: Number(values[`weight_${i}`])}))}})}><Button type="submit" disabled={busy}>{actions.save}</Button></AutoForm><Button disabled={busy} onClick={() => void save({action: "ACTIVATE"})}>{actions.activate}</Button></>}
 {goal.key_results.filter(kr => kr.source === "MANUAL").map(kr => <AutoForm key={kr.key} fields={{current: {name: `${kr.title} / ${labels.current}`, type: "number", required: true}, evidence: {name: labels.evidence, type: "text"}}} initialData={{current: kr.current, evidence: kr.evidence ?? ""}} mode="edit" readonly={busy} onSubmit={values => save({action: "MANUAL", key: kr.key, current: Number(values.current), evidence: String(values.evidence ?? "")})}><Button type="submit" disabled={busy}>{actions.update}</Button></AutoForm>)}
 {goal.key_results.some(kr => kr.source === "AUTO") && <Button variant="outline" disabled={busy} onClick={() => void save({action: "REFRESH"})}>{actions.refresh}</Button>}
 <label className="block text-sm">{actions.reason}<Input value={reason} onChange={event => setReason(event.target.value)} maxLength={2000}/></label><div className="flex flex-wrap gap-2">{(["ACHIEVED", "MISSED", "CANCELLED"] as const).map(result => <Button key={result} variant="outline" disabled={busy || (result === "CANCELLED" ? !reason.trim() : record.state === "DRAFT")} onClick={() => void save({action: "CLOSE", result, reason})}>{result === "ACHIEVED" ? actions.achieved : result === "MISSED" ? actions.missed : actions.cancel}</Button>)}</div>
 </section>
}

export function ReportReopenControl({record, subject, allowed, busy, reason, setReason, labels, reopen}: {record: PlanningRecord; subject?: string; allowed: boolean; busy: boolean; reason: string; setReason: (value: string) => void; labels: PlanningLabels; reopen: () => Promise<void>}) {
 if (record.state !== "CONFIRMED" || !subject || !allowed || (record.author_id ?? record.principal_id) === subject || !record.can_reopen) return null
 return <section className="space-y-3 border-t pt-3"><label className="block text-sm">{labels.reopenReason}<Input value={reason} onChange={event => setReason(event.target.value)} maxLength={2000} required disabled={busy}/></label><Button variant="outline" disabled={busy || [...reason.trim()].length < 4} onClick={() => void reopen()}>{labels.reopen}</Button></section>
}

export function PlanSuggestions({suggestions, labels, busy, add}: {suggestions: PlanItem[]; labels: ReturnType<typeof planningCarryLabels>; busy: boolean; add: (item: PlanItem) => void}) {
 if (!suggestions.length) return null
 return <section className="space-y-2"><h4>{labels.suggestions}</h4><ul className="space-y-2">{suggestions.map(item=><li key={`${item.goal_id}:${item.kr}`} className="flex flex-wrap items-center gap-2"><span>{item.title}</span><Button type="button" variant="outline" disabled={busy} onClick={()=>add(item)}>{labels.add}: {item.title}</Button></li>)}</ul></section>
}
