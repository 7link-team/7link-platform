import { iamApi } from "../../lib/iam-api"
import { planningApi, type PlanningRecord } from "../goals/api"
import { memoryApi } from "../agents/memory-api"
import { clarificationApi, type Clarification } from "../project-views/clarification-api"
import type { InboxItem } from "./api"

type Source = {id:string;version:number;project_id?:string;state?:string;learning_state?:string;learning_draft?:{title?:string;body?:string};title?:string;body?:string;reason?:string;summary?:string;note?:string;conflict_memory_ids?:string[]|null;content_hash?:string;recommended_id?:string;inputs_digest?:string;supervisor_id?:string;expires_at?:string;tool?:string;content?:{files?:string[];risks?:string[];criteria_coverage?:{criterion_id:string;approach:string;verification:string}[]}}
export type Loaded = {source:Source; path:string; planning?:PlanningRecord}
export const inlineInboxKinds = ["MEMORY_RULE_APPROVAL","DISPATCH_PROPOSAL","ENSEMBLE_APPROVAL","AGENT_SPAWN_APPROVAL","PLAN_CONFIRMATION","REPORT_CONFIRMATION","PLAN_APPROVAL","QUALITY_ESCAPE","WORKLOG_APPROVAL","PREVIEW_SHARE_APPROVAL","REQUIREMENT_CONFLICT"]
const get = <T,>(url:string,signal:AbortSignal)=>iamApi.deliveryResultRequest<T>({method:"GET",url},{signal})
// Every preview comes from the source owner's authorized read, including when
// its domain only exposes a paginated collection rather than an item endpoint.
export async function loadInboxSource(tenant:string,item:InboxItem,signal:AbortSignal):Promise<Loaded> {
 const team=`/tenants/${encodeURIComponent(tenant)}`,project=`/entities/${encodeURIComponent(item.project_id??"")}`,id=encodeURIComponent(item.object_id)
 if(item.kind==="PLAN_CONFIRMATION"||item.kind==="REPORT_CONFIRMATION"){
  const kind=item.kind==="PLAN_CONFIRMATION"?"plans":"reports",record=await planningApi.get(tenant,kind,item.object_id,signal)
  return {source:{id:record.id,version:record.version,state:record.state,content_hash:record.content_hash,project_id:record.project_id},path:`${team}/${kind}/${id}`,planning:record}
 }
 let path:string
 switch(item.kind){
 case "MEMORY_RULE_APPROVAL":path=`${team}/knowledge/${id}`;break
 case "DISPATCH_PROPOSAL":path=`${project}/dispatch-proposals/${id}`;break
 case "ENSEMBLE_APPROVAL":path=`${team}${project}/ensemble-plans/${encodeURIComponent(item.id)}`;break
 case "QUALITY_ESCAPE":path=`${team}/quality-escapes/${id}`;break
 case "WORKLOG_APPROVAL":{
  const collection=`${team}/projects/${encodeURIComponent(item.project_id??"")}/worklogs`,seen=new Set<string>()
  let cursor=""
  for(;;){
   const page=await get<{items:Source[]|null;has_more:boolean;next_cursor?:string}>(`${collection}?${new URLSearchParams({limit:"100",...(cursor?{cursor}:{})})}`,signal)
   const source=page.items?.find(row=>row.id===item.object_id)
   if(source){
    if(source.project_id&&source.project_id!==item.project_id)throw new Error("Inbox source belongs to another project")
    return {source,path:`${collection}/${id}`}
   }
   if(!page.has_more||!page.next_cursor||seen.has(page.next_cursor))throw new Error("Inbox source is no longer available")
   cursor=page.next_cursor;seen.add(cursor)
  }
 }
 case "PREVIEW_SHARE_APPROVAL":path=`${project}/preview-shares/${id}`;break
 case "REQUIREMENT_CONFLICT":path=`${project}/work-items/${id}`;break
 case "PLAN_APPROVAL":
  for(let offset=0;;offset+=100){
   const page=await iamApi.deliveryResultPage<Source>({method:"GET",url:`${project}/review-plans?limit=100&offset=${offset}`},signal)
   const source=page.items.find(row=>row.id===item.object_id)
   if(source)return {source,path:`${project}/review-plans/${id}`}
   if(offset+page.items.length>=page.total||page.items.length===0)throw new Error("Inbox source is no longer available")
  }
 case "AGENT_SPAWN_APPROVAL":
  for(let offset=0;;offset+=100){
   const rows=await get<Source[]>(`${team}/agent-spawn-requests?limit=100&offset=${offset}`,signal),source=rows.find(row=>row.id===item.object_id)
   if(source)return {source,path:`${team}/agent-spawn-requests/${id}`}
   if(rows.length<100)throw new Error("Inbox source is no longer available")
  }
 default:throw new Error("Inbox source does not have an inline decision")
 }
 const source=await get<Source>(path,signal)
 if(source.project_id&&item.project_id&&source.project_id!==item.project_id)throw new Error("Inbox source belongs to another project")
 return {source,path}
}
export async function decideInboxSource(tenant:string,item:InboxItem,loaded:Loaded,action:"A"|"R",reason:string,key:string,conflictsAcknowledged=false):Promise<void>{
 const {source,path}=loaded
 const headers={"Idempotency-Key":key,"If-Match":`"${source.version}"`}
 const send=(method:"POST"|"PUT",suffix:string,body:unknown)=>iamApi.deliveryResultRequest<unknown>({method,url:path+suffix},{headers,body:JSON.stringify(body)})
 switch(item.kind){
 case "PLAN_CONFIRMATION":case "REPORT_CONFIRMATION":
  if(!loaded.planning)throw new Error("Planning source unavailable")
  await planningApi.decide(tenant,item.kind==="PLAN_CONFIRMATION"?"plans":"reports",loaded.planning,action==="A"?"CONFIRM":"RETURN",reason,key);return
 case "MEMORY_RULE_APPROVAL":await memoryApi.review(tenant,source.id,source.version,action==="A"?"APPROVED":"REJECTED",reason,key,conflictsAcknowledged);return
 case "DISPATCH_PROPOSAL":await send("POST","/decisions",{decision:action==="A"?"CONFIRM":"DISMISS",reason});return
 case "ENSEMBLE_APPROVAL":await send("POST","/decisions",{version:source.version,action:action==="A"?"APPROVE":"ABANDON",reason});return
 case "AGENT_SPAWN_APPROVAL":await send("PUT","/state",{version:source.version,action:action==="A"?"CREATE":"REJECT",reason});return
 case "PLAN_APPROVAL":await send("POST","/decisions",{version:source.version,content_hash:source.content_hash,action:action==="A"?"APPROVE":"REJECT",feedback:reason});return
 case "QUALITY_ESCAPE":await send("PUT","/learning",{state:action==="A"?"RECORDED":"DISMISSED",reason});return
 case "WORKLOG_APPROVAL":if(action!=="R")throw new Error("Open worklog approval form");await send("PUT","/state",{state:"REJECTED",reason});return
 case "PREVIEW_SHARE_APPROVAL":await send("POST","/decisions",{decision:action==="A"?"APPROVE":"REJECT",reason});return
 case "REQUIREMENT_CONFLICT":if(action!=="R")throw new Error("Open requirement source");await send("POST","/requirement-conflicts",{version:source.version,action:"RESOLVE",reason});return
 default:throw new Error("Inbox source does not have an inline decision")
 }
}

export async function loadInboxClarification(item:InboxItem,signal:AbortSignal):Promise<Clarification> {
 let cursor=""
 const seen=new Set<string>()
 for(;;){
  const page=await clarificationApi.list(item.project_id!,cursor,signal)
  const round=page.items?.find(row=>row.id===item.id&&row.intake_id===item.object_id)
  if(round)return round
  if(!page.has_more||!page.next_cursor||seen.has(page.next_cursor))throw new Error("Inbox source is no longer available")
  cursor=page.next_cursor;seen.add(cursor)
 }
}
