import { useEffect, useRef, useState } from "react"
import { useParams, useSearchParams } from "react-router"
import type { FormConfig } from "@workspace/ui/components/auto-form"
import { AutoTable } from "@workspace/ui/components/auto-table"
import { Button } from "@workspace/ui/components/button"
import { useTranslations } from "@workspace/ui/i18n/intl-provider"
import { Alert } from "../../components/alert"
import { PageHeader } from "../../components/page-header"
import { useAllowed } from "../../lib/permissions"
import { iamApi } from "../../lib/iam-api"
import { approvedChangeDelta, collaborationID, toleranceBudget, toleranceForecast, type ClientPage } from "../../lib/clientwork-api"
import type { components } from "../../lib/api-schema"
import { scopeBody, type ScopeValues } from "../requirements/changes-api"
import { ReauthenticationDialog } from "../../components/reauthentication-dialog"
import { MutationForm } from "./client"

type Tolerance = components["schemas"]["ToleranceView"]
type Exception = components["schemas"]["ExceptionView"]
type Receipt = components["schemas"]["Receipt"]
type Kind = "tolerances" | "exceptions"
type Row = Tolerance | Exception
function path(project: string, kind: Kind, id?: string) {
  return `/entities/${collaborationID(project)}/project/${kind}${id ? `/${collaborationID(id)}` : ""}`
}


export default function ToleranceWorkspace() {
  const { entityID: project = "" } = useParams(), [params, setParams] = useSearchParams()
  const t = useTranslations("iam"), allowed = useAllowed(), canRead = allowed("project_read")
  const kind: Kind = params.get("collection") === "exceptions" ? "exceptions" : "tolerances"
  const cursor = params.get("cursor") ?? "", selected = params.get("id") ?? ""
  const [page, setPage] = useState<ClientPage<Row>>({ items: [], has_more: false })
  const [revision, setRevision] = useState(0), [loading, setLoading] = useState(false), [error, setError] = useState("")
  const [detail, setDetail] = useState<Row | null>(null)
  const [scope, setScope] = useState("")
  const requestScope = JSON.stringify([project, kind, cursor, revision, canRead, selected])
  if (scope !== requestScope) { setScope(requestScope); setPage({ items: [], has_more: false }); setDetail(null); setError(""); setLoading(canRead) }
  useEffect(() => {
    const c = new AbortController()
    if (!canRead) return () => c.abort()
    void iamApi.deliveryResultRequest<ClientPage<Row>>({ method: "GET", url: `${path(project, kind)}?${new URLSearchParams({ limit: "20", ...(cursor ? { cursor } : {}) })}` }, { signal: c.signal }).then(v => { if (!c.signal.aborted) setPage(v) }).catch(e => { if (!c.signal.aborted) setError(String(e)) }).finally(() => { if (!c.signal.aborted) setLoading(false) })
    return () => c.abort()
  }, [project, kind, cursor, revision, canRead])
  useEffect(() => {
    const c = new AbortController()
    if (!canRead || !selected) return () => c.abort()
    void iamApi.deliveryResultRequest<Row>({ method: "GET", url: path(project, kind, selected) }, { signal: c.signal }).then(v => { if (!c.signal.aborted) setDetail(v) }).catch(e => { if (!c.signal.aborted) setError(String(e)) })
    return () => c.abort()
  }, [project, kind, selected, revision, canRead])
  const refresh = () => setRevision(v => v + 1)
  const command = async (url: string, method: "POST" | "PUT", body: unknown, key: string, version?: number) => { await iamApi.deliveryResultRequest<Receipt>({ method, url }, { headers: { "Idempotency-Key": key, ...(version ? { "If-Match": `"${version}"` } : {}) }, body: JSON.stringify(body) }); refresh() }
  const budgetFields: FormConfig = {
    appetite_end_at: { name: t("toleranceEnd"), type: "text", required: true },
    appetite_cost_micros: { name: t("toleranceCost"), type: "number" },
    appetite_scope_criteria: { name: t("toleranceScope"), type: "number" },
    tolerance_time_pct: { name: t("toleranceTimePct"), type: "number", required: true },
    tolerance_cost_pct: { name: t("toleranceCostPct"), type: "number", required: true },
    tolerance_scope_pct: { name: t("toleranceScopePct"), type: "number", required: true },
  }
  return <div className="grid min-w-0 gap-4 overflow-auto"><PageHeader title={t("projectSurfaceExceptions")} />{!canRead ? <p role="status">{t("projectSurfaceAccessUnavailable")}</p> : <>
    <nav aria-label={t("clientSections")} className="flex flex-wrap gap-2">{(["tolerances", "exceptions"] as const).map(k => <Button key={k} variant={k === kind ? "default" : "outline"} onClick={() => setParams({ collection: k })}>{t(k === "tolerances" ? "toleranceCommitments" : "toleranceExceptions")}</Button>)}<Button variant="outline" disabled={loading} onClick={refresh}>{t("clientRetry")}</Button></nav>
    {error && <Alert>{error}</Alert>}
    {kind === "tolerances" && allowed("requirement_read") && <ToleranceImpact key={`${project}:${revision}`} project={project} />}
    <AutoTable data={page.items.map(r => ({ id: r.id, state: r.state, version: r.version, subject: "subject_kind" in r ? `${r.subject_kind} ${r.subject_id}` : r.tolerance_id }))} rowKey="id" loading={loading} pagination={false} exportable={false} emptyContent={t("clientEmpty")} columns={[{ field: "subject", name: t("toleranceSubject"), render: (_, r) => <button type="button" className="underline underline-offset-4" onClick={() => setParams(old => { const p = new URLSearchParams(old); p.set("id", r.id!); return p })}>{r.subject}</button> }, { field: "state", name: t("status") }, { field: "version", name: t("clientVersion") }]} />
    <div className="flex gap-2"><Button variant="outline" disabled={!cursor || loading} onClick={() => setParams({ collection: kind })}>{t("clientFirstPage")}</Button><Button variant="outline" disabled={!page.has_more || loading} onClick={() => setParams({ collection: kind, cursor: page.next_cursor! })}>{t("fleetNext")}</Button></div>
    {selected && detail && <section className="grid gap-3 border-t pt-4"><h2 className="font-medium">{t("clientDetail")}</h2>{kind === "tolerances" ? <ToleranceDetail key={`${selected}:${revision}`} row={detail as Tolerance} fields={budgetFields} canManage={allowed("project_manage")} submit={(suffix, body, key, method = "POST") => command(`${path(project, "tolerances", selected)}${suffix}`, method, body, key, detail.version)} /> : <ExceptionDetail key={`${selected}:${revision}`} row={detail as Exception} fields={budgetFields} canManage={allowed("project_manage")} canDecide={allowed("exception_decide")} submit={(suffix, body, key, method = "POST") => command(`${path(project, "exceptions", selected)}${suffix}`, method, body, key, detail.version)} />}</section>}
    {kind === "tolerances" && allowed("project_manage") && <section className="grid gap-3 border-t pt-4"><h2 className="font-medium">{t("clientCreate")}</h2><MutationForm fields={{ subject_kind: { name: t("toleranceSubject"), type: "select", required: true, config: { options: [{ value: "PROJECT", label: t("toleranceProject") }, { value: "REQUIREMENT", label: t("toleranceRequirement") }] } }, subject_id: { name: t("toleranceSubjectID"), type: "text", required: true }, ...budgetFields }} initialData={{ subject_kind: "PROJECT", subject_id: project, tolerance_time_pct: 20, tolerance_cost_pct: 15, tolerance_scope_pct: 10 }} label={t("clientCreate")} submit={(v, key) => command(path(project, "tolerances"), "POST", { ...toleranceBudget(v, t("toleranceInvalidBudget")), subject_kind: v.subject_kind, subject_id: collaborationID(String(v.subject_id ?? "")) }, key)} /></section>}
  </>}</div>
}

