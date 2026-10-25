import { useContext, useEffect, useState } from "react"
import { useParams, useSearchParams } from "react-router"
import { AutoTable } from "@workspace/ui/components/auto-table"
import { Button } from "@workspace/ui/components/button"
import { Input } from "@workspace/ui/components/input"
import { useTranslations } from "@workspace/ui/i18n/intl-provider"
import { PageHeader } from "../../components/page-header"
import { AuthContext } from "../../components/auth"
import { Alert } from "../../components/alert"
import { useAllowed } from "../../lib/permissions"
import { iamApi } from "../../lib/iam-api"
import { collaborationID, type ClientPage } from "../../lib/clientwork-api"
import type { components } from "../../lib/api-schema"
import { MutationForm } from "./client"

type Proposal = components["schemas"]["DispatchView"]
type Candidates = components["schemas"]["DispatchCandidatesView"]
const endpoint = (project: string, id?: string) => `/entities/${collaborationID(project)}/dispatch-proposals${id ? `/${collaborationID(id)}` : ""}`
export default function DispatchWorkspace() {
 const {tenantID="",entityID=""}=useParams(), auth=useContext(AuthContext)
 return <DispatchWorkspaceBody key={`${tenantID}:${entityID}:${auth?.session?.subject??""}`} />
}
function DispatchWorkspaceBody() {
 const { entityID: project = "" } = useParams(), t = useTranslations("iam"), allowed = useAllowed()
 const canAssign = allowed("work_item_assign")
 const [params, setParams] = useSearchParams(), [revision, setRevision] = useState(0)
 const selected = params.get("id") ?? "", cursor = params.get("cursor") ?? ""
 const workFilter=params.get("work_item_id")??"", principalFilter=params.get("principal_id")??"", stateFilter=params.get("state")??""
 const previous=params.getAll("previous")
 const listKey=JSON.stringify([project,cursor,workFilter,principalFilter,stateFilter,revision])
 const [loadedQuery,setLoadedQuery]=useState("")
 const [page, setPage] = useState<ClientPage<Proposal> | null>(null), [detail, setDetail] = useState<Proposal | null>(null), [error, setError] = useState("")
 const [preview,setPreview]=useState<{project:string;data:Candidates}|null>(null)
 const [stopStatus,setStopStatus]=useState<{project:string;proposal:string;state:string}|null>(null)
 useEffect(() => {
  const c = new AbortController()
  if (!canAssign) return () => c.abort()
  void iamApi.deliveryResultRequest<ClientPage<Proposal>>({method:"GET", url:`${endpoint(project)}?${new URLSearchParams({limit:"20", ...(cursor ? {cursor} : {}),...(workFilter?{work_item_id:workFilter}:{}),...(principalFilter?{principal_id:principalFilter}:{}),...(stateFilter?{state:stateFilter}:{})})}`}, {signal:c.signal}).then(p => {if(!c.signal.aborted) {setPage(p);setLoadedQuery(listKey);setError("")}}).catch(e => {if(!c.signal.aborted)setError(String(e))})
  return () => c.abort()
 }, [project, cursor, revision, canAssign, workFilter, principalFilter, stateFilter, listKey])
 useEffect(() => {
  const c = new AbortController()
  if (!canAssign || !selected) return () => c.abort()
  void iamApi.deliveryResultRequest<Proposal>({method:"GET",url:endpoint(project,selected)},{signal:c.signal}).then(p => {if(!c.signal.aborted)setDetail(p)}).catch(e => {if(!c.signal.aborted)setError(String(e))})
  return () => c.abort()
 }, [project, selected, revision, canAssign])
 const refresh=()=>{setDetail(null);setPage(null);setError("");setRevision(v=>v+1)}
 const command=(body:unknown,key:string,id?:string,version?:number)=>iamApi.deliveryResultRequest<components["schemas"]["Receipt"]>({method:"POST",url:`${endpoint(project,id)}${id?"/decisions":""}`},{headers:{"Idempotency-Key":key,...(version?{"If-Match":`"${version}"`}:{})},body:JSON.stringify(body)}).then(receipt=>{refresh();if(!id&&receipt.id)choose(receipt.id)})
 const choose=(id:string)=>{setDetail(null);setError("");setParams(p=>{const n=new URLSearchParams(p);n.set("id",id);return n})}
 const currentPage=loadedQuery===listKey?page:null
 const move=(target:string,history:string[])=>setParams(old=>{const next=new URLSearchParams(old);next.delete("cursor");next.delete("previous");next.delete("id");if(target)next.set("cursor",target);for(const value of history)next.append("previous",value);return next})
 return <div className="grid min-w-0 gap-4 overflow-auto"><PageHeader title={t("projectSurfaceDispatch")}/>{!canAssign?<p role="status">{t("projectSurfaceAccessUnavailable")}</p>:<>
  <p className="text-sm text-muted-foreground">{t("dispatchCurrentLimits")}</p>
  <form key={JSON.stringify([workFilter,principalFilter,stateFilter])} className="flex flex-wrap items-end gap-3" aria-label={t("dispatchHistoryFilters")} onSubmit={event=>{
   event.preventDefault();const fields=new FormData(event.currentTarget)
   setDetail(null);setError("");setParams(old=>{const next=new URLSearchParams(old);for(const key of ["id","cursor","previous","work_item_id","principal_id","state"])next.delete(key);for(const key of ["work_item_id","principal_id","state"]){const value=String(fields.get(key)??"").trim();if(value)next.set(key,value)}return next})
  }}>
   <label className="grid gap-1 text-sm">{t("clientWorkItem")}<Input name="work_item_id" defaultValue={workFilter} inputMode="numeric" pattern="[1-9][0-9]{0,18}" /></label>
   <label className="grid gap-1 text-sm">{t("dispatchHistoryPrincipal")}<Input name="principal_id" defaultValue={principalFilter} inputMode="numeric" pattern="[1-9][0-9]{0,18}" /></label>
   <label className="grid gap-1 text-sm">{t("status")}<select name="state" defaultValue={stateFilter} className="h-9 rounded-md border bg-background px-3"><option value="">{t("dispatchHistoryAll")}</option>{["PROPOSED","APPLIED","DISMISSED","EXPIRED"].map(state=><option key={state} value={state}>{state}</option>)}</select></label>
   <Button type="submit">{t("dispatchHistoryApply")}</Button>
  </form>
  {error&&<Alert>{error}</Alert>}<Button variant="outline" onClick={refresh}>{t("clientRetry")}</Button>
  {!currentPage&&!error&&<p role="status">{t("loading")}</p>}
  {currentPage&&<AutoTable data={currentPage.items.map(row=>({id:row.id,work:row.work_item_id,state:row.state,result:row.result,expires:row.expires_at,decision:row.decision?.action??"",actor:row.decision?.actor_id??""}))} rowKey="id" pagination={false} exportable={false} emptyContent={t("clientEmpty")} columns={[{field:"id",name:t("clientDetail"),render:(_,row)=><Button variant="link" onClick={()=>choose(String(row.id))}>{String(row.id)}</Button>},{field:"work",name:t("clientWorkItem")},{field:"state",name:t("status")},{field:"result",name:t("dispatchResult")},{field:"expires",name:t("dispatchExpires")},{field:"decision",name:t("dispatchDecisionRecord")},{field:"actor",name:t("actorId")}]}/>}
  <div className="flex justify-end gap-2"><Button variant="outline" disabled={!cursor||!currentPage} onClick={()=>move(previous.at(-1)??"",previous.slice(0,-1))}>{t("fleetPrevious")}</Button><Button variant="outline" disabled={!currentPage?.has_more||!currentPage.next_cursor} onClick={()=>move(currentPage!.next_cursor!,[...previous,cursor])}>{t("fleetNext")}</Button></div>
  {detail&&detail.id===selected&&<section className="grid gap-3 border-t pt-4"><h2>{t("clientDetail")} / {detail.id}</h2><p>{detail.state} / {detail.snapshot.scoring_version} / {detail.expires_at}</p><p className="break-all text-xs">{detail.inputs_digest}</p><p>{t("dispatchGuards")}: {detail.snapshot.guard_reasons?.join(", ")}</p>
   {detail.decision&&<section className="grid gap-2 border-l-2 pl-3"><h3>{t("dispatchDecisionRecord")}: {detail.decision.id}</h3><p>{detail.decision.action} / {detail.decision.created_at} / {t("clientRevision")}: {detail.decision.proposal_version}</p><p>{t("actorId")}: {detail.decision.actor_id}</p><p>{t("dispatchAuthority")}: {detail.decision.authority_source}</p><p className="whitespace-pre-wrap break-words">{detail.decision.reason}</p></section>}
   <AutoTable data={(detail.snapshot.candidates??[]).map(c=>({id:c.agent_id,name:c.name,score:c.score/10000,skill:c.components.skill_bp/100,fpa:c.history_available?String(c.components.fpa_bp/100):t("dispatchUnmeasured"),load:`${c.active_items}/${c.max_parallel}`,codes:c.ineligible_codes?.join(", ")}))} rowKey="id" pagination={false} exportable={false} emptyContent={t("clientEmpty")} columns={[{field:"name",name:t("name")},{field:"score",name:t("dispatchScore")},{field:"skill",name:t("dispatchSkill")},{field:"fpa",name:t("dispatchQuality")},{field:"load",name:t("dispatchLoad")},{field:"codes",name:t("dispatchEligibility")}]}/>
   <AutoTable data={(detail.snapshot.candidates??[]).map(c=>({id:c.agent_id,name:c.name,queue:c.pending_commands??t("dispatchUnmeasured"),skills:c.observed_skills?`${c.observed_skills.state} / ${c.observed_skills.source} / ${c.observed_skills.inspection_id}: ${(c.observed_skills.skills??[]).map(skill=>`${skill.name} (${skill.sha256})`).join(", ")}`:t("dispatchUnmeasured")}))} rowKey="id" pagination={false} exportable={false} emptyContent={t("clientEmpty")} columns={[{field:"name",name:t("name")},{field:"queue",name:t("dispatchPendingCommands")},{field:"skills",name:t("dispatchLocalSkills")}]} />
   {detail.state==="PROPOSED"&&detail.trigger==="REDISPATCH"&&detail.snapshot.work.current_execution_id&&allowed("execution_stop")&&<section className="grid gap-2"><p className="text-sm text-muted-foreground">{t("dispatchStopNotice")}</p><MutationForm key={`stop:${detail.id}:${detail.version}`} fields={{reason:{name:t("clientReason"),type:"textarea",required:true}}} label={t("dispatchStopExecution")} submit={async(v,key)=>{
    const executionID=collaborationID(detail.snapshot.work.current_execution_id??"")
    const proposalID=collaborationID(detail.id??"")
    const current=await iamApi.execution(executionID)
    if(current.execution.project_id!==project||current.execution.work_item_id!==detail.work_item_id)throw new Error(t("executionWrongProject"))
    const result=await iamApi.deliveryResultRequest<{state:string}>({method:"POST",url:`${endpoint(project,proposalID)}/execution-stop`},{headers:{"Idempotency-Key":key,"If-Match":`"${detail.version}"`},body:JSON.stringify({execution_id:executionID,expected_execution_version:current.execution.version,reason:v.reason})})
    setStopStatus({project,proposal:proposalID,state:result.state});refresh()
   }}/></section>}
   {stopStatus?.project===project&&stopStatus.proposal===detail.id&&<p role="status">{t(stopStatus.state==="EXECUTION_STOPPED"?"dispatchStopComplete":"dispatchStopPending")}</p>}
   {(detail.state==="PROPOSED"||detail.can_expire)&&<MutationForm key={`${detail.id}:${detail.version}`} fields={{decision:{name:t("dispatchDecision"),type:"select",required:true,config:{options:[...(detail.can_expire?[{value:"EXPIRE",label:t("dispatchExpire")}]:[...(detail.recommended_id?[{value:"CONFIRM",label:t("dispatchConfirm")}]:[]),{value:"OVERRIDE",label:t("dispatchOverride")},{value:"DISMISS",label:t("dispatchDismiss")}])] }},assignee_principal_id:{name:t("dispatchAssignee"),type:"text"},reason:{name:t("clientReason"),type:"textarea",required:true}}} label={t("clientSave")} submit={(v,key)=>command({decision:v.decision,reason:v.reason,...(v.decision==="OVERRIDE"?{assignee_principal_id:collaborationID(String(v.assignee_principal_id??""))}:{})},key,detail.id!,detail.version)}/>}
  </section>}
  <section className="grid gap-3 border-t pt-4"><h2>{t("dispatchPreview")}</h2><p className="text-sm text-muted-foreground">{t("dispatchPreviewNotice")}</p><MutationForm fields={{work_item_id:{name:t("clientWorkItem"),type:"text",required:true}}} label={t("dispatchPreview")} submit={async v=>{setPreview(null);const data=await iamApi.deliveryResultRequest<Candidates>({method:"GET",url:`/entities/${collaborationID(project)}/work-items/${collaborationID(String(v.work_item_id??""))}/dispatch-candidates`});setPreview({project,data})}}/>
   {preview?.project===project&&<section className="grid gap-2" aria-live="polite"><p>{t("clientWorkItem")}: {preview.data.snapshot.work.work_item_id} / {t("clientRevision")}: {preview.data.snapshot.work.work_version}</p><p className="break-all text-xs">{preview.data.inputs_digest}</p><AutoTable data={(preview.data.snapshot.candidates??[]).map(c=>({id:c.agent_id,name:c.name,score:c.score/10000,codes:c.ineligible_codes?.join(", ")}))} rowKey="id" pagination={false} exportable={false} emptyContent={t("clientEmpty")} columns={[{field:"name",name:t("name")},{field:"score",name:t("dispatchScore")},{field:"codes",name:t("dispatchEligibility")}]}/><MutationForm key={`observed:${project}:${preview.data.inputs_digest}`} fields={{}} label={t("dispatchCreate")} submit={(_,key)=>command({work_item_id:collaborationID(preview.data.snapshot.work.work_item_id),expected_work_version:preview.data.snapshot.work.work_version,trigger:preview.data.snapshot.work.state==="READY"?"MANUAL_REQUEST":"REDISPATCH"},key)}/></section>}
  </section>
  <section className="grid gap-3 border-t pt-4"><h2>{t("dispatchCreate")}</h2><MutationForm fields={{work_item_id:{name:t("clientWorkItem"),type:"text",required:true},expected_work_version:{name:t("clientRevision"),type:"number",required:true},trigger:{name:t("dispatchTrigger"),type:"select",required:true,config:{options:[{value:"MANUAL_REQUEST",label:t("dispatchManual")},{value:"CHANNEL_REQUEST",label:t("dispatchChannel")},{value:"REDISPATCH",label:t("dispatchRedispatch")}]}}}} initialData={{trigger:"MANUAL_REQUEST"}} label={t("dispatchCreate")} submit={(v,key)=>{const version=Number(v.expected_work_version);if(!Number.isSafeInteger(version)||version<1)throw new Error(t("clientSaveFailed"));return command({...v,work_item_id:collaborationID(String(v.work_item_id??"")),expected_work_version:version},key)}}/></section>
 </>}</div>
}
