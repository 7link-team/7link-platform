import { useEffect, useRef, useState } from "react"
import { Button } from "@workspace/ui/components/button"
import { useTranslations } from "@workspace/ui/i18n/intl-provider"
import { Alert } from "../../components/alert"
import { iamApi, type WorkItemDetail } from "../../lib/iam-api"
import { useAllowed } from "../../lib/permissions"
import { WorkResults } from "../work-items/results"
import { ReviewAttention } from "../work-items/attention"
import { InboxSourceAction } from "./inbox-source-action"
import { loadInboxClarification } from "./inbox-source-data"
import { type Clarification } from "../project-views/clarification-api"
import { governanceApi, inboxObjectHref, type InboxItem } from "./api"
import type { components } from "../../lib/api-schema"

export function InboxPreview({tenant,item,onChanged,onOpen,revision=0}:{tenant:string;item:InboxItem;onChanged:()=>void;onOpen:()=>void;revision?:number}) {
 const t=useTranslations("iam"), api=governanceApi(tenant)
 const [current,setCurrent]=useState<InboxItem|null>(null),[error,setError]=useState(""),[busy,setBusy]=useState(false)
 const flight=useRef(false)
 useEffect(()=>{const c=new AbortController();void governanceApi(tenant).item(item.kind,item.id,c.signal).then(v=>{if(!c.signal.aborted){setCurrent(v);setError("")}},e=>{if(!c.signal.aborted){setCurrent(null);setError(e.message)}});return()=>c.abort()},[tenant,item.kind,item.id,item.source_revision,revision])
 const acknowledge=async()=>{if(!current||flight.current)return;flight.current=true;setBusy(true);setError("");try{await api.acknowledge(current);onChanged()}catch(e){setError(e instanceof Error?e.message:t("govUnavailable"))}finally{flight.current=false;setBusy(false)}}
 const ack=current&&["CLARIFICATION_REPLY","ESCALATION_ALERT"].includes(current.kind)
 return <section className="min-w-0 space-y-4 rounded-md border p-4" aria-label={t("govPreview")}>
  <h2 className="font-semibold">{t("govPreview")}</h2>
  {error&&<Alert>{error}</Alert>}
  {!current&&!error&&<p role="status">{t("loading")}</p>}
  {current&&<>
   <dl className="grid grid-cols-[auto_minmax(0,1fr)] gap-x-3 gap-y-2 text-sm"><dt>{t("govKind")}</dt><dd>{t(`govKind_${current.kind}`)}</dd><dt>{t("govPriority")}</dt><dd>{t(`govPriority_${current.priority}`)}</dd>{current.reason&&<><dt>{t("govReason")}</dt><dd className="whitespace-pre-wrap break-words">{current.reason}</dd></>}{current.due_at&&<><dt>{t("govDue")}</dt><dd><time dateTime={current.due_at}>{new Date(current.due_at).toLocaleString()}</time></dd></>}</dl>
   {current.project_id&&["WORK_ITEM_REVIEW","HUMAN_VERIFICATION"].includes(current.kind)&&<InboxWork item={current} onChanged={onChanged}/>}
   {current.kind==="INTERACTION"&&current.project_id&&<InboxInteraction item={current} onChanged={onChanged}/>}
   {current.kind==="CLARIFICATION_REPLY"&&current.project_id&&<InboxClarification key={`${current.id}:${current.source_revision}`} item={current}/>}
   <InboxSourceAction key={`${current.kind}:${current.id}`} tenant={tenant} item={current} onChanged={onChanged}/>
   {ack&&<div className="grid gap-2"><Button data-inbox-primary disabled={busy} onClick={()=>void acknowledge()}>{t("govAcknowledge")}</Button></div>}
   {inboxObjectHref(tenant,current)&&<Button data-inbox-primary={!["WORK_ITEM_REVIEW","INTERACTION","ESCALATION_ALERT","MEMORY_RULE_APPROVAL","DISPATCH_PROPOSAL","ENSEMBLE_APPROVAL","AGENT_SPAWN_APPROVAL","PLAN_CONFIRMATION","REPORT_CONFIRMATION","PLAN_APPROVAL","QUALITY_ESCAPE","PREVIEW_SHARE_APPROVAL"].includes(current.kind) ? true : undefined} variant="outline" onClick={onOpen}>{t("govOpenSource")}</Button>}
   <p className="text-xs text-muted-foreground">{t("govAuthorityNote")}</p>
  </>}
 </section>
}
export function ClarificationSummary({round}:{round:Clarification}) {
 const t=useTranslations("iam")
 return <section className="grid min-w-0 gap-3" aria-label={t("clarificationTitle")}><h3 className="font-medium">{t("clarificationTitle")}</h3>{(round.questions??[]).map(question=><dl key={question.number} className="grid min-w-0 gap-2 border-l-2 pl-3"><dt className="whitespace-pre-wrap break-words font-medium">{question.number}. {question.text}</dt><dd className="whitespace-pre-wrap break-words"><span className="text-sm text-muted-foreground">{t("clarificationAnswer")}: </span>{question.answer??"—"}</dd></dl>)}</section>
}
function InboxClarification({item}:{item:InboxItem}) {
 const t=useTranslations("iam"),[round,setRound]=useState<Clarification|null>(null),[error,setError]=useState("")
 useEffect(()=>{const c=new AbortController();void loadInboxClarification(item,c.signal).then(value=>{if(!c.signal.aborted)setRound(value)},()=>{if(!c.signal.aborted)setError(t("govUnavailable"))});return()=>c.abort()},[item,t])
 return error?<Alert>{error}</Alert>:round?<ClarificationSummary round={round}/>:<p role="status">{t("loading")}</p>
}
function InboxWork({item,onChanged}:{item:InboxItem;onChanged:()=>void}) {
 const t=useTranslations("iam"),[detail,setDetail]=useState<WorkItemDetail|null>(null),[error,setError]=useState("")
 useEffect(()=>{const c=new AbortController();void iamApi.workItem(item.project_id!,item.object_id,c.signal).then(v=>{if(!c.signal.aborted)setDetail(v)},e=>{if(!c.signal.aborted)setError(e.message)});return()=>c.abort()},[item.project_id,item.object_id])
 return error?<Alert>{error}</Alert>:detail?<WorkResults entityID={item.project_id!} detail={detail} surface="INBOX" onUpdated={v=>{setDetail(v);onChanged()}}/>:<p role="status">{t("loading")}</p>
}
function InboxInteraction({item,onChanged}:{item:InboxItem;onChanged:()=>void}) {
 const t=useTranslations("iam"),allowed=useAllowed(),[detail,setDetail]=useState<components["schemas"]["ExecutionDetail"]|null>(null),[reason,setReason]=useState(""),[error,setError]=useState(""),[busy,setBusy]=useState(false),flight=useRef(false)
 useEffect(()=>{const c=new AbortController();void iamApi.execution(item.object_id,c.signal).then(v=>{if(!c.signal.aborted){if(v.execution.project_id!==item.project_id){setError(t("executionWrongProject"));return}setDetail(v)}},e=>{if(!c.signal.aborted)setError(e.message)});return()=>c.abort()},[item.object_id,item.project_id,t])
 const approval=detail?.approvals?.find(v=>v.id===item.id)
 const decide=async(state:"ALLOW"|"DENY")=>{if(!approval||flight.current||!reason.trim())return;flight.current=true;setBusy(true);setError("");try{await iamApi.decideExecutionPermission(item.object_id,item.id,state,reason);onChanged()}catch(e){setError(e instanceof Error?e.message:t("executionUnavailable"))}finally{flight.current=false;setBusy(false)}}
 return <div className="space-y-3">{error&&<Alert>{error}</Alert>}{approval&&<><h3 className="font-medium">{approval.tool}</h3><pre className="max-h-56 overflow-auto whitespace-pre-wrap break-words text-sm">{JSON.stringify(approval.input,null,2)}</pre>{detail?.execution.work_item_id&&<ReviewAttention project={item.project_id!} work={detail.execution.work_item_id} claim="" interaction={item.id} surface="INBOX"/>}<label className="grid gap-2 text-sm">{t("executionPermissionReason")}<textarea className="min-h-24 rounded-md border bg-background p-3" maxLength={2000} value={reason} disabled={busy} onChange={e=>setReason(e.target.value)}/></label><div className="flex flex-wrap gap-2"><Button data-inbox-primary disabled={busy||!reason.trim()||!allowed("interaction_respond")} onClick={()=>void decide("ALLOW")}>{t("executionPermissionAllow")}</Button><Button data-inbox-secondary variant="outline" disabled={busy||!reason.trim()||!allowed("interaction_respond")} onClick={()=>void decide("DENY")}>{t("executionPermissionDeny")}</Button></div></>}</div>
}
