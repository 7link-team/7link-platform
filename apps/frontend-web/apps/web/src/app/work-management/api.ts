import { iamApi } from "../../lib/iam-api"
export type Worklog = {
  timezone?: string | null
  confirmed_by?: string | null
  confirmed_at?: string | null
  work_item_id?: string
  id: string
  principal_id: string
  actor_kind: string
  category: string
  work_date: string
  minutes: number
  cost_micros: number | null
  execution_id?: string
  note: string
  state: string
  version: number
  approved_by?: string | null
  approved_at?: string | null
  created_at?: string
  updated_at?: string
}
export type State = {
  archived?: boolean
  migrate_to?: string
  key: string
  display_name: string
  group: string
  maps_to: string
}
export type Transition = {
  from: string
  to: string
  enabled: boolean
  manual_allowed: boolean
  conditions: unknown[]
}
export type Workflow = {
  carry_over_mode?: "NEXT_ITERATION" | "BACKLOG"
  source?: "SYSTEM" | "TEAM" | "PROJECT"
  team_version?: number
  id?: string
  work_item_type?: string
  version: number
  states: State[]
  transitions: Transition[]
}
export type Page<T> = { items: T[]; has_more: boolean; next_cursor?: string }
export type WorkflowPreview = {
  before: Workflow
  after: Workflow
  changed_states: string[]
  changed_transitions: string[]
  runtime_impact: "NOT_EVALUATED" | "EVALUATED"
  projects_affected?: number
  items_in_archived_states?: number
}
export type WorkflowDefinition = Pick<
  Workflow,
  "carry_over_mode" | "states" | "transitions"
>
export function workflowDefinition(workflow: Workflow): WorkflowDefinition {
  return {
    carry_over_mode: workflow.carry_over_mode,
    states: workflow.states,
    transitions: workflow.transitions,
  }
}
// A selected matrix cell may not exist yet. Retain its conditions even when
// navigation, rather than a checkbox toggle, creates the disabled cell.
export function applyCellConditions(
  workflow: Workflow,
  selected: { from: string; to: string } | null,
  conditions: string
): Workflow {
  if (!selected) return workflow
  const parsed: unknown = JSON.parse(conditions)
  if (!Array.isArray(parsed)) throw new Error("conditions")
  const enabled =
    workflow.transitions.find(
      (cell) => cell.from === selected.from && cell.to === selected.to
    )?.enabled ?? false
  const next = setCell(workflow, selected.from, selected.to, enabled)
  return {
    ...next,
    transitions: next.transitions.map((cell) =>
      cell.from === selected.from && cell.to === selected.to
        ? { ...cell, conditions: parsed }
        : cell
    ),
  }
}
export function previewWorkflow(
  url: string,
  version: number,
  definition: WorkflowDefinition,
  signal?: AbortSignal,
  remove = false
) {
  const [path, query] = url.split("?")
  return iamApi.deliveryResultRequest<WorkflowPreview>(
    { method: "POST", url: `${path}/previews${query ? `?${query}` : ""}` },
    { signal, body: JSON.stringify({ version, remove, definition }) }
  )
}
export type Summary = {
  human_confirmed_minutes?: number
  human_unconfirmed_minutes?: number
  agent_wall_minutes?: number
  legacy_approved_human_minutes?: number
  by_work_item?: Record<string, number>
  by_date?: Record<string, number>
  approved_minutes: number
  known_cost_micros: number
  unknown_cost_count: number
}

export function worklogPeriodQuery(
  workItemID: string,
  from: string,
  until: string
) {
  const query = new URLSearchParams()
  if (workItemID) query.set("work_item_id", workItemID)
  if (from) query.set("from", from)
  if (until) query.set("until", until)
  return query.toString()
}
export const basePath = (tenant: string, project: string) =>
  `/tenants/${encodeURIComponent(tenant)}/projects/${encodeURIComponent(project)}`
export const read = <T>(url: string, signal?: AbortSignal) =>
  iamApi.deliveryResultRequest<T>({ method: "GET", url }, { signal })
export const write = <T>(
  url: string,
  method: "POST" | "PATCH" | "PUT" | "DELETE",
  body: unknown,
  key: string,
  version?: number
) =>
  iamApi.deliveryResultRequest<T>(
    { method, url },
    {
      headers: {
        "Idempotency-Key": key,
        ...(version !== undefined ? { "If-Match": `"${version}"` } : {}),
      },
      body: JSON.stringify(body),
    }
  )
export function setCell(
  workflow: Workflow,
  from: string,
  to: string,
  enabled: boolean
): Workflow {
  const others = workflow.transitions.filter(
    (t) => t.from !== from || t.to !== to
  )
  const previous = workflow.transitions.find(
    (t) => t.from === from && t.to === to
  )
  return {
    ...workflow,
    transitions: [
      ...others,
      {
        from,
        to,
        enabled,
        manual_allowed: previous?.manual_allowed ?? true,
        conditions: previous?.conditions ?? [],
      },
    ],
  }
}
