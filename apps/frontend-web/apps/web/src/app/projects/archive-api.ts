import { iamApi, type ProjectDetail } from "../../lib/iam-api"
import type { components } from "../../lib/api-schema"
import { collaborationID } from "../../lib/clientwork-api"

export type ArchiveResource = components["schemas"]["ProjectArchiveResource"]
export type ArchivePreflight = Omit<components["schemas"]["ProjectArchivePreflight"], "resources" | "unavailable_owners"> & {resources: ArchiveResource[]; unavailable_owners: string[]}

export const createProjectArchiveApi = (api: Pick<typeof iamApi, "deliveryResultRequest">) => ({
  preflight: (project: string, signal?: AbortSignal) => api.deliveryResultRequest<ArchivePreflight>({method: "GET", url: `/entities/${collaborationID(project)}/project/archive-preflight`}, {signal}),
  change: (project: string, version: number, restore: boolean, reason: string, key: string, hash?: string) => {
    if (!Number.isSafeInteger(version) || version < 1 || Array.from(reason.trim()).length < 10 || Array.from(reason).length > 2000 || !key || (!restore && !/^[a-f0-9]{64}$/.test(hash ?? ""))) throw new Error("Invalid archive command")
    return api.deliveryResultRequest<ProjectDetail>({method: "POST", url: `/entities/${collaborationID(project)}/project/${restore ? "restore" : "archive"}`}, {headers: {"If-Match": `"${version}"`, "Idempotency-Key": key}, body: JSON.stringify({reason, ...(!restore ? {preflight_hash: hash} : {})})})
  },
})
export const projectArchiveApi = createProjectArchiveApi(iamApi)

// Dispositions use existing owner commands; following a link never records a
// receipt. Refresh always reads the owner's actual persisted state again.
export function archiveResourceHref(tenant: string, project: string, resource: ArchiveResource) {
  const root = `/admin/${collaborationID(tenant)}`
  const scoped = `${root}/entities/${collaborationID(project)}`
  const id = collaborationID(resource.id)
  if (resource.kind === "WORK_ITEM") return `${scoped}/work-items?work=${id}`
  if (resource.kind === "EXECUTION") return `${scoped}/executions/${id}`
  if (resource.kind === "PLAN_CONFIRMATION") return `${scoped}/plans/${id}`
  if (resource.kind === "REPORT_CONFIRMATION") return `${scoped}/reports`
  if (resource.kind === "MEMORY_APPROVAL") return `${scoped}/governance/memory-review`
  if (resource.kind === "CONSTITUTION_REVIEW") return `${scoped}/project?view=constitution`
  if (resource.kind === "WORK_PLAN_REVIEW") return `${scoped}/plan-review`
  if (resource.kind.includes("DELEGATION")) return `${root}/governance/delegations`
  if (resource.kind.includes("SPAWN")) return `${root}/governance/spawn-grants`
  return null
}
