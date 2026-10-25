import type {MachineListFilters} from "../../lib/iam-api"

export function machineListFilters(params: URLSearchParams): MachineListFilters {
  const state = params.get("state")
  const runtime = params.get("runtime")
  return {
    state: state === "ONLINE" || state === "OFFLINE" || state === "DRAINING" || state === "REVOKED" ? state : undefined,
    runtime: runtime === "CODEX" || runtime === "CLAUDE_CODE" ? runtime : undefined,
    free_slots: params.get("free_slots") === "true" || undefined,
  }
}
