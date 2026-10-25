// Real notification components with explicit API doubles; browser UI evidence,
// separate from backend SMTP, transaction, and authorization acceptance tests.
import { useState } from "react"
import { createRoot } from "react-dom/client"
import { MemoryRouter, Outlet, Route, Routes } from "react-router"
import { IntlAppProvider } from "@workspace/ui/i18n/intl-provider"
import uiEN from "@workspace/ui/i18n/messages/en-US.json"
import uiZH from "@workspace/ui/i18n/messages/zh-CN.json"
import uiMS from "@workspace/ui/i18n/messages/ms-MY.json"
import en from "../../src/messages/en-US.json"
import zh from "../../src/messages/zh-CN.json"
import ms from "../../src/messages/ms-MY.json"
import NotificationsPage from "../../src/app/notifications/page"
import { AuthContext, type AuthContextValue } from "../../src/components/auth"
import { setTenant } from "../../src/lib/iam-api"
import { notificationApi, type Notification, type NotificationPreference } from "../../src/lib/notification-api"
import "../../src/index.css"

const locale = new URLSearchParams(location.search).get("locale") ?? "en-US"
const messages = locale === "zh-CN" ? { ...uiZH, iam: zh } : locale === "ms-MY" ? { ...uiMS, iam: ms } : { ...uiEN, iam: en }
setTenant("7")
const calls: unknown[] = []
Object.assign(window, { notificationCalls: calls })
let fail = false
const notices: Notification[] = Array.from({ length: 23 }, (_, index) => ({ id: String(1000-index), tenant_id: index%2 ? "7" : "0", type: "machine.offline", severity: index===0 ? "URGENT" : "ACTION", title: `Machine notice ${1000-index}`, body: "Current actionable notification with a safe application link.", link_path: "/user/notifications", merge_count: 1, read_at: null, archived_at: null, created_at: "2026-10-20T12:00:00Z" }))
notificationApi.list = async (principal, filter = "", offset = 0) => {
 calls.push(["list", principal, filter, offset]); if(fail)throw Error("fixture outage")
 const rows = principal === "12" ? [] : notices.filter(row=>!row.archived_at && (filter==="unread"?!row.read_at:filter==="read"?Boolean(row.read_at):true))
 return { items: rows.slice(offset,offset+20), total: rows.length }
}
notificationApi.summary = async () => ({unread:23,action_required:22,urgent:1,by_tenant:[{tenant_id:"0",unread:12},{tenant_id:"7",unread:11}]})
notificationApi.patch = async (id, changes) => { calls.push(["patch",id,changes]); const row=notices.find(row=>row.id===id)!; if(changes.read!==undefined)row.read_at=changes.read?new Date().toISOString():null;if(changes.archived)row.archived_at=new Date().toISOString();return row }
notificationApi.markRead = async (principal,tenant,upTo)=>{calls.push(["markRead",principal,tenant,upTo]);for(const row of notices)if(row.tenant_id===tenant&&BigInt(row.id)<=BigInt(upTo))row.read_at=new Date().toISOString();return {marked:23}}
notificationApi.preference = async (principal,tenant,type="*")=>{calls.push(["preference",principal,tenant,type]);return {tenant_id:tenant,type_key:type,in_app:true,browser_push:false,email:"OFF",quiet_enabled:false,quiet_start_min:1320,quiet_end_min:480,timezone:"UTC",digest_hour:9,version:0}}
notificationApi.savePreference = async (principal,tenant,preference:NotificationPreference)=>{calls.push(["savePreference",principal,tenant,preference]);if(fail)throw Error("fixture conflict");return {...preference,version:preference.version+1}}
const unsupported = async (): Promise<never> => { throw Error("unused authentication fixture method") }
export function Fixture(){
 const [subject,setSubject]=useState("11")
 const auth:AuthContextValue={session:{subject,issuer:"fixture",acr:1},status:"authenticated",login:unsupported,requestPhoneLoginCode:unsupported,loginWithPhoneCode:unsupported,requestMfaPhoneCode:unsupported,verifyLoginMfa:unsupported,loginWithPasskey:unsupported,logout:unsupported,refresh:unsupported}
 return <AuthContext.Provider value={auth}><div className="flex gap-2 p-4"><button onClick={()=>setSubject(subject==="11"?"12":"11")}>Switch account</button><button onClick={()=>{fail=!fail}}>Toggle failure</button></div><Outlet context={{permissions:new Set(["system_job_manage","system_event_manage"]),permissionsResolved:true,menus:[]}}/></AuthContext.Provider>
}
createRoot(document.getElementById("root")!).render(<IntlAppProvider initialLocale={locale as "en-US"} initialMessages={messages}><MemoryRouter initialEntries={["/user/notifications"]}><Routes><Route element={<Fixture/>}><Route path="/user/notifications" element={<NotificationsPage/>}/></Route></Routes></MemoryRouter></IntlAppProvider>)
