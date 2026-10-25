import {iamApi} from "../../lib/iam-api"
import {collaborationID} from "../../lib/clientwork-api"

export type EnrollmentStatus = {id: string; name: string; os: string; state: string; expires_at: string; server_time: string; machine_id?: string; machine_state?: string; connection_delayed?: boolean; facts?: {hostname: string; arch: string; runtimes: {kind: string; available: boolean; version?: string; error?: string}[]}}
export type EnrollmentSettings = {available: boolean; install: string; login: string; start: string}
export const createEnrollmentApi = (api: Pick<typeof iamApi, "deliveryResultRequest" | "decideMachineEnrollment">) => ({
  settings: (tenant: string, os: string, signal?: AbortSignal) => api.deliveryResultRequest<EnrollmentSettings>({method: "GET", url: `/tenants/${collaborationID(tenant)}/machine-enrollment-settings?os=${encodeURIComponent(os)}`}, {signal}),
  status: (tenant: string, id: string, signal?: AbortSignal) => api.deliveryResultRequest<EnrollmentStatus>({method: "GET", url: `/tenants/${collaborationID(tenant)}/machine-enrollments/${collaborationID(id)}`}, {signal}),
  lookup: (tenant: string, code: string, signal?: AbortSignal) => api.deliveryResultRequest<EnrollmentStatus>({method: "GET", url: `/tenants/${collaborationID(tenant)}/machine-enrollments?${new URLSearchParams({user_code: code.trim().toUpperCase()})}`}, {signal}),
  decide: (tenant: string, id: string, code: string, approve: boolean, name?: string) => api.decideMachineEnrollment(tenant, id, code, approve, name),
})
export const enrollmentApi = createEnrollmentApi(iamApi)
