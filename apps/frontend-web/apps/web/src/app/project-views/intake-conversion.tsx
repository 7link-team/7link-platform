import { useEffect, useRef, useState } from "react"
import { Link } from "react-router"
import { Button } from "@workspace/ui/components/button"
import { useTranslations } from "@workspace/ui/i18n/intl-provider"
import { Input } from "@workspace/ui/components/input"
import { Textarea } from "@workspace/ui/components/textarea"
import { useAllowed } from "../../lib/permissions"
import type { ReworkDisposition } from "./intake-api"
import { AutoForm } from "@workspace/ui/components/auto-form"
import { ReauthenticationDialog } from "../../components/reauthentication-dialog"
import { Alert } from "../../components/alert"
import { intakeChangeApi, type IntakeLedgerSource, type DelegatedIntakeCard } from "./intake-api"

export function IntakeNewRequirement({project, base, source, onSaved}: {project: string; base: string; source: IntakeLedgerSource; onSaved: () => void}) {
  const t = useTranslations("iam")
  const [busy, setBusy] = useState(false), [error, setError] = useState("")
  const [created, setCreated] = useState("")
  const attempt = useRef<string | null>(null)
  if (!source.triage || source.kind !== "NEW" || source.state !== "TRIAGED") return null
  return <section className="grid gap-2 border-t pt-4"><p className="text-sm text-muted-foreground">{t("intakeNewRequirementNotice")}</p>{error && <Alert>{error}</Alert>}<Button className="min-h-11 justify-self-start" disabled={busy || Boolean(created)} onClick={async () => {
    if (busy) return
    attempt.current ??= crypto.randomUUID()
    setBusy(true); setError("")
    try { const result = await intakeChangeApi.newRequirement(project, source, attempt.current); setCreated(result.id ?? ""); onSaved() }
    catch (cause) { setError(cause instanceof Error ? cause.message : t("clientSaveFailed")) }
    finally { setBusy(false) }
  }}>{busy ? t("clientSaving") : t("intakeNewRequirement")}</Button>{created && <Link className="underline underline-offset-4" to={`${base}/requirements`}>{t("requirements")}</Link>}</section>
}

export function IntakeNewProject({project,source,onSaved}:{project:string;source:IntakeLedgerSource;onSaved:()=>void}) {
 const t=useTranslations("iam")
 const [draft,setDraft]=useState<{name:string;code:string}|null>(null)
 const attempt=useRef<{signature:string;key:string}|null>(null)
 if(source.kind!=="NEW"||source.state!=="TRIAGED"||!source.triage)return null
 return <details className="border-t pt-4"><summary className="cursor-pointer py-3">{t("intakeNewProject")}</summary><p className="mb-3 text-sm text-muted-foreground">{t("intakeNewProjectNotice")}</p><AutoForm mode="add" fields={{name:{name:t("name"),type:"text",required:true},code:{name:t("projectCode"),type:"text",required:true}}} onSubmit={values=>{
  const name=String(values.name??"").trim(),code=String(values.code??"").trim()
  if(!name||Array.from(name).length>200||!/^[A-Z][A-Z0-9-]{1,15}$/.test(code))throw new Error(t("projectCreateFailed"))
  setDraft({name,code})
 }}><Button type="submit" className="min-h-11">{t("intakeNewProject")}</Button></AutoForm>
 {draft&&<ReauthenticationDialog purpose="intake_convert_privileged" target={`intake:${source.id}`} onClose={()=>setDraft(null)} onVerified={async proof=>{
  const signature=JSON.stringify([project,source.id,source.version,source.triage?.hash,draft])
  if(attempt.current?.signature!==signature)attempt.current={signature,key:crypto.randomUUID()}
  await intakeChangeApi.newProject(project,source,draft.name,draft.code,proof,attempt.current.key)
  attempt.current=null;setDraft(null);onSaved()
 }}/>}
 </details>
}


