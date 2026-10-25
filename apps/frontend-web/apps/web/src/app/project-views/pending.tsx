import { useEffect, useState } from "react"
import { Link, useParams, useSearchParams } from "react-router"
import { Button } from "@workspace/ui/components/button"
import { useTranslations } from "@workspace/ui/i18n/intl-provider"
import { PageHeader } from "../../components/page-header"
import { Alert } from "../../components/alert"
import { useAllowed } from "../../lib/permissions"
import { clientworkApi, collaborationID, type ClientPage } from "../../lib/clientwork-api"
import { iamApi } from "../../lib/iam-api"
import type { components } from "../../lib/api-schema"

type Pending = components["schemas"]["ClientPendingItem"]
const destinations = {SIGNOFF:"baseline",CHANGE:"changes",ACCEPTANCE:"deliveries"} as const

export default function ClientPending() {
 const {entityID:project=""}=useParams(),[params]=useSearchParams(),allowed=useAllowed()
 const cursor=params.get("cursor")??"",canRead=allowed("channel_read")
 return <PendingQueue key={`${project}:${cursor}:${canRead}`} project={project} cursor={cursor} canRead={canRead}/>
}

function PendingQueue({project,cursor,canRead}:{project:string;cursor:string;canRead:boolean}) {
 const {tenantID=""}=useParams(),t=useTranslations("iam")
 const [,setParams]=useSearchParams()
 const [page,setPage]=useState<ClientPage<Pending>|null>(null),[error,setError]=useState(""),[busy,setBusy]=useState(canRead),[internal,setInternal]=useState(false),[revision,setRevision]=useState(0)
 useEffect(()=>{
  const c=new AbortController()
  if(!canRead)return()=>c.abort()
  void clientworkApi.access(project,c.signal).then(async access=>{
   if(c.signal.aborted)return
   if(!access.external){setInternal(true);return}
   const result=await iamApi.deliveryResultRequest<ClientPage<Pending>>({method:"GET",url:`/entities/${collaborationID(project)}/client-pending?${new URLSearchParams({limit:"20",...(cursor?{cursor}:{})})}`},{signal:c.signal})
   if(!c.signal.aborted)setPage(result)
  }).catch(e=>{if(!c.signal.aborted)setError(String(e))}).finally(()=>{if(!c.signal.aborted)setBusy(false)})
  return()=>c.abort()
 },[project,cursor,canRead,revision])
 const navigate=(next:string)=>{setPage(null);setParams(next?{cursor:next}:{})}
 const refresh=()=>{setBusy(true);setError("");setPage(null);setInternal(false);setRevision(v=>v+1)}
 return <div className="grid min-w-0 gap-4 overflow-auto"><PageHeader title={t("clientPendingTitle")}/>{!canRead||internal?<p role="status">{t("clientPendingClientOnly")}</p>:<section className="grid gap-3" aria-busy={busy}>
  <p>{t("clientPendingNotice")}</p>{error&&<Alert>{error}</Alert>}
  <Button variant="outline" disabled={busy} onClick={refresh}>{t("clientRetry")}</Button>
  {busy&&<p role="status">{t("loading")}</p>}
  {page?.items.map(item=><article key={item.id} className="grid gap-2 border-b py-3"><p>{t(`clientPending_${item.kind}`)} / {t("clientVersion")} {item.version}</p><Link className="break-words underline underline-offset-4" to={`/admin/${encodeURIComponent(tenantID)}/entities/${collaborationID(project)}/${destinations[item.kind]}?${new URLSearchParams({id:collaborationID(item.id)})}`}>{item.title}</Link></article>)}
  {page&&!page.items.length&&<p>{t("clientPendingEmpty")}</p>}
  <div className="flex gap-2"><Button variant="outline" disabled={busy||!cursor} onClick={()=>navigate("")}>{t("clientFirstPage")}</Button><Button variant="outline" disabled={busy||!page?.has_more} onClick={()=>navigate(page?.next_cursor??"")}>{t("fleetNext")}</Button></div>
 </section>}</div>
}
