import { iamApi } from "./iam-api"

export type Notification = { id: string; tenant_id: string; type: string; severity: "INFO" | "ACTION" | "URGENT"; title: string; body: string; link_path: string; merge_count: number; read_at: string | null; archived_at: string | null; created_at: string }
export type NotificationSummary = { unread: number; action_required: number; urgent: number; by_tenant: { tenant_id: string; unread: number }[] }
export type NotificationPreference = { tenant_id: string; type_key: string; in_app: boolean; browser_push: boolean; email: "OFF" | "IMMEDIATE" | "DAILY_DIGEST"; quiet_enabled: boolean; quiet_start_min: number; quiet_end_min: number; timezone: string; digest_hour: number; version: number }
const principalPath = (id: string) => `/principals/${encodeURIComponent(id)}`
const preferencePath = (principal: string, tenant: string, type: string) => `${principalPath(principal)}${tenant === "0" ? "/platform-notification-preferences" : `/tenants/${encodeURIComponent(tenant)}/notification-preferences`}/${encodeURIComponent(type)}`
export function safeNotificationLink(path: string) { return path.startsWith("/") && !path.startsWith("//") && !/[\\\r\n]/.test(path) ? path : "/user/notifications" }
export const notificationApi = {
 list: (principal: string, read = "", offset = 0, signal?: AbortSignal) => iamApi.deliveryResultPage<Notification>({ method: "GET", url: `${principalPath(principal)}/notifications?limit=20&offset=${offset}&read=${read}` }, signal),
 summary: (principal: string, signal?: AbortSignal) => iamApi.deliveryResultRequest<NotificationSummary>({method:"GET",url:`${principalPath(principal)}/notification-summary`}, {signal}),
 patch: (id: string, changes: { read?: boolean; archived?: boolean }) => iamApi.deliveryResultRequest<Notification>({method:"PATCH",url:`/notifications/${encodeURIComponent(id)}`},{body:JSON.stringify(changes)}),
 markRead: (principal: string, tenant: string, upTo: string) => iamApi.deliveryResultRequest<{ marked: number }>({method:"POST",url:`${principalPath(principal)}/notification-reads`},{body:JSON.stringify({tenant_id:tenant,up_to_id:upTo})}),
 preference: (principal: string, tenant: string, type = "*") => iamApi.deliveryResultRequest<NotificationPreference>({method:"GET",url:preferencePath(principal,tenant,type)}),
 savePreference: (principal: string, tenant: string, preference: NotificationPreference) => iamApi.deliveryResultRequest<NotificationPreference>({method:"PUT",url:preferencePath(principal,tenant,preference.type_key)},{headers:{"If-Match":String(preference.version)},body:JSON.stringify(preference)}),
}
