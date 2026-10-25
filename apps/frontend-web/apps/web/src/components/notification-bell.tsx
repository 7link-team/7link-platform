import { useEffect, useState } from "react"
import { useRealtime } from "@workspace/ui/hooks/use-realtime"
import { Bell, CircleAlert } from "lucide-react"
import { Link, useNavigate } from "react-router"
import { Popover, PopoverContent, PopoverTrigger } from "@workspace/ui/components/popover"
import { Button } from "@workspace/ui/components/button"
import { useTranslations } from "@workspace/ui/i18n/intl-provider"
import { useAuth } from "./auth"
import { notificationApi, safeNotificationLink, type Notification } from "../lib/notification-api"

export function NotificationBell({tenantID = ""}: {tenantID?: string}) {
 const t = useTranslations("iam")
 const {session} = useAuth()
 const principal = session?.subject ?? ""
 const refreshInbox = () => window.dispatchEvent(new Event("notification-changed"))
 useRealtime(principal ? `tenant/${tenantID || "0"}/inbox/${principal}` : null, {eventTypes:["notification.created"],onEvent:refreshInbox,onResync:refreshInbox,poll:refreshInbox})
 const navigate = useNavigate()
 const [open,setOpen] = useState(false)
 const [snapshot,setSnapshot] = useState<{principal:string;count:number;items:Notification[]}>({principal:"",count:0,items:[]})
 const [error,setError] = useState("")
 const [busy,setBusy] = useState(false)
 useEffect(() => {
  if(!principal)return
  const controller = new AbortController()
  const refresh = async () => {try {const summary = await notificationApi.summary(principal,controller.signal);const page = open ? await notificationApi.list(principal,"",0,controller.signal) : null;if(!controller.signal.aborted){setSnapshot({principal,count:summary.unread,items:page?.items ?? []});setError("")}}catch {if(!controller.signal.aborted)setError(t("notificationLoadError"))}}
  void refresh()
  const interval = window.setInterval(() => void refresh(),30000)
  const changed = () => void refresh()
  window.addEventListener("notification-changed",changed)
  return () => {controller.abort();window.clearInterval(interval);window.removeEventListener("notification-changed",changed)}
 },[principal,open,t])
 const current = snapshot.principal === principal ? snapshot : {count:0,items:[]}
 const onSelect = async (item: Notification) => {setBusy(true);try{await notificationApi.patch(item.id,{read:true});window.dispatchEvent(new Event("notification-changed"));setOpen(false);navigate(safeNotificationLink(item.link_path))}catch{setError(t("notificationSaveError"))}finally{setBusy(false)}}
 const markAll = async () => {if(!current.items.length)return;setBusy(true);try{const summary=await notificationApi.summary(principal);for(const group of summary.by_tenant)await notificationApi.markRead(principal,group.tenant_id,current.items[0]!.id);window.dispatchEvent(new Event("notification-changed"))}catch{setError(t("notificationSaveError"))}finally{setBusy(false)}}
 if(!principal)return null
 return <Popover open={open} onOpenChange={setOpen}>
  <PopoverTrigger aria-label={t("notificationUnreadCount",{count:current.count})} className="relative grid size-11 place-items-center rounded-md hover:bg-accent focus-visible:outline-2 focus-visible:outline-ring">
   <Bell className="size-5" aria-hidden="true"/>{current.count>0&&<span aria-hidden="true" className="absolute end-0 top-0 rounded-full bg-destructive px-1.5 text-xs text-destructive-foreground">{current.count>99?"99+":current.count}</span>}
  </PopoverTrigger>
  <PopoverContent align="end" className="w-[min(24rem,calc(100vw-2rem))] p-0">
   <div className="flex items-center justify-between border-b p-4"><h2 className="font-semibold">{t("notificationTitle")}</h2><Link to="/user/notifications" onClick={()=>setOpen(false)} className="text-sm text-primary underline">{t("notificationViewAll")}</Link></div>
   <Button variant="ghost" className="w-full rounded-none" disabled={busy||current.count===0||current.items.length===0} onClick={()=>void markAll()}>{t("notificationMarkAll")}</Button>
   {error&&<p role="alert" className="p-4 text-sm text-destructive">{error}</p>}
   {!error&&current.items.length===0&&<p className="p-6 text-sm text-muted-foreground">{t("notificationEmpty")}</p>}
   <ul className="max-h-96 overflow-y-auto divide-y">{current.items.map(item=><li key={item.id}><Button variant="ghost" disabled={busy} onClick={()=>void onSelect(item)} className="h-auto w-full justify-start gap-3 whitespace-normal rounded-none p-4 text-start">{item.severity!=="INFO"&&<CircleAlert className="size-4 shrink-0" aria-label={t(item.severity==="URGENT"?"notificationUrgent":"notificationAction")}/>}<span><span className={item.read_at?"font-normal":"font-semibold"}>{item.title}</span>{item.merge_count>1&&<span> &times;{item.merge_count}</span>}<time dateTime={item.created_at} className="mt-1 block text-xs text-muted-foreground">{new Date(item.created_at).toLocaleString()}</time></span></Button></li>)}</ul>
  </PopoverContent>
 </Popover>
}
