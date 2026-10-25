import { PackageBundle } from "./package-bundle"
import { PackageSend } from "./package-send"
import {AcceptanceStatus} from "./acceptance"
import { useContext, useEffect, useRef, useState } from "react"
import { useParams, useSearchParams } from "react-router"
import { AutoForm, type FormConfig, type FieldRendererProps } from "@workspace/ui/components/auto-form"
import { AutoTable } from "@workspace/ui/components/auto-table"
import { Button } from "@workspace/ui/components/button"
import { useTranslations } from "@workspace/ui/i18n/intl-provider"
import { ReauthenticationDialog } from "../../components/reauthentication-dialog"
import { Alert } from "../../components/alert"
import { PageHeader } from "../../components/page-header"
import { useAllowed } from "../../lib/permissions"
import { clientworkApi, type ClientCollection, type ClientRecords, type ClientPage, textLines, splitReferenceIDs, approvedChangeDelta } from "../../lib/clientwork-api"
import { ReferencePicker, ArtifactPicker } from "./client-pickers"
import { iamApi, type FileEntryView, type WorkItem } from "../../lib/iam-api"
import { scopeBody, type ScopeValues } from "../requirements/changes-api"
import { ClientPickerScope } from "./client-picker-scope"
import { IntakeChange } from "./intake"
import { Clarifications } from "./clarifications"
import { TestReports } from "./test-reports"
import { TestCaseImport, TestCaseMetadata } from "./test-case-import"
import { PackageAcceptance } from "./package-acceptance"
import { PackageArtifactDisclosure } from "./package-evidence"
import {ProjectSettingsDetails} from "./settings"
import { ReleaseNotes } from "./release-notes"
import type { ProjectSurface } from "../../lib/project-surfaces"
type Row = ClientRecords[ClientCollection]
type Mutation = (body: Record<string, unknown>, key: string) => Promise<unknown>
export function MutationForm({fields,initialData,submit,label}:{fields:FormConfig;initialData?:Record<string,unknown>;submit:Mutation;label:string}) {
  const t=useTranslations("iam")
  const [busy,setBusy]=useState(false),[error,setError]=useState("")
  const pending=useRef<{signature:string;key:string}|null>(null)
  return <div className="grid gap-2">{error&&<Alert>{error}</Alert>}<AutoForm fields={fields} mode={initialData ? "edit" : "add"} initialData={initialData} onSubmit={async values=>{if(busy)return;setBusy(true);setError("");const signature=JSON.stringify(values);if(pending.current?.signature!==signature)pending.current={signature,key:crypto.randomUUID()};try{await submit(values,pending.current.key);pending.current=null}catch(e){setError(e instanceof Error?e.message:t("clientSaveFailed"))}finally{setBusy(false)}}}><Button type="submit" disabled={busy}>{busy?t("clientSaving"):label}</Button></AutoForm></div>
}
function valueIDs(value:unknown):string[]{return Array.isArray(value)?value as string[]:splitReferenceIDs(value)}
function Editor({collection,onSave,initialData,editing=false}:{collection:ClientCollection;onSave:Mutation;initialData?:Record<string,unknown>;editing?:boolean}) {
 const t=useTranslations("iam")
 const title={name:t("clientTitle"),type:"text" as const,required:true},description={name:t("clientDescription"),type:"textarea" as const,required:true}
 const refs={name:t("clientRequirements"),type:"custom" as const,component:ReferencePicker,required:true}
 let fields:FormConfig
 let body:(v:Record<string,unknown>)=>Record<string,unknown>=v=>v
 switch(collection){
 case "sheets":fields={title,requirement_ids:refs,signer_id:{name:t("clientSigner"),type:"text",required:true},source_refs:{name:t("clientSources"),type:"textarea"},milestones:{name:t("clientMilestones"),type:"textarea"},out_of_scope:{name:t("clientOutOfScope"),type:"textarea"}};body=v=>({...v,requirement_ids:valueIDs(v.requirement_ids),source_refs:textLines(v.source_refs),milestones:textLines(v.milestones),out_of_scope:textLines(v.out_of_scope)});break
 case "changes":fields={sheet_id:{name:t("clientSheet"),type:"text",required:true},base_revision:{name:t("clientRevision"),type:"number",required:true},description};body=v=>({...v,base_revision:Number(v.base_revision)});break
 case "issues":fields={title,description};break
 case "packages":fields={title,release_notes:{name:t("clientReleaseNotes"),type:"textarea",required:true},recipient_id:{name:t("clientRecipient"),type:"text",required:true},artifacts:{name:t("clientArtifacts"),type:"custom",component:ArtifactPicker,required:true}};break
 case "previews":return <PreviewEditor onSave={onSave} />
 case "shares":fields={preview_id:{name:t("clientSharePreview"),type:"text",required:true},recipient_id:{name:t("clientRecipient"),type:"text",required:true},ttl_hours:{name:t("clientShareTTL"),type:"number",required:true,value:24},note:{name:t("clientShareNote"),type:"textarea",required:true}};body=v=>({...v,ttl_hours:Number(v.ttl_hours)});break
 case "cases":fields={title,steps:{name:t("clientSteps"),type:"textarea",required:true},expected:{name:t("clientExpected"),type:"textarea",required:true},work_item_id:{name:t("clientWorkItem"),type:"text",required:true},defect_id:{name:t("clientDefect"),type:"text"}};body=v=>({...v,defect_id:v.defect_id||undefined});break
 case "plans":fields={title,case_ids:{name:t("clientCases"),type:"custom",component:ReferencePicker,required:true}};body=v=>({...v,case_ids:valueIDs(v.case_ids)});break
 default:return null
 }
 return <section className="grid gap-3 border-t pt-4"><h2 className="font-medium">{t(editing?"clientSave":"clientCreate")}</h2><MutationForm fields={fields} initialData={initialData} submit={(values,key)=>onSave(body(values),key)} label={t(editing?"clientSave":"clientCreate")}/></section>
}
export default function ClientWorkflowPage({surface}:{surface:ProjectSurface}) {
 const {tenantID="",entityID:project=""}=useParams(),t=useTranslations("iam"),allowed=useAllowed()
 const [params,setParams]=useSearchParams()
 const initial:ClientCollection=surface==="baseline"?"sheets":surface==="changes"?"changes":surface==="feedback"?"issues":surface==="deliveries"?"packages":surface==="test-cases"?"cases":"previews"
 const section=params.get("collection")
 const collection:ClientCollection=surface==="test-cases"&&(section==="cases"||section==="plans"||section==="runs")?section:surface==="previews"&&(section==="previews"||section==="comments"||section==="shares")?section:initial
 const [page,setPage]=useState<ClientPage<Row>>({items:[],has_more:false})
 const [detail,setDetail]=useState<Row|null>(null),[loading,setLoading]=useState(false),[error,setError]=useState("")
 const [access,setAccess]=useState<{external:boolean;principal_id:string}|null>(null)
 const [revision,setRevision]=useState(0),[showCreate,setShowCreate]=useState(false)
 const selected=params.get("id")??"",cursor=params.get("cursor")??"",sheetRevision=Number(params.get("revision"))||0,replyCursor=params.get("reply_cursor")??""
 const scope=`${tenantID}:${project}:${collection}`
 const canRead=allowed(surface==="test-cases"?"project_read":"channel_read")
 const requestKey=JSON.stringify([scope,cursor,revision,canRead])
 const [previousRequest,setPreviousRequest]=useState("")
 if(previousRequest!==requestKey){setPreviousRequest(requestKey);setPage({items:[],has_more:false});setError("");setLoading(canRead);setDetail(null)}
 const [previousDetail,setPreviousDetail]=useState(`${selected}:${sheetRevision}:${replyCursor}`)
 if(previousDetail!==`${selected}:${sheetRevision}:${replyCursor}`){setPreviousDetail(`${selected}:${sheetRevision}:${replyCursor}`);setDetail(null)}
 useEffect(()=>{const c=new AbortController();if(!canRead)return()=>c.abort();void clientworkApi.access(project,c.signal).then(v=>{if(!c.signal.aborted)setAccess(v)}).catch(e=>{if(!c.signal.aborted)setError(String(e))});return()=>c.abort()},[project,tenantID,canRead,revision])
 const canCreate=access && (collection!=="changes" || access.external) && (collection==="issues"||collection==="changes"?canRead:allowed(collection==="sheets"?"requirement_write":collection==="packages"?"delivery_compose":(collection==="previews"||collection==="shares")?"project_manage":collection==="cases"?"test_case_manage":"test_plan_manage")) && (!access.external || collection==="issues" || collection==="changes")
 useEffect(()=>{const c=new AbortController();if(!canRead){return()=>c.abort()};void clientworkApi.list(project,collection,cursor,c.signal).then(v=>{if(!c.signal.aborted)setPage(v)}).catch(e=>{if(!c.signal.aborted)setError(e instanceof Error?e.message:t("clientLoadFailed"))}).finally(()=>{if(!c.signal.aborted)setLoading(false)});return()=>c.abort()},[scope,project,collection,cursor,revision,canRead,t])
 useEffect(()=>{const c=new AbortController();if(!selected||!canRead)return()=>c.abort();void clientworkApi.get(project,collection,selected,c.signal,sheetRevision,replyCursor).then(v=>{if(!c.signal.aborted)setDetail(v)}).catch(e=>{if(!c.signal.aborted)setError(e instanceof Error?e.message:t("clientLoadFailed"))});return()=>c.abort()},[scope,project,collection,selected,revision,sheetRevision,replyCursor,canRead,t])
 const refresh=()=>setRevision(v=>v+1)
 const select=(id:string)=>setParams(old=>{const next=new URLSearchParams(old);next.delete("revision");next.delete("reply_cursor");if(id)next.set("id",id);else next.delete("id");return next})
 const command:Mutation=async(body,key)=>{await clientworkApi.command(project,collection,body,key);setShowCreate(false);refresh()}
 const tabs:ClientCollection[]=surface==="test-cases"?["cases","plans","runs"]:surface==="previews"?["previews","comments","shares"]:[]
 return <ClientPickerScope.Provider value={{project,collection:collection==="plans"?"cases":collection}}><div className="flex min-h-0 min-w-0 flex-1 flex-col gap-3 overflow-auto p-1">
 <PageHeader title={t(`clientCollection_${collection}`)} />
 {surface==="baseline"&&canRead&&access&&<Clarifications key={`${scope}:${access.principal_id}`} project={project} external={access.external}/>}
 {!canRead?<p role="status">{t("projectSurfaceAccessUnavailable")}</p>:<>
 {!!tabs.length&&<nav aria-label={t("clientSections")} className="flex gap-2">{tabs.map(tab=><Button key={tab} variant={tab===collection?"default":"outline"} onClick={()=>{setParams({collection:tab});setShowCreate(false)}}>{t(`clientCollection_${tab}`)}</Button>)}</nav>}
 {error&&<Alert>{error}</Alert>}
 <div className="flex flex-wrap gap-2"><Button variant="outline" onClick={refresh} disabled={loading}>{t("clientRetry")}</Button>{canCreate&&!["runs","comments"].includes(collection)&&<Button onClick={()=>setShowCreate(v=>!v)}>{showCreate?t("clientCancel"):t("clientCreate")}</Button>}</div>
 {canCreate&&collection==="cases"&&<TestCaseImport key={scope} project={project} onImported={refresh}/>}
 <AutoTable data={loading?[]:page.items.map(row=>({id:row.id!,title:"title"in row?row.title:"body"in row?row.body:row.id!,state:"state"in row?row.state:"result"in row?row.result:"",version:"version"in row?row.version:""}))} rowKey="id" loading={loading} pagination={false} exportable={false} emptyContent={t("clientEmpty")} columns={[{field:"title",name:t("clientTitle"),render:(_,row)=><button type="button" className="text-left underline underline-offset-4" onClick={()=>select(row.id)}>{row.title}</button>},{field:"state",name:t("status")},{field:"version",name:t("clientVersion")}]} />
 <div className="flex gap-2"><Button variant="outline" disabled={loading||!cursor} onClick={()=>setParams(old=>{const next=new URLSearchParams(old);next.delete("cursor");next.delete("id");return next})}>{t("clientFirstPage")}</Button><Button variant="outline" disabled={loading||!page.has_more} onClick={()=>setParams(old=>{const next=new URLSearchParams(old);next.set("cursor",page.next_cursor!);next.delete("id");return next})}>{t("fleetNext")}</Button></div>
 {showCreate&&<Editor key={scope} collection={collection} onSave={command}/>}
 {selected&&<section className="grid gap-4 border-t pt-4" aria-busy={!detail&&!error}><div className="flex justify-between gap-3"><h2 className="font-medium">{t("clientDetail")}</h2><Button variant="outline" onClick={()=>select("")}>{t("personalWorkClose")}</Button></div>{detail&&collection==="issues"&&<nav className="flex gap-2"><Button variant="outline" disabled={!replyCursor} onClick={()=>setParams(old=>{const next=new URLSearchParams(old);next.delete("reply_cursor");return next})}>{t("clientFirstPage")}</Button><Button variant="outline" disabled={!(detail as ClientRecords["issues"]).replies_has_more} onClick={()=>setParams(old=>{const next=new URLSearchParams(old);next.set("reply_cursor",(detail as ClientRecords["issues"]).replies_next_cursor!);return next})}>{t("fleetNext")}</Button></nav>}{detail&&collection==="sheets"&&<nav className="flex items-center gap-2" aria-label={t("clientRevision")}><Button variant="outline" disabled={(detail as ClientRecords["sheets"]).revision<=1} onClick={()=>setParams(old=>{const next=new URLSearchParams(old);next.set("revision",String((detail as ClientRecords["sheets"]).revision-1));return next})}>{t("clientRevision")} -1</Button><Button variant="outline" disabled={!sheetRevision} onClick={()=>setParams(old=>{const next=new URLSearchParams(old);next.delete("revision");return next})}>{t("clientRetry")}</Button></nav>}{detail&&<ClientDetail key={`${scope}:${selected}:${revision}:${sheetRevision}`} project={project} collection={collection} detail={detail} access={access} historical={sheetRevision>0} onUpdated={refresh}/>}</section>}
 </>}
 </div></ClientPickerScope.Provider>
}
function ClientDetail({project,collection,detail,access,historical=false,onUpdated}:{project:string;collection:ClientCollection;detail:Row;access:{external:boolean;principal_id:string}|null;historical?:boolean;onUpdated:()=>void}) {
 const t=useTranslations("iam"),allowed=useAllowed()
 const mutate=(body:unknown,key:string,action:string,patch=false)=>clientworkApi.command(project,collection,body,key,detail.id!,action||undefined,"version"in detail?detail.version:undefined,patch).then(onUpdated)
 const textField=(name:string)=>({name:t(name),type:"textarea"as const,required:true})
 if(collection==="sheets"){
 const row=detail as ClientRecords["sheets"]
 return <><h3 className="font-medium">{row.title} / {t("clientRevision")} {row.revision} / {row.state}</h3><p className="break-all text-xs text-muted-foreground">{row.content_hash}</p>{row.content.items?.map(item=><section key={item.id} className="grid gap-2"><h4 className="font-medium">{item.title}</h4><p className="whitespace-pre-wrap">{item.description}</p><ul className="list-disc pl-5">{item.acceptance_criteria?.map(c=><li key={c.id}>{c.text}</li>)}</ul></section>)}<TextList title={t("clientMilestones")} items={row.content.milestones}/><TextList title={t("clientOutOfScope")} items={row.content.out_of_scope}/><TextList title={t("clientSources")} items={row.content.source_refs}/>{row.decision&&<p>{row.decision.decision}: {row.decision.reason}</p>}
 {!historical&&allowed("requirement_write")&&row.state==="DRAFT"&&<MutationForm fields={{}} label={t("clientPublish")} submit={(_,key)=>mutate({},key,"publish")}/>}
 {!historical&&allowed("requirement_write")&&<details><summary className="cursor-pointer py-2">{t("clientNewRevision")}</summary><MutationForm fields={{title:{name:t("clientTitle"),type:"text",required:true},requirement_ids:{name:t("clientRequirements"),type:"custom",component:ReferencePicker,required:true},signer_id:{name:t("clientSigner"),type:"text",required:true},milestones:{name:t("clientMilestones"),type:"textarea"},out_of_scope:{name:t("clientOutOfScope"),type:"textarea"},source_refs:{name:t("clientSources"),type:"textarea"}}} initialData={{title:row.title,requirement_ids:row.content.items?.map(v=>v.id),signer_id:row.signer_id,milestones:row.content.milestones?.join("\n"),out_of_scope:row.content.out_of_scope?.join("\n"),source_refs:row.content.source_refs?.join("\n")}} label={t("clientNewRevision")} submit={(v,key)=>mutate({...v,requirement_ids:valueIDs(v.requirement_ids),source_refs:textLines(v.source_refs),milestones:textLines(v.milestones),out_of_scope:textLines(v.out_of_scope)},key,"versions")}/></details>}
 {!historical&&row.version>0&&access?.external&&access.principal_id===row.signer_id&&row.state==="AWAITING_SIGNOFF"&&<MutationForm fields={{decision:{name:t("clientDecision"),type:"select",required:true,config:{options:[{value:"SIGN",label:t("clientSign")},{value:"REJECT",label:t("clientReject")}]}},reason:textField("clientReason")}} label={t("clientSubmitDecision")} submit={(v,key)=>mutate({...v,revision:row.revision,content_hash:row.content_hash},key,"signoffs")}/>}
 {!historical&&access?.external&&row.state==="SIGNED"&&<MutationForm fields={{description:textField("clientChangeDescription")}} label={t("clientRequestChange")} submit={(v,key)=>clientworkApi.command(project,"changes",{sheet_id:row.id,base_revision:row.revision,description:v.description},key).then(onUpdated)}/>}
 </>
 }
 if(collection==="changes"){
 const row=detail as ClientRecords["changes"]
 return <><p className="whitespace-pre-wrap">{row.description}</p><p>{row.state}</p><p className="whitespace-pre-wrap">{row.impact}</p>{row.proposal&&<section className="grid gap-2"><p>{row.proposal.title}</p>{row.proposal.tolerance_delta&&<dl className="grid gap-1"><dt>{t("toleranceDeltaDays")}</dt><dd>{row.proposal.tolerance_delta.schedule_delta_days}</dd><dt>{t("toleranceDeltaCost")}</dt><dd>{row.proposal.tolerance_delta.cost_delta_micros}</dd><dt>{t("toleranceDeltaScope")}</dt><dd>{row.proposal.tolerance_delta.scope_delta_criteria}</dd></dl>}<p className="whitespace-pre-wrap">{row.proposal.description}</p><pre className="overflow-auto whitespace-pre-wrap text-xs">{JSON.stringify(row.proposal.criteria,null,2)}</pre></section>}{row.state==="REQUESTED"&&allowed("requirement_write")&&<MutationForm fields={{impact:textField("clientImpact"),requirement_id:{name:t("clientRequirements"),type:"text",required:true},requirement_version:{name:t("clientVersion"),type:"number",required:true},base_version:{name:t("clientRevision"),type:"number",required:true},description:textField("clientDescription"),schedule_delta_days:{name:t("toleranceDeltaDays"),type:"number"},cost_delta_micros:{name:t("toleranceDeltaCost"),type:"number"},scope_delta_criteria:{name:t("toleranceDeltaScope"),type:"number"},action:{name:t("scopeCriterionAction"),type:"select",value:"NONE",config:{options:["NONE","ADD","RETIRE","SUPERSEDE"].map(value=>({value,label:t(`scopeAction_${value}`)}))}},criterion_id:{name:t("scopeCriterion"),type:"text",required:true,show:v=>v?.action==="RETIRE"||v?.action==="SUPERSEDE"},text:{name:t("workCriterionText"),type:"textarea",required:true,show:v=>v?.action==="ADD"||v?.action==="SUPERSEDE"},required:{name:t("requirementRequired"),type:"checkbox",value:true,show:v=>v?.action==="ADD"||v?.action==="SUPERSEDE"}}} label={t("clientAssess")} submit={(v,key)=>mutate({impact:v.impact,action:"ASSESS",proposal:{tolerance_delta:approvedChangeDelta(v),requirement_id:v.requirement_id,requirement_version:Number(v.requirement_version),change:{base_version_no:Number(v.base_version),description:v.description,summary:String(v.impact).slice(0,2000),criteria:scopeBody({current_version_no:Number(v.base_version)},v as ScopeValues).criteria}}},key,"actions")}/>} {access?.external&&row.state==="AWAITING_CONFIRMATION"&&<MutationForm fields={{action:{name:t("clientDecision"),type:"select",required:true,config:{options:[{value:"CONFIRM",label:t("clientConfirm")},{value:"REJECT",label:t("clientReject")}]}}}} label={t("clientSubmitDecision")} submit={(v,key)=>mutate({...v,proposal_hash:row.proposal_hash},key,"actions")}/>} {!access?.external&&allowed("scope_change")&&row.state==="CONFIRMED"&&row.proposal&&<ApplyClientChange project={project} row={row} onUpdated={onUpdated}/>} {!access?.external&&allowed("scope_change")&&row.id&&<IntakeChange project={project} change={row.id} canFreeze={Boolean(row.proposal)&&["AWAITING_CONFIRMATION","CONFIRMED"].includes(row.state)} confirmed={row.state==="CONFIRMED"} onUpdated={onUpdated}/>}</>
 }
 if(collection==="issues"){
 const row=detail as ClientRecords["issues"]
 return <><h3>{row.title}</h3><p className="whitespace-pre-wrap">{row.description}</p><p>{row.state}</p>{row.replies?.map(reply=><p key={reply.id} className="whitespace-pre-wrap border-l-2 pl-3">{reply.body}</p>)}{allowed("project_manage")&&row.state!=="CLOSED"&&<MutationForm fields={{state:{name:t("clientIssueState"),type:"select",required:true,config:{options:[{value:"TRIAGED",label:t("clientTriaged")},{value:"RESPONDED",label:t("clientResponded")},{value:"CLOSED",label:t("clientClosed") }]}},reply:{name:t("clientPublicReply"),type:"textarea"},internal_defect_id:{name:t("clientDefect"),type:"text"}}} label={t("clientSave")} submit={(v,key)=>mutate({...v,internal_defect_id:v.internal_defect_id||undefined},key,"actions")}/>}</>
 }
 if(collection==="packages") return <PackageLifecycle project={project} row={detail as ClientRecords["packages"]} access={access} mutate={mutate} onChanged={onUpdated}/>
 if(collection==="shares"){
  const row=detail as ClientRecords["shares"]
  return <><p>{row.note}</p><p>{row.state} {row.expires_at}</p><p role="status">{t("clientShareReadOnly")}</p>{!access?.external&&row.state==="PENDING_APPROVAL"&&allowed("preview_share_approve")&&<MutationForm fields={{decision:{name:t("clientDecision"),type:"select",required:true,config:{options:[{value:"APPROVE",label:t("clientConfirm")},{value:"REJECT",label:t("clientReject") }]}},reason:textField("clientReason")}} label={t("clientSubmitDecision")} submit={(v,key)=>mutate(v,key,"decisions")}/>} {!access?.external&&["ACTIVE","PENDING_APPROVAL"].includes(row.state)&&allowed("project_manage")&&<MutationForm fields={{reason:textField("clientReason")}} label={t("clientShareRevoke")} submit={(v,key)=>mutate({...v,decision:"REVOKE"},key,"decisions")}/>} {row.state==="ACTIVE"&&row.preview&&<><h3>{row.preview.title}</h3><p>{t(row.preview.source_stage==="PROGRESS"?"clientProgressNotice":"clientAcceptedWork")}</p><Button variant="outline" onClick={()=>void navigator.clipboard.writeText(window.location.href)}>{t("clientShareCopy")}</Button><ArtifactContent project={project} collection="shares" id={row.id!} name={row.preview.title}/></>}</>
 }
 if(collection==="previews"){
 const row=detail as ClientRecords["previews"]
 return <><h3>{row.title}</h3><p role="status">{t(row.source_stage === "PROGRESS" ? "clientProgressNotice" : "clientAcceptedWork")}</p><p className="break-all text-xs">{row.content_hash}</p><ArtifactContent project={project} collection="previews" id={row.id!} name={row.title}/><MutationForm fields={{kind:{name:t("clientAnchorKind"),type:"select",required:true,config:{options:[{value:"TEXT",label:t("clientTextAnchor")},{value:"IMAGE",label:t("clientImageAnchor")},{value:"PAGE",label:t("clientPageAnchor")}]}},start:{name:t("clientAnchorStart"),type:"number"},end:{name:t("clientAnchorEnd"),type:"number"},page:{name:t("clientAnchorPage"),type:"number"},x:{name:"X (0-1)",type:"number"},y:{name:"Y (0-1)",type:"number"},body:textField("clientComment"),client_visible:{name:t("clientVisible"),type:"checkbox"}}} label={t("clientAddComment")} submit={(v,key)=>clientworkApi.command(project,"comments",{preview_id:row.id,file_version_id:row.file_version_id,content_hash:row.content_hash,anchor:{kind:v.kind,start:Number(v.start||0),end:Number(v.end||0),page:Number(v.page||0),x:Number(v.x||0),y:Number(v.y||0)},body:v.body,client_visible:v.client_visible===true},key).then(onUpdated)}/><MutationForm fields={{title:{name:t("clientTitle"),type:"text",required:true},description:textField("clientDescription")}} label={t("clientReportIssue")} submit={(v,key)=>clientworkApi.command(project,"issues",{...v,preview_id:row.id},key).then(onUpdated)}/></>
 }
 if(collection==="comments"){const row=detail as ClientRecords["comments"];return <><p className="whitespace-pre-wrap">{row.body}</p><p>{row.anchor.kind} / {row.anchor.start}-{row.anchor.end} / {row.anchor.page}</p><p className="break-all text-xs">{row.content_hash}</p></>}
 if(collection==="cases"){const row=detail as ClientRecords["cases"];return <><h3>{row.title}</h3><TestCaseMetadata project={project} row={row} canEdit={allowed("test_case_manage")} onUpdated={onUpdated}/><p className="whitespace-pre-wrap">{row.steps}</p><p className="whitespace-pre-wrap">{row.expected}</p>{allowed("test_case_manage")&&<MutationForm fields={{title:{name:t("clientTitle"),type:"text",required:true},steps:textField("clientSteps"),expected:textField("clientExpected"),work_item_id:{name:t("clientWorkItem"),type:"text",required:true},defect_id:{name:t("clientDefect"),type:"text"}}} initialData={row} label={t("clientSave")} submit={(v,key)=>mutate({...v,defect_id:v.defect_id||undefined},key,"",true)}/>}</>}
 if(collection==="plans"){const row=detail as ClientRecords["plans"];return <><h3>{row.title}</h3><p>{t("clientFrozenPlan")}</p><TestReports project={project} plan={row.id!} title={row.title} canManage={allowed("test_plan_manage")}/>{row.cases?.map(c=><section key={c.id}><h4>{c.title} / {c.version}</h4><TestCaseMetadata project={project} row={c}/><p className="whitespace-pre-wrap">{c.steps}</p><p className="whitespace-pre-wrap">{c.expected}</p>{allowed("test_run_execute")&&<MutationForm fields={{result:{name:t("clientRunResult"),type:"select",required:true,config:{options:[{value:"PASSED",label:t("clientPassed")},{value:"FAILED",label:t("clientFailed")},{value:"BLOCKED",label:t("clientBlocked")}] }},evidence:textField("clientManualEvidence"),defect_id:{name:t("clientDefect"),type:"text",show:v=>!v?.create_defect},...(allowed("work_item_write")?{
create_defect:{name:t("testFailureCreateDefect"),type:"checkbox" as const,show:(v:Record<string,unknown>|undefined)=>v?.result==="FAILED"},
defect_title:{name:t("clientTitle"),type:"text" as const,required:true,show:(v:Record<string,unknown>|undefined)=>v?.result==="FAILED"&&v.create_defect===true},
defect_owner:{name:t("workOwner"),type:"text" as const,required:true,show:(v:Record<string,unknown>|undefined)=>v?.result==="FAILED"&&v.create_defect===true},
defect_severity:{name:t("workSeverity"),type:"select" as const,required:true,config:{options:["FATAL","CRITICAL","NORMAL","MINOR"].map(value=>({value,label:t(`artifactCommentSeverity_${value}`)}))},show:(v:Record<string,unknown>|undefined)=>v?.result==="FAILED"&&v.create_defect===true},
defect_priority:{name:t("workPriority"),type:"select" as const,required:true,config:{options:["URGENT","HIGH","MEDIUM","LOW"].map(value=>({value,label:t(`artifactCommentPriority_${value}`)}))},show:(v:Record<string,unknown>|undefined)=>v?.result==="FAILED"&&v.create_defect===true},
defect_reproducibility:{name:t("workReproducibility"),type:"select" as const,required:true,config:{options:["ALWAYS","HIGH","MEDIUM","LOW","UNABLE"].map(value=>({value,label:t(`testFailureRepro_${value}`)}))},show:(v:Record<string,unknown>|undefined)=>v?.result==="FAILED"&&v.create_defect===true}
}:{})}} initialData={{defect_title:c.title}} label={t("clientRecordRun")} submit={(v,key)=>mutate({result:v.result,evidence:v.evidence,case_id:c.id,case_version:c.version,...(v.result==="FAILED"&&v.create_defect===true?{defect:{title:v.defect_title,accountable_principal_id:v.defect_owner,severity:v.defect_severity,priority:v.defect_priority,reproducibility:v.defect_reproducibility}}:{defect_id:v.defect_id||undefined})},key,"runs")}/>}</section>)}</>}
 const row=detail as ClientRecords["runs"];return <><p>{row.result} / {t("clientManualRun")}</p><p className="whitespace-pre-wrap">{row.evidence}</p></>
}
function TextList({title,items}:{title:string;items:string[]|null}){return <section><h3 className="font-medium">{title}</h3><ul className="list-disc pl-5">{items?.map((item,i)=><li key={i}>{item}</li>)}</ul></section>}
export function ClientProjectSettings(){const{entityID:project=""}=useParams(),t=useTranslations("iam"),allowed=useAllowed(),canRead=allowed("project_manage");const[settings,setSettings]=useState<Awaited<ReturnType<typeof clientworkApi.settings>>|null>(null),[error,setError]=useState(""),[revision,setRevision]=useState(0);const[previousRevision,setPreviousRevision]=useState(revision);if(previousRevision!==revision){setPreviousRevision(revision);setSettings(null);setError("")};useEffect(()=>{const c=new AbortController();if(!canRead)return()=>c.abort();void clientworkApi.settings(project,c.signal).then(v=>{if(!c.signal.aborted)setSettings(v)}).catch(e=>{if(!c.signal.aborted)setError(String(e))});return()=>c.abort()},[project,revision,canRead]);return <div className="grid gap-4 overflow-auto"><PageHeader title={t("projectSurfaceSettings")}/>{!canRead&&<p role="status">{t("projectSurfaceAccessUnavailable")}</p>}{error&&<Alert>{error}</Alert>}{error&&<Button variant="outline" onClick={()=>setRevision(v=>v+1)}>{t("clientRetry")}</Button>}{canRead&&settings&&<MutationForm key={`${project}:${revision}`} fields={{name:{name:t("clientTitle"),type:"text",required:true},description:{name:t("clientDescription"),type:"textarea"},target_date:{name:t("clientTargetDate"),type:"text"}}} initialData={{name:settings.name,description:settings.description??"",target_date:settings.target_date??""}} label={t("clientSave")} submit={(v,key)=>clientworkApi.patchSettings(project,{name:String(v.name??""),description:String(v.description??""),target_date:String(v.target_date??"")},settings.version,key).then(()=>setRevision(v=>v+1))}/>}{canRead&&settings&&<ProjectSettingsDetails key={`details:${project}:${revision}`} project={project} settings={settings} onSaved={()=>setRevision(v=>v+1)}/>}</div>}

