import {useEffect, useState} from "react"
import {useParams, useSearchParams} from "react-router"
import {useLocale} from "@workspace/ui/i18n/intl-provider"
import {Button} from "@workspace/ui/components/button"
import {planningApi, type GoalInput, type GoalTreeRow, type PlanningRecord} from "./api"
import {planningLabels} from "./labels"

import {goalTreeVisible, krProgress, goalViewLabels, type GoalFilters} from "./tree-model"
export function GoalTreeView({rows,filters,open}: {rows:GoalTreeRow[];filters:GoalFilters;open:(row:PlanningRecord)=>void}) {
 const locale=useLocale();const labels=planningLabels(locale);const words=goalViewLabels(locale)
 const visible=goalTreeVisible(rows,filters)
 const byID=new Map(rows.map(row=>[row.id,row]))
 const roots=rows.filter(row=>visible.has(row.id)&&!byID.has((row.content as GoalInput).parent_goal_id??""))
 // Corrupt cyclic input cannot trap rendering or silently hide all records.
 const render=(row:GoalTreeRow,path:Set<string>)=>{
  if(path.has(row.id)) return null
  const next=new Set(path).add(row.id);const goal=row.content as GoalInput
  return <details key={row.id} open className="min-w-0 border-l pl-3"><summary className="cursor-pointer py-2"><strong>{goal.title}</strong> ? {row.state} ? {row.principal_id}</summary><div className="space-y-2 pb-3"><Button variant="outline" size="sm" onClick={()=>open(row)}>{labels.view}</Button><p className="text-sm">{labels.period}: {row.period_kind} ? {row.period_start}</p><label className="block text-sm">{words.elapsed}: {Math.round(row.elapsed*100)}%<progress className="block h-2 w-full max-w-md" max={1} value={row.elapsed}/></label>{goal.key_results.map(kr=><section key={kr.key} className="space-y-2 pl-3"><p className="text-sm font-medium">{kr.title} ? {kr.current} / {kr.target} ? {Math.round(krProgress(kr)*100)}%</p><progress aria-label={`${words.progress}: ${kr.title}`} className="h-2 w-full max-w-md" value={krProgress(kr)} max={1}/>{rows.filter(child=>visible.has(child.id)&&(child.content as GoalInput).parent_goal_id===row.id&&(child.content as GoalInput).parent_kr===kr.key).map(child=>render(child,next))}</section>)}</div></details>
 }
 return <section aria-label={words.tree} className="space-y-3"><p className="text-sm text-muted-foreground">{words.hint}</p>{roots.length?roots.map(row=>render(row,new Set())):<p>{labels.empty}</p>}</section>
}
export function GoalTree({open}: {open:(row:PlanningRecord)=>void}) {
 const {tenantID="",projectID="",entityID=""}=useParams();const [search,setSearch]=useSearchParams()
 const locale=useLocale();const labels=planningLabels(locale);const words=goalViewLabels(locale)
 const [rows,setRows]=useState<GoalTreeRow[]>([]);const [error,setError]=useState("");const [loading,setLoading]=useState(true);const [refresh,setRefresh]=useState(0)
 const filters:GoalFilters={project:projectID||entityID||search.get("goal_project")||"",owner:search.get("goal_owner")||"",state:search.get("goal_state")||"",period:search.get("goal_period")||"",start:search.get("goal_start")||"",origin:search.get("goal_origin")||""}
 useEffect(()=>{const abort=new AbortController();void (async()=>{try{const all:GoalTreeRow[]=[];let cursor="";const seen=new Set<string>();do{const page=await planningApi.tree(tenantID,cursor,abort.signal);all.push(...page.items);if(!page.has_more)break;if(!page.next_cursor||seen.has(page.next_cursor))throw new Error(labels.retry);cursor=page.next_cursor;seen.add(cursor)}while(!abort.signal.aborted);if(!abort.signal.aborted)setRows(all)}catch(cause){if(!abort.signal.aborted)setError(cause instanceof Error?cause.message:String(cause))}finally{if(!abort.signal.aborted)setLoading(false)}})();return()=>abort.abort()},[tenantID,refresh,labels.retry])
 const select=(key:string,label:string,options:string[]) => <label className="text-sm">{label}<select className="mt-1 block h-10 w-full rounded-md border bg-background px-2" value={search.get(`goal_${key}`)??""} onChange={e=>setSearch(previous=>{const next=new URLSearchParams(previous);if(e.target.value)next.set(`goal_${key}`,e.target.value);else next.delete(`goal_${key}`);return next})}><option value="">{words.all}</option>{options.map(value=><option key={value} value={value}>{value==="HUMAN"?words.human:value==="HUMAN_CREATED"?words.manual:value==="AGENT_SPAWNED"?words.spawned:value}</option>)}</select></label>
 return <div className="space-y-4">{error&&<p role="alert">{error}<Button onClick={()=>{setLoading(true);setError("");setRefresh(refresh+1)}}>{labels.retry}</Button></p>}<div className="grid gap-3 sm:grid-cols-2 lg:grid-cols-3">{select("period",labels.period,["MONTH","QUARTER","YEAR"])}{select("start",labels.start,[...new Set(rows.map(row=>row.period_start))].sort())}{!projectID&&!entityID&&select("project",labels.project,[...new Set(rows.map(row=>row.project_id))])}{select("owner",labels.principal,[...new Set(rows.map(row=>row.principal_id))])}{select("state",labels.state,["DRAFT","ACTIVE","AT_RISK","ACHIEVED","MISSED","CANCELLED"])}{select("origin",words.origin,["HUMAN","HUMAN_CREATED","AGENT_SPAWNED"])}</div>{loading?<p role="status">{labels.loading}</p>:<GoalTreeView rows={rows} filters={filters} open={open}/>}</div>
}
