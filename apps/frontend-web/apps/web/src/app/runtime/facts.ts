import type { components } from "../../lib/api-schema"

/** Remote sessions are reached from the same restorable execution workspace. */
export function workspaceTab(value: string | null) {
  return value && ["logs", "evidence", "workdirs", "remote"].includes(value) ? value : "logs"
}

/** Only an exact server-issued ID and project match identifies the execution directory. */
export function executionWorkdirs(execution: components["schemas"]["ExecutionView"], facts: components["schemas"]["MachineCapabilities"]) {
  return (facts.workdirs ?? []).filter(row => Boolean(execution.plan.workdir_id) && row.id === execution.plan.workdir_id && row.project_id === execution.project_id)
}

/** Date inputs represent UTC, with an exclusive end bound, as required by the API. */
export function metricPeriod(from: string, to: string) {
  const valid = (value: string) => /^\d{4}-\d{2}-\d{2}$/.test(value) && !Number.isNaN(Date.parse(value)) && new Date(value).toISOString().slice(0, 10) === value
  return valid(from) && valid(to) && from < to ? {from, to} : null
}