function ArtifactContent({project,collection,id,versionID,name}:{project:string;collection:"packages"|"previews"|"shares";id:string;versionID?:string;name:string}){
 const t=useTranslations("iam"),[busy,setBusy]=useState(false),[error,setError]=useState("")
 const [content,setContent]=useState<{url:string;type:string;text?:string}|null>(null)
 useEffect(()=>()=>{if(content)URL.revokeObjectURL(content.url)},[content])
 useEffect(()=>{
  if(collection!=="shares"||!content)return
  const c=new AbortController()
  const timer=setInterval(()=>{void clientworkApi.get(project,"shares",id,c.signal).then(row=>{if(!c.signal.aborted&&(row.state!=="ACTIVE"||!row.preview)){setContent(null);setError(t("clientShareExpired"))}}).catch(e=>{if(!c.signal.aborted){setContent(null);setError(String(e))}})},1000)
  return()=>{clearInterval(timer);c.abort()}
 },[project,collection,id,content,t])
 const load=async(download:boolean)=>{setBusy(true);setError("");try{const blob=await clientworkApi.download(project,collection,id,versionID);const url=URL.createObjectURL(blob);if(download){const a=document.createElement("a");a.href=url;a.download=name;a.click();setTimeout(()=>URL.revokeObjectURL(url),1000)}else{setContent({url,type:blob.type,text:blob.type.startsWith("text/plain")&&blob.size<=2000000?await blob.text():undefined})}}catch(e){setError(String(e))}finally{setBusy(false)}}
 return <section className="grid min-w-0 gap-2">{error&&<Alert>{error}</Alert>}<div className="flex gap-2"><Button variant="outline" disabled={busy} onClick={()=>void load(false)}>{t("clientOpenArtifact")}</Button>{collection!=="shares"&&<Button variant="outline" disabled={busy} onClick={()=>void load(true)}>{t("download")}</Button>}</div>{content?.type.startsWith("image/")&&<img src={content.url} alt={name} className="max-h-[32rem] max-w-full object-contain"/>}{content?.type==="application/pdf"&&<iframe title={name} src={content.url} sandbox="" className="h-[32rem] w-full"/>}{content?.text!==undefined&&<pre className="max-h-[32rem] overflow-auto whitespace-pre-wrap break-words text-sm">{content.text}</pre>}{content&&!content.type.startsWith("image/")&&content.type!=="application/pdf"&&content.text===undefined&&<p>{t("clientDownloadToView")}</p>}</section>
}

