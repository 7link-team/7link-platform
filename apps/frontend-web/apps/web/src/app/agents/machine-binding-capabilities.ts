import type { components } from "../../lib/api-schema"
import { ApiError } from "../../lib/iam-api"

export function rebindFailureDetails(cause: unknown): string[] {
  if (
    !(cause instanceof ApiError) ||
    cause.businessCode !== 142007 ||
    !cause.data ||
    typeof cause.data !== "object"
  )
    return []
  const unknown = (cause.data as { unknown?: unknown }).unknown
  return Array.isArray(unknown)
    ? unknown.filter((name): name is string => typeof name === "string")
    : []
}

export function rebindObservations(
  capabilities: components["schemas"]["MachineCapabilities"]
) {
  return (capabilities.discoveries ?? [])
    .filter((receipt) => receipt.scope_key === "home")
    .flatMap((receipt) =>
      (receipt.discovery ?? []).map((fact) => ({
        runtime: fact.runtime,
        kind: receipt.kind,
        source: fact.source,
        state: fact.state,
        checkedAt: fact.last_success_at,
        mcp: fact.mcp ?? [],
        reasoning: (fact.reasoning ?? []).map((model) => ({
          model: model.model,
          efforts: model.efforts ?? [],
        })),
      }))
    )
}
