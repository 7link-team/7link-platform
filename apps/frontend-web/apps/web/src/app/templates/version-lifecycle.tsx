import { useId, useRef, useState } from "react"
import { useTranslations } from "@workspace/ui/i18n/intl-provider"
import { Button } from "@workspace/ui/components/button"
import { Input } from "@workspace/ui/components/input"
import { templatesApi } from "./api"
import type { TemplateVersion } from "./workflow"

export function TemplateVersionLifecycle({tenantID,version,canManage,onChanged}:{tenantID:string;version:TemplateVersion;canManage:boolean;onChanged:(next:TemplateVersion)=>void}) {
 const t=useTranslations("iam")
 const id=useId()
 const [key,setKey]=useState("")
 const [name,setName]=useState("")
 const [confirmed,setConfirmed]=useState(false)
 const [busy,setBusy]=useState(false)
 const [error,setError]=useState("")
 const pending=useRef<{fingerprint:string;retryKey:string}|null>(null)
 const immutable=version.state==="PUBLISHED"||version.state==="DEPRECATED"
 const run=async(action:"copy"|"deprecate")=>{
  if(busy||!canManage)return
  const fingerprint=JSON.stringify([action,version.id,key,name])
  if(pending.current?.fingerprint!==fingerprint)pending.current={fingerprint,retryKey:crypto.randomUUID()}
  setBusy(true);setError("")
  try {
   const next=action==="copy"?await templatesApi.copy(tenantID,version.id,key.trim(),name.trim(),pending.current.retryKey):await templatesApi.deprecate(tenantID,version.id,pending.current.retryKey)
   pending.current=null
   onChanged(next)
  }catch(failure){setError(failure instanceof Error?failure.message:String(failure))}finally{setBusy(false)}
 }
 return <section className="space-y-3 rounded-md border p-4" aria-label={t("templateLifecycle")}>
  {version.name&&<p className="font-medium">{version.name}</p>}
  {version.based_on_version_id&&<p className="break-all text-sm">{t("templateCopiedFrom")}: {version.based_on_version_id} / <code>{version.based_on_sha256}</code></p>}
  {version.state==="DEPRECATED"&&<p role="status" className="text-sm">{t("templateDeprecatedNotice")}</p>}
  {error&&<p role="alert" className="text-sm text-destructive">{error}</p>}
  {canManage&&immutable&&<form className="flex flex-wrap items-end gap-3" onSubmit={event=>{event.preventDefault();if(key.trim()&&name.trim())void run("copy")}}>
   <label htmlFor={`${id}-key`} className="space-y-1 text-sm">{t("templateCopyKey")}<Input id={`${id}-key`} value={key} onChange={event=>setKey(event.target.value)} disabled={busy} required maxLength={64}/></label>
   <label htmlFor={`${id}-name`} className="space-y-1 text-sm">{t("templateCopyName")}<Input id={`${id}-name`} value={name} onChange={event=>setName(event.target.value)} disabled={busy} required maxLength={200}/></label>
   <Button type="submit" disabled={busy||!key.trim()||!name.trim()} aria-busy={busy}>{t("templateCopy")}</Button>
  </form>}
  {canManage&&version.state==="PUBLISHED"&&<div className="space-y-2 border-t pt-3">
   <p className="text-sm text-muted-foreground">{t("templateDeprecateHelp")}</p>
   <label className="flex items-center gap-2 text-sm"><input type="checkbox" checked={confirmed} disabled={busy} onChange={event=>setConfirmed(event.target.checked)}/>{t("templateDeprecateConfirm")}</label>
   <Button variant="outline" disabled={busy||!confirmed} aria-busy={busy} onClick={()=>void run("deprecate")}>{t("templateDeprecate")}</Button>
  </div>}
 </section>
}