export function IntakeReworkDisposition({project,source,onSaved}:{project:string;source:IntakeLedgerSource;onSaved:()=>void}) {
 const t=useTranslations("iam"), allowed=useAllowed()
 const [item,setItem]=useState(source.rework?.rejection.items?.[0]?.item_id??"")
 const [mode,setMode]=useState<ReworkDisposition["items"][number]["mode"]>("REOPEN_WORK_ITEMS")
 const [version,setVersion]=useState("")
 const [reason,setReason]=useState(""),[message,setMessage]=useState("")
 const [requirement,setRequirement]=useState("")
 const [requirementVersion,setRequirementVersion]=useState("")
 const [baseline,setBaseline]=useState("")
 const [criterion,setCriterion]=useState("")
 const [verify,setVerify]=useState(false),[busy,setBusy]=useState(false),[error,setError]=useState("")
 const pending=useRef<{signature:string;key:string}|null>(null)
 if(source.kind!=="REWORK"||!source.rework||!allowed("decision_request_changes"))return null
 const submit=async(proof?:string)=>{
  const body:ReworkDisposition={receipt_id:source.rework!.receipt_id,source_hash:source.raw_content_hash,items:[{item_id:item,mode,reason,client_message:message,...(mode==="REOPEN_WORK_ITEMS"?{work_version:Number(version)}:{})}]}
  if(mode==="NEW_REQUIREMENT")body.requirement_draft=source.triage?.draft
  if(mode==="APPEND_TO_REQUIREMENT"){
   body.requirement_id=requirement;body.requirement_version=Number(requirementVersion)
   body.append={base_version_no:Number(baseline),summary:reason,criteria:{add:[{text:criterion,required:true}]},reauth_proof:proof??""}
  }
  const signature=JSON.stringify({...body,append:body.append?{...body.append,reauth_proof:undefined}:undefined})
  if(pending.current?.signature!==signature)pending.current={signature,key:crypto.randomUUID()}
  setBusy(true);setError("")
  try{await intakeChangeApi.dispose(project,source,body,pending.current.key);setVerify(false);onSaved()}
  catch(cause){setError(cause instanceof Error?cause.message:t("clientSaveFailed"));throw cause}
  finally{setBusy(false)}
 }
 return <form className="space-y-3 border-t pt-4" aria-label={t("intakeDisposition")} onSubmit={event=>{
  event.preventDefault();if(busy)return
  if(mode==="APPEND_TO_REQUIREMENT")setVerify(true)
  else void submit().catch(()=>undefined)
 }}>
 <h3 className="font-medium">{t("intakeDisposition")}</h3>
 <fieldset disabled={busy} className="grid gap-3 sm:grid-cols-2">
 <label className="space-y-1"><span>{t("intakeRejectedItem")}</span><select className="min-h-11 w-full rounded-md border bg-background px-3" value={item} onChange={event=>setItem(event.target.value)}>{(source.rework.rejection.items??[]).map(fact=><option key={fact.item_id} value={fact.item_id}>{fact.work_item_id} / {fact.comment}</option>)}</select></label>
 <label className="space-y-1"><span>{t("intakeDisposition")}</span><select className="min-h-11 w-full rounded-md border bg-background px-3" value={mode} onChange={event=>setMode(event.target.value as typeof mode)}>{(["REOPEN_WORK_ITEMS","APPEND_TO_REQUIREMENT","NEW_REQUIREMENT","DECLINE"] as const).map(value=><option key={value} value={value} disabled={value==="NEW_REQUIREMENT"&&!source.triage}>{t(`intakeDisposition_${value}`)}</option>)}</select></label>
 {mode==="REOPEN_WORK_ITEMS"&&<label><span>{t("intakeWorkVersion")}</span><Input type="number" min={1} step={1} required value={version} onChange={event=>setVersion(event.target.value)}/></label>}
 {mode==="APPEND_TO_REQUIREMENT"&&<>
 <label><span>{t("intakeTargetRequirement")}</span><Input required pattern="[1-9][0-9]*" value={requirement} onChange={event=>setRequirement(event.target.value)}/></label>
 <label><span>{t("intakeRequirementVersion")}</span><Input type="number" min={1} step={1} required value={requirementVersion} onChange={event=>setRequirementVersion(event.target.value)}/></label>
 <label><span>{t("workRecoveryVersion")}</span><Input type="number" min={1} step={1} required value={baseline} onChange={event=>setBaseline(event.target.value)}/></label>
 <label><span>{t("intakeAdditionalCriterion")}</span><Textarea required maxLength={2000} value={criterion} onChange={event=>setCriterion(event.target.value)}/></label>
 </>}
 <label><span>{t("intakeDispositionReason")}</span><Textarea required minLength={mode==="APPEND_TO_REQUIREMENT"?5:1} maxLength={2000} value={reason} onChange={event=>setReason(event.target.value)}/></label>
 <label><span>{t("intakeClientMessage")}</span><Textarea required maxLength={2000} value={message} onChange={event=>setMessage(event.target.value)}/></label>
 </fieldset>
 {error&&<Alert>{error}</Alert>}
 <Button type="submit" className="min-h-11" disabled={busy}>{busy?t("clientSaving"):t("intakeDisposition")}</Button>
 {verify&&<ReauthenticationDialog purpose="scope_change" target={`requirement:${requirement}`} onClose={()=>setVerify(false)} onVerified={submit}/>}
 </form>
}

