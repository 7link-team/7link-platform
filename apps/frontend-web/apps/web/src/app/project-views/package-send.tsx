import { useEffect, useState } from "react"
import { Button } from "@workspace/ui/components/button"
import { useTranslations } from "@workspace/ui/i18n/intl-provider"
import { Alert } from "../../components/alert"
import { ReauthenticationDialog } from "../../components/reauthentication-dialog"
import { clientworkApi, type ClientRecords, type DeliveryConfirmation, type PackageSendRecord, type ClientPage } from "../../lib/clientwork-api"
import { useAllowed } from "../../lib/permissions"
import { channelLifecycleApi } from "../conversations/channel-controls-api"
import type { ConversationChannel } from "../../lib/iam-api"
import { MutationForm } from "./client"

export function PackageSend({project,row,external,mutate,bundleReady=false}:{project:string;row:ClientRecords["packages"];external:boolean;bundleReady?:boolean;mutate:(body:unknown,key:string,action:string)=>Promise<void>}) {
 const t=useTranslations("iam"),allowed=useAllowed()
 const [publish,setPublish]=useState<string|null>(null)
 const [confirmation,setConfirmation]=useState<DeliveryConfirmation|null>(null)
 if(external || row.archived_at)return null
 return <section className="grid min-w-0 gap-3 border-t pt-4" aria-label={t("packageSending")}>
  <h3 className="font-medium">{t("packageSending")}</h3><p className="text-sm text-muted-foreground">{t("packageSendNotice")}</p>
  {row.state==="DRAFT"&&allowed("delivery_compose")&&<MutationForm fields={{}} label={t("packageRequestConfirmation")} submit={async(_,key)=>{const result=await clientworkApi.requestDeliveryConfirmation(project,row.id!,row.version,key);setConfirmation(result)}}/>}
  {row.state==="DRAFT"&&confirmation&&<><p role="status">{t("packageConfirmationPending")}</p><MutationForm fields={{}} label={t("packageWithdrawConfirmation")} submit={async(_,key)=>{await clientworkApi.withdrawDeliveryConfirmation(project,confirmation.id!,key);setConfirmation(null)}}/></>}
  {row.state==="DRAFT"&&bundleReady&&allowed("delivery_send")&&<MutationForm fields={{}} label={t("clientPublish")} submit={async(_,key)=>setPublish(key)}/>}
  {publish&&<ReauthenticationDialog purpose="delivery_send" target={`package:${row.id}`} onClose={()=>setPublish(null)} onVerified={async proof=>{await mutate({reauth_proof:proof},publish,"publish");setPublish(null)}}/>}
  {allowed("delivery_send")&&row.state!=="DRAFT"&&<SendAttempts key={`${project}:${row.id}:${row.version}`} project={project} row={row} mutate={mutate}/>}
 </section>
}
function SendAttempts({project,row,mutate}:{project:string;row:ClientRecords["packages"];mutate:(body:unknown,key:string,action:string)=>Promise<void>}) {
 const t=useTranslations("iam")
 const [page,setPage]=useState<ClientPage<PackageSendRecord>>({items:[],has_more:false})
 const [channels,setChannels]=useState<ConversationChannel[]>([]),[channelCursor,setChannelCursor]=useState("")
 const [cursor,setCursor]=useState(""),[revision,setRevision]=useState(0),[busy,setBusy]=useState(true),[error,setError]=useState("")
 useEffect(()=>{const controller=new AbortController();void Promise.all([clientworkApi.packageSends(project,row.id!,cursor,controller.signal),channelLifecycleApi.list(project,false,undefined,controller.signal)]).then(([attempts,choices])=>{if(controller.signal.aborted)return;setPage(attempts);setChannels(choices.items??[]);setChannelCursor(choices.next_cursor??"");setError("")}).catch(cause=>{if(!controller.signal.aborted)setError(cause instanceof Error?cause.message:t("clientLoadFailed"))}).finally(()=>{if(!controller.signal.aborted)setBusy(false)});return()=>controller.abort()},[project,row.id,cursor,revision,t])
 const latest=page.items.at(-1)
 const sendable=row.state==="PUBLISHED"&&!busy&&!error&&!page.has_more&&!page.items.some(item=>item.state==="SUCCEEDED")
 return <div className="grid gap-3" aria-busy={busy}>
  {error&&<Alert>{error}</Alert>}<Button variant="outline" disabled={busy} onClick={()=>{setBusy(true);setRevision(v=>v+1)}}>{t("clientRetry")}</Button>
  {sendable&&<MutationForm fields={latest?{}:{channel_id:{name:t("packageSendChannel"),type:"select",required:true,config:{options:channels.filter(c=>c.visibility==="CLIENT_SHARED"&&!c.archived).map(c=>({value:c.id!,label:c.name}))}}}} label={latest?t("packageRetrySend"):t("packageSendNow")} submit={async(values,key)=>{await mutate({channel_id:latest?.channel_id??String(values.channel_id)},key,"sends");setRevision(v=>v+1)}}/>}
  {latest&&<p className="text-sm">{t("packageFrozenRecipient",{recipient:latest.recipient_id,channel:latest.channel_id})}</p>}
  {!!channelCursor&&<Button variant="outline" onClick={async()=>{try{const next=await channelLifecycleApi.list(project,false,channelCursor);setChannels(old=>[...old,...(next.items??[])]);setChannelCursor(next.next_cursor??"")}catch(cause){setError(String(cause))}}}>{t("fleetNext")}</Button>}
  {!busy&&!page.items.length&&<p role="status">{t("packageNoSendFacts")}</p>}
  <ol className="grid gap-3">{page.items.map(item=><li key={item.id} className="grid gap-1 border-l-2 pl-3 text-sm"><strong>{t(item.state==="SUCCEEDED"?"packageSendSucceeded":"packageSendFailed",{attempt:item.attempt_no})}</strong><p>{t("packageFrozenRecipient",{recipient:item.recipient_id,channel:item.channel_id})}</p><p className="break-all">{item.content_hash}</p><p>{item.created_at}</p>{item.message_id&&<p>{t("packageSendMessage",{message:item.message_id})}</p>}{item.failure_code&&<p>{t("packageSendUnavailable")}</p>}</li>)}</ol>
  <div className="flex gap-2"><Button variant="outline" disabled={busy||!cursor} onClick={()=>{setBusy(true);setCursor("")}}>{t("clientFirstPage")}</Button><Button variant="outline" disabled={busy||!page.has_more} onClick={()=>{setBusy(true);setCursor(page.next_cursor!)}}>{t("fleetNext")}</Button></div>
 </div>
}
