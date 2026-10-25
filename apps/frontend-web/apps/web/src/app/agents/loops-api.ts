import { apiRoute } from "../../lib/api-route"
import type { components } from "../../lib/api-schema"
import { iamApi } from "../../lib/iam-api"

export type Loop = components["schemas"]["LoopContractView"]
export type LoopCreate = components["schemas"]["LoopCreateInputBody"]
export type LoopPatch = components["schemas"]["LoopPatch"]
export type LoopRun = components["schemas"]["LoopRunView"]

export function loopApi(client: Pick<typeof iamApi, "deliveryResultRequest">) {
  return {
    list: (tenant: string, agent: string, cursor?: string, signal?: AbortSignal) => client.deliveryResultRequest<components["schemas"]["LoopContractPage"]>(apiRoute("get", "/tenants/{tenant_id}/loop-contracts", {path: {tenant_id: tenant}, query: {agent_id: agent, limit: 50, ...(cursor ? {cursor} : {})}}), {signal}),
    create: (tenant: string, body: LoopCreate, key: string) => client.deliveryResultRequest<Loop>(apiRoute("post", "/tenants/{tenant_id}/loop-contracts", {path: {tenant_id: tenant}}), {headers: {"Idempotency-Key": key}, body: JSON.stringify(body)}),
    update: (id: string, version: number, body: LoopPatch, key: string) => client.deliveryResultRequest<Loop>(apiRoute("patch", "/loop-contracts/{loop_contract_id}", {path: {loop_contract_id: id}}), {headers: {"Idempotency-Key": key, "If-Match": `"${version}"`}, body: JSON.stringify(body)}),
    state: (id: string, version: number, state: "ACTIVE" | "PAUSED" | "ARCHIVED", key: string) => client.deliveryResultRequest<Loop>(apiRoute("put", "/loop-contracts/{loop_contract_id}/state", {path: {loop_contract_id: id}}), {headers: {"Idempotency-Key": key, "If-Match": `"${version}"`}, body: JSON.stringify({state})}),
    runs: (id: string, cursor?: string, signal?: AbortSignal) => client.deliveryResultRequest<components["schemas"]["LoopRunPage"]>(apiRoute("get", "/loop-contracts/{loop_contract_id}/runs", {path: {loop_contract_id: id}, query: {limit: 50, ...(cursor ? {cursor} : {})}}), {signal}),
  }
}

export const agentLoops = loopApi(iamApi)