export function IntakeDelegatedNew({project,base}:{project:string;base:string}) {
 const t=useTranslations("iam")
 const [previewRevision,setPreviewRevision]=useState(0)
 const [selected,setSelected]=useState(""),[card,setCard]=useState<DelegatedIntakeCard|null>(null)
 const [error,setError]=useState(""),[loading,setLoading]=useState(false),[busy,setBusy]=useState(false),[created,setCreated]=useState("")
 const attempt=useRef<string|null>(null)
 useEffect(()=>{
  if(!selected)return
  const controller=new AbortController()
  void intakeChangeApi.delegatedCard(project,selected,controller.signal).then(value=>{if(!controller.signal.aborted)setCard(value)}).catch((cause:unknown)=>{if(!controller.signal.aborted)setError(cause instanceof Error?cause.message:t("clientSaveFailed"))}).finally(()=>{if(!controller.signal.aborted)setLoading(false)})
  return ()=>controller.abort()
 },[project,selected,previewRevision,t])
 return <section className="grid gap-3" aria-busy={loading||busy}>
 <h2 className="font-medium">{t("intakeDelegatedConversion")}</h2><p className="text-sm text-muted-foreground">{t("intakeDelegatedNotice")}</p>
 <AutoForm mode="add" fields={{source:{name:t("intakeWorkbenchID"),type:"text",required:true}}} onSubmit={values=>{const id=String(values.source??"").trim();if(!/^[1-9][0-9]{0,18}$/.test(id))throw new Error(t("intakeRead"));setLoading(true);setCard(null);setError("");setCreated("");attempt.current=null;setSelected(id);setPreviewRevision(value=>value+1)}}><Button type="submit" disabled={busy||loading} className="min-h-11">{t("intakeRead")}</Button></AutoForm>
 {error&&<Alert>{error}</Alert>}{loading&&<p role="status">{t("loading")}</p>}
 {card&&<><p className="whitespace-pre-wrap break-words">{card.draft.summary}</p><p className="text-sm">{t("intakeCardRevision")}: {card.revision}</p><p className="break-all text-xs">{card.hash}</p><p className="whitespace-pre-wrap break-words text-sm">{card.draft.requirement_draft}</p><ul className="list-inside list-disc text-sm">{(card.draft.criteria??[]).map((text,index)=><li key={index}>{text}</li>)}</ul>
 <Button className="min-h-11 justify-self-start" disabled={busy||Boolean(created)} onClick={async()=>{
 if(busy)return
 attempt.current??=crypto.randomUUID();setBusy(true);setError("")
 try{const result=await intakeChangeApi.delegatedRequirement(project,card,attempt.current);setCreated(result.id??"")}catch(cause){setError(cause instanceof Error?cause.message:t("clientSaveFailed"))}finally{setBusy(false)}
 }}>{busy?t("clientSaving"):t("intakeNewRequirement")}</Button></>}
 {created&&<Link className="underline underline-offset-4" to={`${base}/requirements`}>{t("requirements")}</Link>}
 </section>
}
