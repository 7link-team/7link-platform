import {useCallback,useState} from "react"
import {AutoTable} from "@workspace/ui/components/auto-table"
import {Button} from "@workspace/ui/components/button"
import {useLocale} from "@workspace/ui/i18n/intl-provider"
import {Alert} from "../../components/alert"
import {useFacts} from "../runtime/use-facts"
import {projectMetricMessages,projectMetricNotes,projectMetricsCSV,readProjectMetrics,type ProjectMetrics,type ProjectMetricSnapshot} from "./project-metrics-data"

export function ProjectMetricsPanel({tenantID,period,projectID=""}:{tenantID:string;period:{from?:string;to?:string};projectID?:string}){
 const locale=useLocale(),l=projectMetricMessages[locale as keyof typeof projectMetricMessages]??projectMetricMessages["en-US"]
 const [project,setProject]=useState(projectID)
 const [selection,setSelection]=useState({model:"",verifier:"",workflow:""})
 const load=useCallback((signal:AbortSignal)=>readProjectMetrics(tenantID,period,project,signal,selection),[tenantID,period,project,selection])
 const {data,error,loading,refresh}=useFacts(`${tenantID}:${period.from??""}:${period.to??""}:${project}:${JSON.stringify(selection)}`,load)
 const download=(format:"json"|"csv")=>{if(!data)return;const body=format==="json"?JSON.stringify(data,null,2):projectMetricsCSV(data);const url=URL.createObjectURL(new Blob([body],{type:format==="json"?"application/json;charset=utf-8":"text/csv;charset=utf-8"}));const link=document.createElement("a");link.href=url;link.download=`project-metrics.${format}`;link.click();URL.revokeObjectURL(url)}
 return <section className="min-w-0 space-y-4" aria-label={l.title}>
  <div className="flex flex-wrap items-center justify-between gap-3"><h2 className="text-lg font-semibold">{l.title}</h2><div className="flex flex-wrap gap-2"><Button variant="outline" disabled={loading} onClick={refresh}>{l.refresh}</Button><Button variant="outline" disabled={!data||loading} onClick={()=>download("json")}>{l.exportJSON}</Button><Button variant="outline" disabled={!data||loading} onClick={()=>download("csv")}>{l.exportCSV}</Button></div></div>
  <p className="text-sm text-muted-foreground">{l.hint}</p>
  {!projectID&&<label className="flex max-w-full flex-col gap-1 text-sm">{l.project}<select className="min-h-11 rounded border bg-background p-2" value={project} onChange={event=>setProject(event.target.value)}><option value="">{l.all}</option>{data?.projects.map(row=><option key={row.project_id} value={row.project_id}>{row.project_id}</option>)}{project&&!data?.projects.some(row=>row.project_id===project)&&<option value={project}>{project}</option>}</select></label>}
  <div className="flex flex-wrap gap-3">{(["model","verifier","workflow"] as const).map(name=><label key={name} className="flex min-w-0 flex-col gap-1 text-sm">{l[name]}<select className="min-h-11 max-w-full rounded border bg-background p-2" value={selection[name]} onChange={event=>setSelection(old=>({...old,[name]:event.target.value}))}><option value="">{l.any}</option>{(data?.team.versions[name]??[]).map(version=><option key={version} value={version}>{version}</option>)}{selection[name]&&!data?.team.versions[name]?.includes(selection[name])&&<option value={selection[name]}>{selection[name]}</option>}</select></label>)}</div>
  {loading&&<p role="status">{l.loading}</p>}{error&&<Alert>{error}</Alert>}
  {data&&<ProjectMetricsValues data={data} locale={locale}/>}
 </section>
}
export function ProjectMetricsValues({data,locale}:{data:ProjectMetrics;locale:string}){
 const l=projectMetricMessages[locale as keyof typeof projectMetricMessages]??projectMetricMessages["en-US"]
 const notes=projectMetricNotes[locale as keyof typeof projectMetricNotes]??projectMetricNotes["en-US"]
 const note=(key:string)=>notes[key as keyof typeof notes]??key
 const sections:{id:string;value:ProjectMetricSnapshot}[]=[{id:l.team,value:data.team},...data.projects.map(value=>({id:`${l.project} ${value.project_id}`,value}))]
 return <div className="min-w-0 space-y-6"><p className="break-words text-sm">{data.from} ≤ UTC &lt; {data.to} · {data.version} · {data.cohort} · {data.computed_at}</p>{sections.map(({id,value})=><section className="min-w-0 space-y-3" key={id}><h3 className="font-semibold">{id}</h3><p className="text-sm">{l.samples}: {value.work_items} · {l.scope}: {value.scope_changed}</p><AutoTable data={Object.entries(value.kpis).map(([key,metric])=>({key,...metric}))} rowKey="key" pagination={false} exportable={false} emptyContent={l.empty} columns={[
 {field:"key",name:l.metric,format:key=>l.keys[key as keyof typeof l.keys]??key},{field:"value",name:l.value,format:(_,row)=><span className="break-all tabular-nums">{row.value??l.empty}{row.lower_bound?` · ${l.lower}`:""}</span>},{field:"numerator",name:l.facts,format:(_,row)=>`${row.numerator??l.empty} / ${row.denominator??l.empty}`},{field:"missing",name:l.missing,format:(_,row)=>`${row.missing} / ${row.missing_denominator} · ${row.missing_rate??l.empty}`},{field:"reason",name:l.reason,format:value=>typeof value==="string"?note(value):""},
 ]}/><dl className="grid gap-3 sm:grid-cols-2">{[[l.rounds,value.internal_rounds],[l.cycles,value.rework_cycle]].map(([name,distribution])=>{const d=distribution as ProjectMetricSnapshot["internal_rounds"];return <div key={String(name)} className="min-w-0 border p-3"><dt>{String(name)}</dt><dd className="mt-2 break-words text-sm tabular-nums">{l.samples}: {d.samples} · {l.sum}: {d.sum} · {l.mean}: {d.mean??l.empty} · P50 {d.p50??l.empty} · P90 {d.p90??l.empty} · P95 {d.p95??l.empty}</dd></div>})}</dl><p className="break-words text-sm">{l.versions}: {Object.entries(value.versions).map(([name,versions])=>`${name}: ${versions.join(", ")||l.empty}`).join(" · ")}</p>{value.missing_sources.length>0&&<p className="break-words text-sm text-muted-foreground">{l.reason}: {value.missing_sources.map(note).join(", ")}</p>}</section>)}</div>
}