function ApplyClientChange({project,row,onUpdated}:{project:string;row:ClientRecords["changes"];onUpdated:()=>void}){
 const t=useTranslations("iam"),[confirm,setConfirm]=useState(false)
 const attempt=useRef<{key:string;body:object}|null>(null)
 return <><Button onClick={()=>setConfirm(true)}>{t("scopeChange")}</Button>{confirm&&<ReauthenticationDialog purpose="scope_change" target={`requirement:${row.proposal!.requirement_id}`} onClose={()=>setConfirm(false)} onVerified={async proof=>{const request=attempt.current??{key:crypto.randomUUID(),body:{proposal_hash:row.proposal_hash,reauth_proof:proof}};attempt.current=request;await clientworkApi.command(project,"changes",request.body,request.key,row.id!,"applications",row.version);attempt.current=null;setConfirm(false);onUpdated()}}/>}</>
}

function PreviewEditor({onSave}:{onSave:Mutation}) {
 const t=useTranslations("iam"),allowed=useAllowed(),[progress,setProgress]=useState(false)
 return <section className="grid gap-3 border-t pt-4"><h2>{t("clientCreate")}</h2><label className="flex min-h-11 items-center gap-2"><input type="checkbox" checked={progress} onChange={e=>setProgress(e.target.checked)}/>{t("clientProgressSource")}</label>{progress&&<p role="status">{t("clientProgressNotice")}</p>}<MutationForm key={String(progress)} fields={{title:{name:t("clientTitle"),type:"text",required:true},artifacts:{name:t("clientArtifacts"),type:"custom",component:progress?DirectProgressSourcePicker:ArtifactPicker,required:true},...(allowed("preview_share_approve")?{client_visible:{name:t("clientVisible"),type:"checkbox" as const}}:{})}} label={t("clientCreate")} submit={(v,key)=>onSave({title:v.title,source_stage:progress?"PROGRESS":"ACCEPTED",...(progress?{progress_source:v.artifacts}:{artifact:(v.artifacts as unknown[])?.[0]}),client_visible:v.client_visible===true},key)}/></section>
}