function ToleranceImpact({project}: {project: string}) {
  const t = useTranslations("iam")
  const [snapshot, setSnapshot] = useState<components["schemas"]["ToleranceChangeSnapshot"] | null>(null)
  const request = useRef<AbortController | null>(null)
  useEffect(() => () => request.current?.abort(), [])
  return <section className="grid min-w-0 gap-3 border-t pt-4"><h2 className="font-medium">{t("toleranceImpactTitle")}</h2><p className="text-sm text-muted-foreground">{t("toleranceImpactHint")}</p>
    <MutationForm fields={{requirement_id: {name: t("clientRequirements"), type: "text", required: true}, schedule_delta_days: {name: t("toleranceDeltaDays"), type: "number"}, cost_delta_micros: {name: t("toleranceDeltaCost"), type: "number"}, scope_delta_criteria: {name: t("toleranceDeltaScope"), type: "number"}}} initialData={{schedule_delta_days: 0, cost_delta_micros: 0, scope_delta_criteria: 0}} label={t("toleranceImpactTitle")} submit={async values => {
      request.current?.abort()
      const controller = new AbortController()
      request.current = controller
      setSnapshot(null)
      const requirement = collaborationID(String(values.requirement_id ?? ""))
      const body = approvedChangeDelta(values)
      const result = await iamApi.deliveryResultRequest<components["schemas"]["ToleranceChangeSnapshot"]>({method: "POST", url: `/entities/${collaborationID(project)}/requirements/${requirement}/tolerance-impact`}, {signal: controller.signal, body: JSON.stringify(body)})
      if (!controller.signal.aborted) setSnapshot(result)
    }} />
    {snapshot && <div role="status" className="grid min-w-0 gap-3">
      {snapshot.low_confidence && <Alert>{t("toleranceImpactUncertain")}</Alert>}
      <p>{(snapshot.breached ?? []).length ? t("toleranceImpactBreach") : t("toleranceImpactWithin")}</p>
      {!(snapshot.subjects ?? []).length && <p>{t("toleranceImpactNoCommitment")}</p>}
      {(snapshot.subjects ?? []).map(after => {
        const before = (snapshot.before ?? []).find(row => row.id === after.id)
        return <dl key={after.id} className="grid min-w-0 gap-1 border-t pt-3 text-sm"><dt>{t("toleranceSubject")}</dt><dd>{t(after.subject_kind === "PROJECT" ? "toleranceProject" : "toleranceRequirement")} {after.subject_id}</dd><dt>{t("toleranceBaseline")}</dt><dd>{after.baseline_no}</dd><dt>{t("toleranceEnd")}</dt><dd className="break-words">{after.appetite_end_at}</dd><dt>{t("toleranceImpactForecast")}</dt><dd className="break-words">{before?.forecast.end_at && !before.forecast.end_at.startsWith("0001-") ? before.forecast.end_at : before?.appetite_end_at} → {after.forecast.end_at}</dd><dt>{t("toleranceCost")}</dt><dd>{before?.forecast.cost_micros ?? before?.appetite_cost_micros ?? "—"} → {after.forecast.cost_micros ?? "—"}</dd><dt>{t("toleranceScope")}</dt><dd>{before?.forecast.scope_criteria ?? before?.appetite_scope_criteria ?? "—"} → {after.forecast.scope_criteria ?? "—"}</dd><dt>{t("toleranceImpactDimensions")}</dt><dd>{(snapshot.breached ?? []).filter(row => row.subject_id === after.subject_id && row.subject_kind === after.subject_kind).map(row => t(`toleranceDimension_${row.dimension}`)).join(" / ") || "—"}</dd></dl>
      })}
    </div>}
  </section>
}

