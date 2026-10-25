import { apiRoute } from "../../lib/api-route"
import type { components } from "../../lib/api-schema"
import { iamApi } from "../../lib/iam-api"

export type Skill = components["schemas"]["SkillView"]
export type SkillVersion = components["schemas"]["SkillVersionView"]
export type SkillResult = components["schemas"]["SkillResult"]
export type SkillMount = components["schemas"]["AgentSkillView"]
export type SkillDistillation = components["schemas"]["Distillation"]
export function knowledgeSkillsApi(client: Pick<typeof iamApi, "deliveryResultRequest">) { return {
  distill: (tenant: string, work: string, agent: string, key: string) => client.deliveryResultRequest<SkillDistillation>(apiRoute("post", "/tenants/{tenant_id}/work-items/{work_item_id}/skill-distillations", {path: {tenant_id: tenant, work_item_id: work}}), {headers: {"Idempotency-Key": key}, body: JSON.stringify({agent_id: agent})}),
  distillation: (tenant: string, id: string, signal?: AbortSignal) => client.deliveryResultRequest<SkillDistillation>(apiRoute("get", "/tenants/{tenant_id}/skill-distillations/{distillation_id}", {path: {tenant_id: tenant, distillation_id: id}}), {signal}),
  list: (tenant: string, offset = 0, search = "", state?: "ACTIVE" | "ARCHIVED", signal?: AbortSignal) => client.deliveryResultRequest<components["schemas"]["SkillPage"]>(apiRoute("get", "/tenants/{tenant_id}/skills", {path: {tenant_id: tenant}, query: {offset, limit: 50, search, state}}), {signal}),
  get: (tenant: string, skill: string, signal?: AbortSignal) => client.deliveryResultRequest<Skill>(apiRoute("get", "/tenants/{tenant_id}/skills/{skill_id}", {path: {tenant_id: tenant, skill_id: skill}}), {signal}),
  create: (tenant: string, content: string, key: string) => client.deliveryResultRequest<SkillResult>(apiRoute("post", "/tenants/{tenant_id}/skills", {path: {tenant_id: tenant}}), {headers: {"Idempotency-Key": key}, body: JSON.stringify({content})}),
  state: (tenant: string, skill: string, version: number, state: "ACTIVE" | "ARCHIVED", key: string) => client.deliveryResultRequest<Skill>(apiRoute("patch", "/tenants/{tenant_id}/skills/{skill_id}", {path: {tenant_id: tenant, skill_id: skill}}), {headers: {"Idempotency-Key": key}, body: JSON.stringify({version, state})}),
  versions: (tenant: string, skill: string, offset = 0, search = "", signal?: AbortSignal) => client.deliveryResultRequest<components["schemas"]["SkillVersionPage"]>(apiRoute("get", "/tenants/{tenant_id}/skills/{skill_id}/versions", {path: {tenant_id: tenant, skill_id: skill}, query: {offset, limit: 50, search}}), {signal}),
  version: (tenant: string, version: string, signal?: AbortSignal) => client.deliveryResultRequest<SkillVersion>(apiRoute("get", "/tenants/{tenant_id}/skill-versions/{version_id}", {path: {tenant_id: tenant, version_id: version}}), {signal}),
  addVersion: (tenant: string, skill: string, content: string, key: string) => client.deliveryResultRequest<SkillResult>(apiRoute("post", "/tenants/{tenant_id}/skills/{skill_id}/versions", {path: {tenant_id: tenant, skill_id: skill}}), {headers: {"Idempotency-Key": key}, body: JSON.stringify({content})}),
  review: (tenant: string, version: string, decision: "APPROVED" | "REJECTED", reason: string, key: string) => client.deliveryResultRequest<SkillVersion>(apiRoute("post", "/tenants/{tenant_id}/skill-versions/{version_id}/reviews", {path: {tenant_id: tenant, version_id: version}}), {headers: {"Idempotency-Key": key}, body: JSON.stringify({decision, reason})}),
  mounts: (tenant: string, agent: string, offset = 0, search = "", signal?: AbortSignal) => client.deliveryResultRequest<components["schemas"]["AgentSkillPage"]>(apiRoute("get", "/tenants/{tenant_id}/agents/{agent_principal_id}/skills", {path: {tenant_id: tenant, agent_principal_id: agent}, query: {offset, limit: 50, search}}), {signal}),
  mount: (tenant: string, agent: string, skill: string, pin: string | undefined, key: string) => client.deliveryResultRequest<SkillMount>(apiRoute("put", "/tenants/{tenant_id}/agents/{agent_principal_id}/skills/{skill_id}", {path: {tenant_id: tenant, agent_principal_id: agent, skill_id: skill}}), {headers: {"Idempotency-Key": key}, body: JSON.stringify({pinned_version_id: pin})}),
  unmount: (tenant: string, agent: string, skill: string, key: string) => client.deliveryResultRequest<void>(apiRoute("delete", "/tenants/{tenant_id}/agents/{agent_principal_id}/skills/{skill_id}", {path: {tenant_id: tenant, agent_principal_id: agent, skill_id: skill}}), {headers: {"Idempotency-Key": key}}),
} }
export const knowledgeSkills = knowledgeSkillsApi(iamApi)