type ProgressSource = {work_item_id:string;file_id:string;version_id:string;sha256:string}
export function DirectProgressSourcePicker({value,onChange,disabled,labelId}:FieldRendererProps) {
 const {project}=useContext(ClientPickerScope),t=useTranslations("iam")
 const [works,setWorks]=useState<WorkItem[]>([]),[page,setPage]=useState(0),[total,setTotal]=useState(0),[work,setWork]=useState("")
 const [directory,setDirectory]=useState(""),[cursor,setCursor]=useState(""),[next,setNext]=useState(""),[files,setFiles]=useState<FileEntryView[]>([])
 const [busy,setBusy]=useState(false),[error,setError]=useState(""),[retry,setRetry]=useState(0),[sourceName,setSourceName]=useState("")
 const active=useRef(true),selection=useRef(0)
 const selected=value as ProgressSource|undefined
 useEffect(()=>{active.current=true;return()=>{active.current=false}},[])
 useEffect(()=>{const controller=new AbortController();void iamApi.workItems(project,{offset:page*20,limit:20},controller.signal).then(v=>{if(!controller.signal.aborted){setWorks(v.items);setTotal(v.total)}}).catch(e=>{if(!controller.signal.aborted)setError(String(e))});return()=>controller.abort()},[project,page,retry])
 const requestKey=JSON.stringify([project,directory,cursor,retry])
 const [previousRequest,setPreviousRequest]=useState("")
 if(previousRequest!==requestKey){setPreviousRequest(requestKey);setBusy(true);setError("");setFiles([])}
 useEffect(()=>{let cancelled=false;const request=directory?iamApi.fileItems(project,directory,cursor):iamApi.files(project,cursor);void request.then(v=>{if(!cancelled){setFiles(v.items??[]);setNext(v.next_after??"")}}).catch(e=>{if(!cancelled)setError(String(e))}).finally(()=>{if(!cancelled)setBusy(false)});return()=>{cancelled=true}},[project,directory,cursor,retry])
 const choose=async(file:FileEntryView)=>{
  if(!work||!file.id)return
  const attempt=++selection.current;onChange(undefined);setBusy(true);setError("")
  try{const candidate=await iamApi.deliveryResultRequest<{file_id:string;version_id:string;sha256:string;name:string}>({method:"GET",url:`/entities/${project}/artifact-previews/source-candidates/${work}/${file.id}`});if(active.current&&attempt===selection.current)onChange({work_item_id:work,file_id:candidate.file_id,version_id:candidate.version_id,sha256:candidate.sha256});if(active.current&&attempt===selection.current)setSourceName(candidate.name)}catch(e){if(active.current&&attempt===selection.current)setError(String(e))}finally{if(active.current&&attempt===selection.current)setBusy(false)}
 }
 return <div role="group" aria-labelledby={labelId} aria-busy={busy} className="grid gap-2">
  <select aria-label={t("clientChooseProgressWork")} className="min-h-11 rounded border bg-background p-2" disabled={disabled||busy} value={work} onChange={e=>{selection.current++;setWork(e.target.value);onChange(undefined)}}><option value="">{t("clientChooseProgressWork")}</option>{works.map(row=><option key={row.id} value={row.id!}>{row.title}</option>)}</select>
  <div className="flex gap-2"><Button type="button" variant="outline" disabled={disabled||busy||page===0} onClick={()=>setPage(v=>v-1)}>{t("fleetPrevious")}</Button><Button type="button" variant="outline" disabled={disabled||busy||(page+1)*20>=total} onClick={()=>setPage(v=>v+1)}>{t("fleetNext")}</Button></div>
  <p>{t("clientSourceFiles")}</p>{error&&<Alert>{error}</Alert>}
  {files.map(file=><Button key={file.id} type="button" variant="outline" className="min-h-11 justify-start" disabled={disabled||busy||(!work&&file.kind==="file")} onClick={()=>{if(file.kind==="directory"){setDirectory(file.id!);setCursor("")}else void choose(file)}}>{file.name}{file.kind==="directory"?" /":""}</Button>)}
  {!busy&&!files.length&&!error&&<p role="status">{t("clientEmpty")}</p>}{selected?.file_id&&<p role="status">{t("clientSourceSelected",{name:sourceName||selected.file_id})}</p>}
  <div className="flex flex-wrap gap-2"><Button type="button" variant="outline" disabled={disabled||busy||(!directory&&!cursor)} onClick={()=>{setDirectory("");setCursor("")}}>{t("clientFirstPage")}</Button><Button type="button" variant="outline" disabled={disabled||busy||!next} onClick={()=>setCursor(next)}>{t("fleetNext")}</Button>{error&&<Button type="button" variant="outline" onClick={()=>setRetry(v=>v+1)}>{t("clientRetry")}</Button>}</div>
 </div>
}

