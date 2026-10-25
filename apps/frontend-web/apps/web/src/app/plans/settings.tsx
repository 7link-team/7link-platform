import { useEffect, useRef, useState } from "react"
import { useParams } from "react-router"
import { AutoForm, type FormConfig } from "@workspace/ui/components/auto-form"
import { Button } from "@workspace/ui/components/button"
import { Dialog, DialogContent, DialogHeader, DialogTitle, DialogDescription } from "@workspace/ui/components/dialog"
import { useLocale } from "@workspace/ui/i18n/intl-provider"
import { useAllowed } from "../../lib/permissions"
import { iamApi } from "../../lib/iam-api"
import { settingsLabels } from "./settings-labels"

type Settings = {
 timezone: string; day_report_at: string; week_report_at: string; month_report_at: string; quarter_report_at: string; year_report_at: string;
 grace_day_minutes: number; grace_week_minutes: number; grace_month_minutes: number; grace_quarter_minutes: number; grace_year_minutes: number;
 required_member_kinds: string[]; agent_day_report: boolean; workdays: number[]; at_risk_threshold: number; version: number
}
export function PlanningSettingsButton() {
 const {tenantID=""}=useParams()
 const allowed=useAllowed()
 const labels=settingsLabels(useLocale())
 const [open,setOpen]=useState(false)
 if (!allowed("goal_read")) return null
 return <><Button variant="outline" onClick={()=>setOpen(true)}>{labels.title}</Button>{open&&<SettingsDialog key={tenantID} tenant={tenantID} canManage={allowed("planning_settings_manage")} onClose={()=>setOpen(false)}/>}</>
}
function SettingsDialog({tenant,canManage,onClose}:{tenant:string;canManage:boolean;onClose:()=>void}) {
 const labels=settingsLabels(useLocale())
 const [value,setValue]=useState<Settings|null>(null)
 const [error,setError]=useState("")
 const [busy,setBusy]=useState(false)
 const [saved,setSaved]=useState(false)
 const pending=useRef(false)
 const active=useRef(true)
 const url=`/tenants/${encodeURIComponent(tenant)}/planning-settings`
 useEffect(()=>{const abort=new AbortController();active.current=true;iamApi.deliveryResultRequest<Settings>({method:"GET",url},{signal:abort.signal}).then(v=>{if(!abort.signal.aborted)setValue(v)}).catch(e=>{if(!abort.signal.aborted)setError(e instanceof Error?e.message:String(e))});return()=>{active.current=false;abort.abort()}},[url])
 const fields:FormConfig={}
 for(const key of ["timezone","day_report_at","week_report_at","month_report_at","quarter_report_at","year_report_at"] as const) fields[key]={name:labels[key],type:"text",required:true,disabled:busy||!canManage}
 for(const key of ["grace_day_minutes","grace_week_minutes","grace_month_minutes","grace_quarter_minutes","grace_year_minutes"] as const) fields[key]={name:labels[key],type:"number",required:true,disabled:busy||!canManage,config:{min:0,step:1}}
 fields.at_risk_threshold={name:labels.at_risk_threshold,type:"number",required:true,disabled:busy||!canManage,config:{min:0,max:1,step:0.01}}
 fields.agent_day_report={name:labels.agent_day_report,type:"switch",disabled:busy||!canManage}
 fields.required_member_kinds={name:labels.required_member_kinds,type:"checkbox-group",disabled:busy||!canManage,config:{options:["DAY","WEEK","MONTH","QUARTER","YEAR"].map((v,i)=>({value:v,label:labels.periods[i]}))}}
 fields.workdays={name:labels.workdays,type:"checkbox-group",disabled:busy||!canManage,config:{options:labels.days.map((label,i)=>({value:String(i+1),label}))}}
 return <Dialog open onOpenChange={v=>{if(!v&&!pending.current)onClose()}}><DialogContent className="max-h-[90dvh] overflow-y-auto sm:max-w-2xl"><DialogHeader><DialogTitle>{labels.title}</DialogTitle><DialogDescription>{labels.hint}</DialogDescription></DialogHeader>{error&&<p role="alert" className="text-destructive">{error}</p>}{saved&&<p role="status">{labels.saved}</p>}{!value?(error?null:<p role="status">{labels.loading}</p>):<AutoForm key={value.version} mode="edit" initialData={{...value,workdays:value.workdays.map(String)}} fields={fields} className="md:grid-cols-2" onSubmit={async values=>{
 if(!canManage||pending.current)return
 pending.current=true;setBusy(true);setError("");setSaved(false)
 const body:Record<string,unknown>={...values,workdays:Array.isArray(values.workdays)?values.workdays.map(Number):[],required_member_kinds:Array.isArray(values.required_member_kinds)?values.required_member_kinds:[],agent_day_report:Boolean(values.agent_day_report)}
 delete body.version
 for(const key of ["grace_day_minutes","grace_week_minutes","grace_month_minutes","grace_quarter_minutes","grace_year_minutes","at_risk_threshold"])body[key]=Number(body[key])
 try{const next=await iamApi.deliveryResultRequest<Settings>({method:"PATCH",url},{headers:{"If-Match":`"${value.version}"`},body:JSON.stringify(body)});if(active.current){setValue(next);setSaved(true)}}catch(e){if(active.current)setError(e instanceof Error?e.message:String(e))}finally{pending.current=false;if(active.current)setBusy(false)}
 }}>{canManage&&<Button type="submit" disabled={busy}>{labels.save}</Button>}</AutoForm>}</DialogContent></Dialog>
}
