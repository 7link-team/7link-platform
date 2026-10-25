import { useCallback } from "react"
import { Link, useSearchParams } from "react-router"
import { Button } from "@workspace/ui/components/button"
import { AutoTable } from "@workspace/ui/components/auto-table"
import { Tabs, TabsList, TabsTrigger } from "@workspace/ui/components/tabs"
import { useTranslations } from "@workspace/ui/i18n/intl-provider"
import { Alert } from "../../components/alert"
import { useFacts } from "../runtime/use-facts"
import { pipelineApi, pipelineStages, type PipelineRun } from "./pipeline-api"

export function PipelineView({ tenant, project }: { tenant: string; project: string }) {
  const t = useTranslations("iam")
  const [params, setParams] = useSearchParams()
  const rawOffset = Number(params.get("offset") ?? 0)
  const offset = Number.isSafeInteger(rawOffset) && rawOffset >= 0 ? rawOffset : 0
  const work = params.get("work") ?? ""
  const view = params.get("tab") === "graph" ? "graph" : "list"
  const load = useCallback((signal: AbortSignal) => pipelineApi.list(project, work, offset, signal), [project, work, offset])
  const { data, error, loading, refresh } = useFacts(`${tenant}:${project}:${work}:${offset}`, load)
  const change = (key: string, value: string) => setParams(old => { const next = new URLSearchParams(old); next.set(key, value); return next })
  return <section className="min-h-0 flex-1 space-y-3 overflow-auto">
    <p className="text-sm text-muted-foreground">{t("pipelineSourceNotice")}</p>
    <div className="flex flex-wrap items-center gap-3"><Tabs value={view} onValueChange={value => change("tab", value)}><TabsList aria-label={t("pipelineStage")}><TabsTrigger className="min-h-11" value="list">{t("pipelineList")}</TabsTrigger><TabsTrigger className="min-h-11" value="graph">{t("pipelineGraph")}</TabsTrigger></TabsList></Tabs><Button variant="outline" disabled={loading} onClick={refresh}>{t("refresh")}</Button></div>
    {error && <Alert>{error}</Alert>}{loading && <p role="status">{t("loading")}</p>}
    {!loading && data && <><PipelineFacts items={data.items} view={view} tenant={tenant} project={project}/>
    <p role="status">{t("pipelineTotal", {count:data.total})}</p><div className="flex gap-3"><Button variant="outline" disabled={offset===0} onClick={()=>change("offset",String(Math.max(0,offset-50)))}>{t("previous")}</Button><Button variant="outline" disabled={offset+50>=data.total} onClick={()=>change("offset",String(offset+50))}>{t("next")}</Button></div></>}
  </section>
}

export function PipelineFacts({items, view, tenant, project}: {items: PipelineRun[]; view: "list" | "graph"; tenant: string; project: string}) {
  const t = useTranslations("iam")
  const root = `/admin/${encodeURIComponent(tenant)}/entities/${encodeURIComponent(project)}`
  const detail = (row: PipelineRun) => <div className="space-y-1 text-sm"><Link className="underline" to={`${root}/work-items?work=${encodeURIComponent(row.work_item_id)}`}>{t("executionWorkItem")}: {row.work_item_id}</Link><div>{t("pipelineResult")}: {row.result_id}</div><div>{t("pipelineAttempt")}: {row.attempt} / {t("pipelineRound")}: {row.internal_round ?? t("pipelineUnknown")}</div><div>{t(`pipelineState_${row.state}`)}</div><div>{t("pipelineStarted")}: {row.started_at ?? t("pipelineUnknown")}</div><div>{t("pipelineEnded")}: {row.ended_at ?? t("pipelineUnknown")}</div>{row.execution_id && <Link className="block underline" to={`${root}/executions/${encodeURIComponent(row.execution_id)}`}>{t("pipelineExecution")}: {row.execution_id}</Link>}{row.verification_id && <div>{t("pipelineVerification")}: {row.verification_id}</div>}{row.decision_id && <div>{t("pipelineDecision")}: {row.decision_id}</div>}{row.delivery_id && <div>{t("pipelineDelivery")}: {row.delivery_id}</div>}<ul>{row.evidence_refs.map(ref => <li className="break-all font-mono text-xs" key={ref}>{ref}</li>)}</ul></div>
  return <>{view === "list" ? <AutoTable data={items} rowKey="id" pagination={false} exportable={false} emptyContent={t("pipelineEmpty")} columns={[{field:"stage",name:t("pipelineStage"),format:(_,row)=>t(`pipelineStage_${row.stage}`)},{field:"id",name:t("pipelineSource"),format:(_,row)=>detail(row)}]}/> : <div className="grid gap-3 sm:grid-cols-2 xl:grid-cols-3">{pipelineStages.map(stage => <section className="min-w-0 rounded-lg border p-3" key={stage}><h2 className="mb-3 font-medium">{t(`pipelineStage_${stage}`)}</h2><ol className="space-y-3">{items.filter(row=>row.stage===stage).map(row=><li className="border-t pt-3" key={row.id}>{detail(row)}</li>)}</ol>{!items.some(row=>row.stage===stage)&&<p className="text-sm text-muted-foreground">{t("pipelineEmpty")}</p>}</section>)}</div>}</>
}