type DetailProps = { fields: FormConfig; canManage: boolean; submit: (suffix: string, body: unknown, key: string, method?: "POST" | "PUT") => Promise<void> }
function ToleranceDetail({ row, fields, canManage, submit }: DetailProps & { row: Tolerance }) {
  const t = useTranslations("iam")
  return <><dl className="grid gap-1"><dt>{t("toleranceEnd")}</dt><dd>{row.appetite_end_at}</dd><dt>{t("toleranceBaseline")}</dt><dd>{row.baseline_no}</dd><dt>{t("toleranceForecast")}</dt><dd>{row.forecast.end_at}</dd></dl>{row.open_exception_id && <p role="status">{t("toleranceLocked")}</p>}{canManage && !row.open_exception_id && <MutationForm fields={fields} initialData={row} label={t("clientSave")} submit={(v, key) => submit("", { ...toleranceBudget(v, t("toleranceInvalidBudget")), subject_kind: row.subject_kind, subject_id: row.subject_id }, key, "PUT")} />}{canManage && <MutationForm fields={{ end_at: { name: t("toleranceForecastEnd"), type: "text", required: true }, cost_micros: { name: t("toleranceCost"), type: "number" }, scope_criteria: { name: t("toleranceScope"), type: "number" } }} label={t("toleranceRecordForecast")} submit={(v, key) => submit("/forecasts", toleranceForecast(v, t("toleranceInvalidBudget")), key)} />}</>
}
function ExceptionDetail({ row, fields, canManage, canDecide, submit }: DetailProps & { row: Exception; canDecide: boolean }) {
  const t = useTranslations("iam")
  return <><p>{(row.breached_dimensions ?? []).join(" / ")}</p><p>{row.forecast_snapshot.appetite_end_at} → {row.forecast_snapshot.forecast.end_at}</p>{row.report && <section className="grid gap-2"><h3>{t("toleranceReport")}</h3><p className="whitespace-pre-wrap">{row.report.situation}</p>{(row.report.options ?? []).map(o => <p key={o.decision}>{o.decision}: {o.impact}</p>)}<p>{row.report.recommendation}</p></section>}{row.decision && <p>{row.decision}: {row.decision_reason}</p>}{row.state === "RAISED" && canManage && <MutationForm fields={{ situation: { name: t("toleranceSituation"), type: "textarea", required: true }, impact: { name: t("toleranceImpact"), type: "textarea", required: true }, recommendation: { name: t("toleranceRecommendation"), type: "select", required: true, config: { options: ["CONTINUE", "RESHAPE", "HOLD", "KILL"].map(value => ({ value, label: t(`toleranceDecision_${value}`) })) } } }} label={t("toleranceSubmitReport")} submit={(v, key) => submit("/report", { situation: v.situation, recommendation: v.recommendation, options: [{ decision: v.recommendation, impact: v.impact, cost_delta: 0, time_delta: 0 }] }, key, "PUT")} />}{row.state === "REPORTED" && canDecide && <><h3>{t("toleranceDecision_CONTINUE")}</h3><MutationForm fields={{ ...fields, reason: { name: t("clientReason"), type: "textarea", required: true } }} initialData={row.forecast_snapshot} label={t("toleranceDecision_CONTINUE")} submit={(v, key) => submit("/decisions", { decision: "CONTINUE", reason: v.reason, budget: toleranceBudget(v, t("toleranceInvalidBudget")) }, key)} /><h3>{t("toleranceDecision_HOLD")}</h3><MutationForm fields={{ reason: { name: t("clientReason"), type: "textarea", required: true }, resume_condition: { name: t("toleranceResume"), type: "textarea", required: true } }} label={t("toleranceDecision_HOLD")} submit={(v, key) => submit("/decisions", { ...v, decision: "HOLD" }, key)} />{row.forecast_snapshot.subject_kind === "REQUIREMENT" && <ReshapeException row={row} fields={fields} submit={submit} />}<KillException submit={submit} /></>}</>
}

