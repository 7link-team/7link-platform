import {useEffect, useRef, useState} from "react"
import {useSearchParams} from "react-router"
import {Button} from "@workspace/ui/components/button"
import {Input} from "@workspace/ui/components/input"
import {useTranslations} from "@workspace/ui/i18n/intl-provider"
import {useAllowed} from "../../lib/permissions"
import {iamApi} from "../../lib/iam-api"
import type {components} from "../../lib/api-schema"
import {qualityRate} from "./metrics-format"
import {Alert} from "../../components/alert"

type Metrics = components["schemas"]["KnowledgeMetrics"]
type History = components["schemas"]["DiagnosticHistoryPage"]
type Result = components["schemas"]["RetrievalDebugResult"]
export function MetricsHistory({tenant, project, onSelect}: {tenant: string; project: string; onSelect?: (result: Result) => void}) {
 const t=useTranslations("iam")
 const allowed=useAllowed()
 const showHistory=Boolean(onSelect)&&allowed("knowledge_manage")
 const [search,setSearch]=useSearchParams()
 const [metrics,setMetrics]=useState<Metrics|null>(null)
 const [history,setHistory]=useState<History|null>(null)
 const [error,setError]=useState("")
 const [busy,setBusy]=useState(false)
 const [filter,setFilter]=useState(search.get("history_search")||"")
 const pending=useRef<AbortController|null>(null)
 const offset=Math.max(0,Number(search.get("history_offset"))||0)
 const size=Math.min(200,Math.max(1,Number(search.get("history_limit"))||50))
 const [from,setFrom]=useState(()=>new Date(Date.now()-30*86400000).toISOString().slice(0,10))
 const [to,setTo]=useState(()=>new Date().toISOString().slice(0,10))
 useEffect(()=>()=>pending.current?.abort(),[])
 const load=async(next=offset,limit=size)=>{
  pending.current?.abort();const controller=new AbortController();pending.current=controller
  setBusy(true);setError("");setHistory(null);setMetrics(null)
  const params=new URLSearchParams(search);params.set("history_offset",String(next));params.set("history_limit",String(limit));params.set("history_search",filter);setSearch(params,{replace:true})
  try{
   const [m,h]=await Promise.all([iamApi.knowledgeMetrics(tenant,{from:new Date(`${from}T00:00:00Z`).toISOString(),to:new Date(`${to}T23:59:59.999Z`).toISOString(),...(project?{project_id:project}:{})},controller.signal),(showHistory ? iamApi.retrievalHistory(tenant,{scope:project?"PROJECT":"TEAM",...(project?{project_id:project}:{}),search:filter,offset:next,limit},controller.signal) : Promise.resolve(null))])
   if(!controller.signal.aborted){setMetrics(m);setHistory(h)}
  }catch(e){if(!controller.signal.aborted)setError(e instanceof Error?e.message:String(e))}finally{if(!controller.signal.aborted)setBusy(false)}
 }
 if(!allowed("knowledge_read"))return null
 const unavailable=t("knowledgeMetricUnavailable")
 return <section className="space-y-4 rounded-lg border p-4" aria-busy={busy}>
  <h2 className="text-lg font-medium">{t("knowledgeQualityTitle")}</h2>
  <form className="flex flex-wrap items-end gap-3" onSubmit={e=>{e.preventDefault();void load(0)}}>
   <label className="text-sm">{t("knowledgeMetricFrom")}<Input required type="date" value={from} onChange={e=>setFrom(e.target.value)}/></label>
   <label className="text-sm">{t("knowledgeMetricTo")}<Input required type="date" value={to} onChange={e=>setTo(e.target.value)}/></label>
   <label className="text-sm">{t("knowledgeHistoryFilter")}<Input maxLength={500} value={filter} onChange={e=>setFilter(e.target.value)}/></label>
   <Button disabled={busy}>{t(busy?"loading":"knowledgeMetricLoad")}</Button>
  </form>
  {error&&<Alert>{error}</Alert>}
  {metrics&&<>
   <dl className="grid gap-4 sm:grid-cols-2 lg:grid-cols-4">{([
    ["knowledgeMetricHit",metrics.hit_rate],["knowledgeMetricUsed",metrics.used_rate],["knowledgeMetricAccepted",metrics.citation_acceptance_rate],["knowledgeMetricZero",metrics.search_zero_result_rate]
   ] as const).map(([key,value])=><div key={key}><dt>{t(key)}</dt><dd className="font-mono text-sm">{qualityRate(value,unavailable)}</dd>{value.reason&&<p className="text-xs text-muted-foreground">{t(`knowledgeMetricReason_${value.reason}`)}</p>}</div>)}</dl>
   <p className="text-sm text-muted-foreground">{t("knowledgeMetricCoverage")}</p>
   <p className="text-sm">{t("knowledgeMetricCounts")}: {metrics.injections} / {metrics.inclusions} / {metrics.citations} / {metrics.searches}</p>
   <dl className="grid gap-3 sm:grid-cols-3">{([["knowledgeMetricSearchLatency",metrics.search_latency],["knowledgeMetricInjectionLatency",metrics.injection_latency],["knowledgeMetricDebugLatency",metrics.diagnostic_latency]] as const).map(([key,value])=><div key={key}><dt>{t(key)}</dt><dd className="font-mono text-sm">{value.p95_ms===undefined?unavailable:`${value.p95_ms.toFixed(3)} ms`} ? n={value.samples}</dd></div>)}</dl>
   <p className="text-sm">{t("knowledgeMetricMemory")}: {metrics.memory.written} / {metrics.memory.escalated} / {metrics.memory.approved} / {metrics.memory.rejected} / {metrics.memory.revoked}</p>
  </>}
  {showHistory && <h3 className="font-medium">{t("knowledgeHistoryTitle")}</h3>}
  {history&&<>
   <div className="flex flex-wrap items-center gap-2">
    <Button variant="outline" disabled={busy||offset===0} onClick={()=>void load(Math.max(0,offset-size))}>{t("knowledgeHistoryPrevious")}</Button>
    <span className="text-sm">{Math.floor(offset/size)+1} / {Math.max(1,Math.ceil(history.total/size))} ? {history.total}</span>
    <Button variant="outline" disabled={busy||offset+size>=history.total} onClick={()=>void load(offset+size)}>{t("knowledgeHistoryNext")}</Button>
    <label className="text-sm">{t("knowledgeHistoryPage")}<Input type="number" min={1} max={Math.max(1,Math.ceil(history.total/size))} defaultValue={Math.floor(offset/size)+1} key={offset} onBlur={e=>{const page=Number(e.target.value);if(Number.isInteger(page)&&page>0&&page<=Math.max(1,Math.ceil(history.total/size)))void load((page-1)*size)}}/></label>
    <label className="text-sm">{t("knowledgeHistorySize")}<select value={size} onChange={e=>void load(0,Number(e.target.value))}>{[10,25,50,100,200].map(n=><option key={n}>{n}</option>)}</select></label>
   </div>
   {history.items?.map(row=><Button key={row.log_id} className="h-auto w-full justify-start whitespace-normal text-left" variant="outline" onClick={()=>onSelect?.(row)}>{row.query.query} ? {new Date(row.evaluated_at).toLocaleString()} ? {row.timings.total_ms.toFixed(3)} ms</Button>)}
   {!history.total&&<p className="text-sm">{t("knowledgeHistoryEmpty")}</p>}
  </>}
 </section>
}
