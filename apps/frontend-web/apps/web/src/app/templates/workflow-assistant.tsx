import {useEffect,useRef,useState} from "react"
import {Button} from "@workspace/ui/components/button"
import {Input} from "@workspace/ui/components/input"
import {Textarea} from "@workspace/ui/components/textarea"
import {useTranslations} from "@workspace/ui/i18n/intl-provider"
import {workflowAssistantApi,type WorkflowValidation,type WorkflowModelProposal} from "./api"
import type {TemplateVersion,WorkflowDef} from "./workflow"

export function WorkflowServerIssues({validation}:{validation:WorkflowValidation}){const t=useTranslations("iam");return <div role="status" className="space-y-2 text-sm">{validation.valid?<p>{t("templateServerValid")}</p>:validation.issues.map((issue,index)=><p key={index}><code className="break-all">{issue.path}</code>: {t(issue.code)}</p>)}</div>}
export function WorkflowAssistant({tenantID,version,workflow,canEdit,onProposal}:{tenantID:string;version:TemplateVersion;workflow:WorkflowDef;canEdit:boolean;onProposal:(proposal:WorkflowModelProposal,base:string)=>void}){
 const t=useTranslations("iam"),[provider,setProvider]=useState(""),[instruction,setInstruction]=useState(""),[validation,setValidation]=useState<WorkflowValidation|null>(null),[error,setError]=useState(""),[busy,setBusy]=useState(false)
 const request=useRef<AbortController|null>(null),pending=useRef<{fingerprint:string;key:string}|null>(null)
 useEffect(()=>()=>request.current?.abort(),[])
 const clean=JSON.stringify(workflow)===JSON.stringify(version.definition.workflow)
 const perform=async(generate:boolean)=>{
  if(request.current||!canEdit)return
  const base=JSON.stringify(workflow),fingerprint=JSON.stringify({tenantID,id:version.id,hash:version.hash,provider,instruction})
  if(pending.current?.fingerprint!==fingerprint)pending.current={fingerprint,key:crypto.randomUUID()}
  const abort=new AbortController();request.current=abort;setBusy(true);setError("");setValidation(null)
  try{
   if(generate){const result=await workflowAssistantApi.generate(tenantID,version,provider,instruction,pending.current.key,abort.signal);if(!abort.signal.aborted){setValidation(result.validation);if(result.validation.valid)onProposal(result,base);pending.current=null}}
   else {const result=await workflowAssistantApi.validate(tenantID,version,workflow,abort.signal);if(!abort.signal.aborted)setValidation(result)}
  }catch(cause){if(!abort.signal.aborted)setError(cause instanceof Error?cause.message:String(cause))}
  finally{request.current=null;if(!abort.signal.aborted)setBusy(false)}
 }
 return <section className="space-y-3 border-y py-4" aria-label={t("templateAssistantTitle")}><h2 className="font-medium">{t("templateAssistantTitle")}</h2><p className="text-sm text-muted-foreground">{t("templateAssistantNotice")}</p>{error&&<p role="alert">{error}</p>}<fieldset disabled={!canEdit||busy} className="space-y-3"><label className="block space-y-1 text-sm"><span>{t("templateAssistantProvider")}</span><Input inputMode="numeric" value={provider} onChange={event=>setProvider(event.target.value)}/></label><label className="block space-y-1 text-sm"><span>{t("templateAssistantInstruction")}</span><Textarea maxLength={8000} value={instruction} onChange={event=>setInstruction(event.target.value)}/></label><div className="flex flex-wrap gap-2"><Button variant="outline" onClick={()=>void perform(false)}>{t("templateServerValidate")}</Button><Button disabled={!clean||!/^[1-9]\d*$/.test(provider)||!instruction.trim()} onClick={()=>void perform(true)}>{t("templateAssistantGenerate")}</Button></div></fieldset>{validation&&<WorkflowServerIssues validation={validation}/>}</section>
}
