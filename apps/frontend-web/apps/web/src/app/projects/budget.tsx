import {useEffect,useRef,useState} from "react"
import {AutoForm} from "@workspace/ui/components/auto-form"
import {Button} from "@workspace/ui/components/button"
import {PaginationPages} from "@workspace/ui/components/pagination"
import {useLocale,useTranslations} from "@workspace/ui/i18n/intl-provider"
import {Alert} from "../../components/alert"
import type {ProjectDetail} from "../../lib/iam-api"
import {useAllowed} from "../../lib/permissions"
import {projectBudgetApi,type BudgetEvent} from "./budget-api"

export function BudgetHistory({rows}:{rows:BudgetEvent[]}){
 const t=useTranslations("iam"),locale=useLocale()
 return <ol className="grid gap-3">{rows.map(row=><li key={row.id} className="border-l-2 pl-3 text-sm"><p>{t.has(`budgetEvent_${row.kind}`)?t(`budgetEvent_${row.kind}`):row.kind} · {new Date(row.occurred_at).toLocaleString(locale)}</p><p>{t("projectBudgetCap")}: {row.cap_micros??t("budgetUnlimited")} · {t("budgetSpent")}: {row.spent_micros} · {row.state} · v{row.cap_version}</p><p className="whitespace-pre-wrap break-words">{row.reason}</p></li>)}</ol>
}
export function ProjectBudget({project,onChanged}:{project:ProjectDetail;onChanged:()=>void}){
 const t=useTranslations("iam"),allowed=useAllowed(),[rows,setRows]=useState<BudgetEvent[]>([]),[offset,setOffset]=useState(0),[total,setTotal]=useState(0),[error,setError]=useState(""),[busy,setBusy]=useState(false),[reload,setReload]=useState(0)
 const attempt=useRef<{identity:string;key:string}|null>(null),pending=useRef(false)
 useEffect(()=>{const c=new AbortController();void projectBudgetApi.history(project.project_id!,offset,c.signal).then(page=>{if(!c.signal.aborted){setRows(page.items);setTotal(page.total);setError("")}},e=>{if(!c.signal.aborted){setRows([]);setError(String(e))}});return()=>c.abort()},[project.project_id,project.version,offset,reload])
 const readOnly=!!project.archived_at||["CLOSED","CANCELLED"].includes(project.stage)
 return <section className="grid gap-3 border-t py-4" aria-label={t("projectBudgetCap")} aria-busy={busy}>
  <h2 className="font-medium">{t("projectBudgetCap")}</h2><p className="text-sm">{t("budgetSpent")}: {project.budget_spent_micros} · {project.budget_state}</p><p className="text-sm text-muted-foreground">{t("budgetManagementNotice")}</p>
  {error&&<Alert>{error}</Alert>}<Button className="min-h-11 justify-self-start" variant="outline" disabled={busy} onClick={()=>setReload(v=>v+1)}>{t("refresh")}</Button>
  <BudgetHistory rows={rows}/><PaginationPages page={Math.floor(offset/10)+1} totalPages={Math.max(1,Math.ceil(total/10))} onPageChange={page=>setOffset((page-1)*10)}/>
  {!readOnly&&allowed("project_manage")&&<fieldset disabled={busy}><AutoForm key={`${project.project_id}:${project.version}`} mode="edit" initialData={{cap:project.budget_cap_micros??"",notify:project.budget_notify_pct}} fields={{cap:{name:t("projectBudgetCap"),type:"number",config:{min:1,max:1_000_000_000_000_000}},notify:{name:t("projectBudgetNotifyPct"),type:"number",required:true,config:{min:50,max:95}},confirm:{name:t("budgetConfirmBelowSpent"),type:"checkbox"},reason:{name:t("stageReason"),type:"textarea",required:true}}} onSubmit={async values=>{
   if(pending.current)return
   const body={cap_micros:values.cap===""||values.cap==null?null:Number(values.cap),notify_pct:Number(values.notify),reason:String(values.reason??""),confirm_below_spent:values.confirm===true},identity=JSON.stringify({project:project.project_id,version:project.version,body})
   if(attempt.current?.identity!==identity)attempt.current={identity,key:crypto.randomUUID()}
   pending.current=true;setBusy(true);setError("")
   try{await projectBudgetApi.change(project.project_id!,project.version,body,attempt.current.key);onChanged();setReload(v=>v+1)}catch(e){setError(String(e));throw e}finally{pending.current=false;setBusy(false)}
  }}><Button type="submit" className="min-h-11">{t("clientSave")}</Button></AutoForm></fieldset>}
 </section>
}