function ReshapeException({row,fields,submit}:Pick<DetailProps,"fields"|"submit">&{row:Exception}) {
 const t=useTranslations("iam"),[pending,setPending]=useState<{body:Record<string,unknown>;key:string}|null>(null)
 return <><h3>{t("toleranceDecision_RESHAPE")}</h3><MutationForm fields={{...fields,reason:{name:t("clientReason"),type:"textarea",required:true},requirement_version:{name:t("clientVersion"),type:"number",required:true},base_version:{name:t("clientRevision"),type:"number",required:true},description:{name:t("clientDescription"),type:"textarea",required:true},action:{name:t("scopeCriterionAction"),type:"select",value:"NONE",config:{options:["NONE","ADD","RETIRE","SUPERSEDE"].map(value=>({value,label:t(`scopeAction_${value}`)}))}},criterion_id:{name:t("scopeCriterion"),type:"text",show:v=>v?.action==="RETIRE"||v?.action==="SUPERSEDE"},text:{name:t("workCriterionText"),type:"textarea",show:v=>v?.action==="ADD"||v?.action==="SUPERSEDE"},required:{name:t("requirementRequired"),type:"checkbox",value:true}}} initialData={row.forecast_snapshot} label={t("toleranceDecision_RESHAPE")} submit={async(v,key)=>{
  const budget=toleranceBudget(v,t("toleranceInvalidBudget"))
  setPending({key,body:{decision:"RESHAPE",reason:v.reason,budget,change:{requirement_id:row.forecast_snapshot.subject_id,requirement_version:Number(v.requirement_version),change:{base_version_no:Number(v.base_version),summary:v.reason,description:v.description,criteria:scopeBody({current_version_no:Number(v.base_version)},v as ScopeValues).criteria}}}})
 }}/>{pending&&<ReauthenticationDialog purpose="scope_change" target={`requirement:${row.forecast_snapshot.subject_id}`} onClose={()=>setPending(null)} onVerified={async proof=>{await submit("/decisions",{...pending.body,reauth_proof:proof},pending.key);setPending(null)}}/>}</>
}

function KillException({ submit }: Pick<DetailProps, "submit">) {
  const t = useTranslations("iam"), { entityID: project = "" } = useParams()
  const [pending, setPending] = useState<{ reason: string; key: string } | null>(null)
  return <><h3>{t("toleranceDecision_KILL")}</h3><MutationForm fields={{ reason: { name: t("clientReason"), type: "textarea", required: true } }} label={t("toleranceDecision_KILL")} submit={async (values, key) => {
    const reason = String(values.reason ?? "").trim()
    if ([...reason].length < 10 || [...reason].length > 2000) throw new Error(`${t("clientReason")}: 10-2000`)
    setPending({ reason, key })
  }} />{pending && <ReauthenticationDialog purpose="project_cancel" target={`project:${collaborationID(project)}`} onClose={() => setPending(null)} onVerified={async proof => {
    await submit("/decisions", { decision: "KILL", reason: pending.reason, reauth_proof: proof }, pending.key)
    setPending(null)
  }} />}</>
}
