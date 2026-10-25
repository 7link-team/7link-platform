import {iamApi} from "../../lib/iam-api"
import {collaborationID} from "../../lib/clientwork-api"
export type HostedAgent = {id: string; principal_id: string; name: string; handle: string; state: string; process_status: string; runtime: string; model: string; version: number}
export type MachineExecution = {id: string; project_id: string; work_item_id: string; state: string; result_state: string; runtime: string; model: string; created_at: string; updated_at: string}
export type HistoryPage<T> = {items: T[]; has_more: boolean; next_cursor?: string}
export const createMachineHistoryApi = (api: Pick<typeof iamApi, "deliveryResultRequest">) => ({
 agents: (tenant: string, machine: string, cursor?: string, signal?: AbortSignal) => api.deliveryResultRequest<HistoryPage<HostedAgent>>({method: "GET", url: `/tenants/${collaborationID(tenant)}/agents?${new URLSearchParams({machine_id: collaborationID(machine), limit: "30", ...(cursor ? {cursor} : {})})}`}, {signal}),
 executions: (machine: string, cursor?: string, signal?: AbortSignal) => api.deliveryResultRequest<HistoryPage<MachineExecution>>({method: "GET", url: `/machines/${collaborationID(machine)}/execution-history?${new URLSearchParams({limit: "30", ...(cursor ? {cursor} : {})})}`}, {signal}),
})
export const machineHistoryApi = createMachineHistoryApi(iamApi)