function PackageLifecycle({project,row,access,mutate,onChanged}:{project:string;row:ClientRecords["packages"];access:{external:boolean;principal_id:string}|null;mutate:(body:unknown,key:string,action:string,patch?:boolean)=>Promise<void>;onChanged:()=>void}) {
 const t=useTranslations("iam"),allowed=useAllowed(),[params,setParams]=useSearchParams()
 const [bundleState,setBundleState]=useState<import("../../lib/clientwork-api").PackageBundleState["state"]>()
 const editable=!access?.external&&allowed("delivery_compose")&&row.state==="DRAFT"&&Boolean(row.draft)
 const tabs=["overview","artifacts","timeline",...(editable?["editor"]:[])]
 const tab=tabs.includes(params.get("tab")??"")?params.get("tab")!:"overview"
 return <><nav aria-label={t("clientSections")} className="flex flex-wrap gap-2">{tabs.map(value=><Button key={value} variant={value===tab?"default":"outline"} onClick={()=>setParams(old=>{const next=new URLSearchParams(old);next.set("tab",value);return next})}>{t(`clientDeliveryTab_${value}`)}</Button>)}</nav>
 {tab==="editor"&&editable&&row.draft&&<Editor collection="packages" initialData={{...row.draft}} editing onSave={(body,key)=>mutate(body,key,"",true)}/>}
 {tab==="editor"&&editable&&<ReleaseNotes project={project} id={row.id!} version={row.version} onChanged={onChanged}/>}
 {tab==="overview"&&<><h3>{row.content.title}</h3><p className="whitespace-pre-wrap">{row.content.release_notes}</p><p>{row.state}</p><p className="break-all text-xs">{row.content_hash}</p>{row.decision&&<p>{row.decision.decision}: {row.decision.reason}</p>}{access?.external&&row.state==="PUBLISHED"&&<MutationForm fields={{decision:{name:t("clientDecision"),type:"select",required:true,config:{options:[{value:"ACCEPT",label:t("clientAccept")},{value:"REJECT",label:t("clientReject") }]}},reason:{name:t("clientReason"),type:"textarea",required:true}}} label={t("clientSubmitDecision")} submit={(v,key)=>mutate({...v,content_hash:row.content_hash},key,"receipts")}/>}</>}
 {(tab==="overview"||tab==="editor")&&<PackageBundle project={project} id={row.id!} editable={editable} onChanged={onChanged} mutate={mutate} onState={setBundleState}/>}
 {tab==="overview"&&access&&<PackageSend project={project} row={row} external={access.external} mutate={mutate} bundleReady={bundleState==="BUILT"}/>}{tab==="overview"&&<PackageAcceptance project={project} row={row} access={access} mutate={mutate}/>}
 {tab==="artifacts"&&row.content.artifacts?.map((a,index)=><section key={`${a.version_id}:${index}`} className="grid gap-1 border-b pb-2"><p>{a.name} / {a.coverage}</p><ArtifactContent project={project} collection="packages" id={row.id!} versionID={a.version_id??undefined} name={a.name}/><PackageArtifactDisclosure artifact={a}/></section>)}
 {tab==="timeline"&&<><PackageTimeline key={`${project}:${row.id}:${row.version}`} project={project} id={row.id!}/><AcceptanceStatus key={`acceptance:${project}:${row.id}:${row.version}`} project={project} id={row.id!} external={access?.external!==false}/></>}
 </>
}
function PackageTimeline({project,id}:{project:string;id:string}){
 const t=useTranslations("iam"),[cursor,setCursor]=useState("")
 const [data,setData]=useState<ClientPage<import("../../lib/api-schema").components["schemas"]["PackageTimelineView"]>>({items:[],has_more:false})
 const [error,setError]=useState(""),[busy,setBusy]=useState(true)
 const changeCursor=(next:string)=>{setBusy(true);setError("");setCursor(next)}
 useEffect(()=>{const c=new AbortController();void clientworkApi.timeline(project,id,cursor,c.signal).then(v=>{if(!c.signal.aborted)setData(v)}).catch(e=>{if(!c.signal.aborted)setError(String(e))}).finally(()=>{if(!c.signal.aborted)setBusy(false)});return()=>c.abort()},[project,id,cursor])
 return <section className="grid gap-3" aria-busy={busy}>{error&&<Alert>{error}</Alert>}{data.items.map(item=><article key={item.id} className="grid gap-1 border-l-2 pl-3"><p>{item.state} / {t("clientVersion")} {item.version}</p><time dateTime={item.created_at}>{item.created_at}</time><p className="break-all text-xs">{item.content_hash}</p></article>)}{!busy&&!data.items.length&&<p>{t("clientEmpty")}</p>}<div className="flex gap-2"><Button variant="outline" disabled={busy||!cursor} onClick={()=>changeCursor("")}>{t("clientFirstPage")}</Button><Button variant="outline" disabled={busy||!data.has_more} onClick={()=>changeCursor(data.next_cursor!)}>{t("fleetNext")}</Button></div></section>
}
