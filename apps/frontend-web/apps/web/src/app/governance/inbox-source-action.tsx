import { useEffect, useRef, useState } from "react"
import { Button } from "@workspace/ui/components/button"
import { useTranslations } from "@workspace/ui/i18n/intl-provider"
import { Alert } from "../../components/alert"
import { type PlanningRecord } from "../goals/api"
import type { InboxItem } from "./api"
import { inlineInboxKinds, loadInboxSource, decideInboxSource, type Loaded } from "./inbox-source-data"

export function InboxSourceAction({tenant,item,onChanged}:{tenant:string;item:InboxItem;onChanged:()=>void}){
 const t=useTranslations("iam"),[loaded,setLoaded]=useState<Loaded|null>(null),[error,setError]=useState(""),[reason,setReason]=useState(""),[busy,setBusy]=useState(false),[conflictsAcknowledged,setConflictsAcknowledged]=useState(false)
 const pending=useRef<{signature:string;key:string}|null>(null),flight=useRef(false)
 const supported=inlineInboxKinds.includes(item.kind)
 useEffect(()=>{if(!supported)return;const c=new AbortController();void loadInboxSource(tenant,item,c.signal).then(v=>{if(!c.signal.aborted){setLoaded(v);setConflictsAcknowledged(false)}},e=>{if(!c.signal.aborted){setLoaded(null);setError(e.message)}});return()=>c.abort()},[tenant,item,supported])
 if(!supported)return null
 const act=async(action:"A"|"R")=>{if(!loaded||flight.current)return;flight.current=true;setBusy(true);setError("");const signature=JSON.stringify([action,reason,loaded.source.version,conflictsAcknowledged]);if(pending.current?.signature!==signature)pending.current={signature,key:crypto.randomUUID()};try{await decideInboxSource(tenant,item,loaded,action,reason,pending.current.key,conflictsAcknowledged);pending.current=null;onChanged()}catch(e){setError(e instanceof Error?e.message:t("govUnavailable"))}finally{setBusy(false);flight.current=false}}
 const source=loaded?.source,minReason=["ENSEMBLE_APPROVAL","AGENT_SPAWN_APPROVAL","REQUIREMENT_CONFLICT"].includes(item.kind)?10:1,primary=!["WORKLOG_APPROVAL","REQUIREMENT_CONFLICT"].includes(item.kind),hasMemoryConflicts=item.kind==="MEMORY_RULE_APPROVAL"&&!!source?.conflict_memory_ids?.length
 const approveLabel=item.kind==="DISPATCH_PROPOSAL"?"dispatchConfirm":item.kind==="AGENT_SPAWN_APPROVAL"?"spawnRequestAction_CREATE":["PLAN_CONFIRMATION","REPORT_CONFIRMATION"].includes(item.kind)?"confirm":item.kind==="QUALITY_ESCAPE"?"qualityRecordLearning":"agentMemoryApprove"
 const rejectLabel=item.kind==="REQUIREMENT_CONFLICT"?"studioResolve":item.kind==="DISPATCH_PROPOSAL"?"dispatchDismiss":item.kind==="ENSEMBLE_APPROVAL"?"heavyAbandon":["PLAN_CONFIRMATION","REPORT_CONFIRMATION"].includes(item.kind)?"resultDo_return":item.kind==="QUALITY_ESCAPE"?"qualityDismissLearning":"agentMemoryReject"
 return <div className="space-y-3">{error&&<Alert>{error}</Alert>}{!source&&!error&&<p role="status">{t("loading")}</p>}{source&&<>
  <p className="text-sm">{t("status")}: {source.state??source.learning_state} / {t("clientRevision")}: {source.version}</p>
  {[source.title,source.body,source.learning_draft?.title,source.learning_draft?.body,source.reason,source.summary,source.note,source.content?.files?.join("\n"),source.content?.risks?.join("\n")].filter(Boolean).map((value,index)=><p key={index} className="whitespace-pre-wrap break-words text-sm">{value}</p>)}
  {source.content?.criteria_coverage?.map(c=><div key={c.criterion_id} className="border-l pl-3 text-sm"><p>{c.criterion_id}</p><p className="whitespace-pre-wrap">{c.approach}</p><p className="whitespace-pre-wrap">{c.verification}</p></div>)}
  {source.recommended_id&&<p className="break-all text-sm">{t("dispatchResult")}: {source.recommended_id}</p>}{source.expires_at&&<p className="text-sm">{t("dispatchExpires")}: <time dateTime={source.expires_at}>{new Date(source.expires_at).toLocaleString()}</time></p>}
  {source.content_hash&&<p className="break-all font-mono text-xs">{source.content_hash}</p>}{source.inputs_digest&&<p className="break-all font-mono text-xs">{source.inputs_digest}</p>}
  {loaded?.planning&&<PlanningPreview record={loaded.planning}/>}
  {hasMemoryConflicts&&<label className="flex items-start gap-2 text-sm"><input type="checkbox" disabled={busy} checked={conflictsAcknowledged} onChange={e=>setConflictsAcknowledged(e.target.checked)}/>{t("agentMemoryAcknowledge")}</label>}
  <label className="grid gap-2 text-sm">{t("clientReason")}<textarea className="min-h-24 rounded-md border bg-background p-3" maxLength={item.kind==="WORKLOG_APPROVAL"?500:2000} disabled={busy} value={reason} onChange={e=>setReason(e.target.value)}/></label>
  <div className="flex flex-wrap gap-2">{primary&&<Button data-inbox-primary disabled={busy||reason.trim().length<minReason||(hasMemoryConflicts&&!conflictsAcknowledged)||(item.kind==="DISPATCH_PROPOSAL"&&!source.recommended_id)} onClick={()=>void act("A")}>{t(approveLabel)}</Button>}<Button data-inbox-secondary variant="outline" disabled={busy||reason.trim().length<minReason} onClick={()=>void act("R")}>{t(rejectLabel)}</Button></div>
 </>}</div>
}

function PlanningPreview({record}:{record:PlanningRecord}){
 const t=useTranslations("iam"),content=record.content
 return <div className="space-y-3 text-sm"><p>{record.period_kind} / {record.period_start}</p>
  {"items" in content&&<ul className="space-y-2">{content.items.map((item,index)=><li key={item.id??index} className="border-l pl-3"><p>{item.title}</p><p className="whitespace-pre-wrap break-words">{item.notes}</p>{item.due_at&&<time dateTime={item.due_at}>{item.due_at}</time>}</li>)}</ul>}
  {"retrospective" in content&&<><p className="whitespace-pre-wrap break-words">{content.status_explanation}</p><p className="whitespace-pre-wrap break-words">{content.uncompleted}</p><p className="whitespace-pre-wrap break-words">{content.retrospective}</p><ul>{content.completed.map(row=><li key={row.work_item_id}>{row.work_item_id}: {row.note}</li>)}</ul><p>{t("status")}: {content.declared_confidence}</p></>}
 </div>
}
