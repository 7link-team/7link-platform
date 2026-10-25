import { iamApi } from "../../lib/iam-api"
import { intakeChannelMessages } from "./messages"
export type ChannelSettings = {
	callback_subscription_id?: string
  triage_agent_id?: string
  notify_principal_ids?: string[]
  enabled: boolean
  display_name: string
  support_email: string
  language: string
  daily_limit: number
  receipt_enabled: boolean
  reminder_hours: number
  timeout_hours: number
}
export type IntakeChannel = {
  id: string
  kind: string
  version: number
  settings: ChannelSettings
  availability: "AVAILABLE" | "UNAVAILABLE"
  credential_configured: boolean
}
export const channelPath = (tenant: string, project: string) =>
  `/tenants/${encodeURIComponent(tenant)}/projects/${encodeURIComponent(project)}/intake-channels`
export function channelSettings(
  values: Record<string, unknown>,
  callbackError: string = intakeChannelMessages["en-US"].callbackError
): ChannelSettings {
  const daily = Number(values.daily_limit),
    reminder = Number(values.reminder_hours),
    timeout = Number(values.timeout_hours),
    triageAgent = String(values.triage_agent_id ?? "").trim()
	const callback = String(values.callback_subscription_id ?? "").trim()
	if (callback && (!/^[1-9][0-9]{0,18}$/.test(callback) || BigInt(callback) > 9223372036854775807n)) throw new Error(callbackError)
  const notify = Array.isArray(values.notify_principal_ids) ? values.notify_principal_ids.map(String) : String(values.notify_principal_ids ?? "").split(/[\s,]+/).filter(Boolean)
  if (notify.length > 50 || new Set(notify).size !== notify.length || notify.some(id => !/^[1-9][0-9]{0,18}$/.test(id))) throw new Error("Invalid notification recipients")
  if (
    !Number.isSafeInteger(daily) ||
    daily < 1 ||
    daily > 10000 ||
    !Number.isSafeInteger(reminder) ||
    reminder < 1 ||
    !Number.isSafeInteger(timeout) ||
    timeout <= reminder ||
    timeout > 8760 ||
    (triageAgent !== "" && !/^[1-9][0-9]{0,18}$/.test(triageAgent))
  )
    throw new Error("Invalid channel limits or reminder interval")
  return {
	...(callback ? {callback_subscription_id: callback} : {}),
    ...(triageAgent ? { triage_agent_id: triageAgent } : {}),
    ...(notify.length ? {notify_principal_ids: notify} : {}),
    enabled: values.enabled === true,
    display_name: String(values.display_name ?? ""),
    support_email: String(values.support_email ?? ""),
    language: String(values.language ?? ""),
    daily_limit: daily,
    receipt_enabled: values.receipt_enabled === true,
    reminder_hours: reminder,
    timeout_hours: timeout,
  }
}
export const intakeChannelsApi = (
  api: Pick<typeof iamApi, "deliveryResultRequest"> = iamApi
) => ({
  rotateCredential: (tenant: string, project: string, version: number, key: string) =>
    api.deliveryResultRequest<{ channel: IntakeChannel; credential?: string }>(
      { method: "POST", url: `${channelPath(tenant, project)}/API/credential` },
      { headers: { "Idempotency-Key": key, "If-Match": `"${version}"` }, body: "{}" }
    ),
  receive: (tenant: string, project: string, credential: string, input: { contact_id?: string; title: string; content: string; format: "PLAIN" | "MARKDOWN" }, key: string) =>
    api.deliveryResultRequest<{ id: string; state: string; version: number }>(
      { method: "POST", url: `${channelPath(tenant, project)}/API/submissions` },
      { headers: { "Idempotency-Key": key, "X-Intake-Key": credential }, body: JSON.stringify(input) }
    ),
  list: (tenant: string, project: string, signal?: AbortSignal) =>
    api.deliveryResultRequest<IntakeChannel[]>(
      { method: "GET", url: channelPath(tenant, project) },
      { signal }
    ),
  save: (
    tenant: string,
    project: string,
    kind: string,
    version: number,
    settings: ChannelSettings,
    key: string
  ) =>
    api.deliveryResultRequest<IntakeChannel>(
      {
        method: "PUT",
        url: `${channelPath(tenant, project)}/${encodeURIComponent(kind)}`,
      },
      {
        headers: {
          "Idempotency-Key": key,
          ...(version > 0 ? { "If-Match": `"${version}"` } : {}),
        },
        body: JSON.stringify(settings),
      }
    ),
})
