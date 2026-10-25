import type { WorkItemDetail } from "../../lib/iam-api"
import type { WorkResult } from "./results-api"

export type RecoveryWork = Omit<
  WorkItemDetail,
  "baseline_stale" | "pause_reason" | "current_execution_id" | "defect_stage"
> & {
  baseline_stale?: boolean
  pause_reason?: string | null
  current_execution_id?: string | null
  defect_stage?: string | null
}
export type Action =
  "resume" | "baseline" | "scope" | "VERIFIED" | "CLOSED" | "reopen"

export function recoveryActions(
  work: RecoveryWork,
  allowed: (code: string) => boolean,
  result?: WorkResult,
  subject?: string
): Action[] {
  if (!allowed("work_item_read") || work.is_container) return []
  const actions: Action[] = []
  // Missing execution facts fail closed while older servers are still deployed.
  const manual = work.current_execution_id === null
  const progressHold =
    ["ESCALATION_RULE", "AUTONOMY_SPINNING"].includes(
      work.pause_reason ?? ""
    ) &&
    (work.preparation?.block_reasons ?? []).some((reason) =>
      [
        "AUTONOMY_NO_PROGRESS",
        "AUTONOMY_DEPENDENCY_MISSING",
        "AUTONOMY_SPINNING",
      ].includes(reason)
    )
  if (
    progressHold &&
    work.paused_at &&
    !["ACCEPTED", "CANCELLED"].includes(work.state) &&
    allowed("decision_pause")
  )
    actions.push("resume")
  if (
    manual &&
    work.state === "REWORK" &&
    work.paused_at &&
    work.pause_reason === "AUTONOMY_REWORK_LIMIT" &&
    allowed("decision_pause")
  )
    actions.push("resume")
  if (
    manual &&
    work.requirement_id &&
    !work.paused_at &&
    ["DRAFT", "READY", "BLOCKED", "CLAIMED", "IN_PROGRESS", "REWORK"].includes(
      work.state
    ) &&
    allowed("work_item_write") &&
    allowed("requirement_read")
  )
    actions.push("baseline")
  const current = result && result.id === work.current_claim_id
  const independent = current && !!subject && result.actor_id !== subject
  if (
    manual &&
    independent &&
    work.state === "NEEDS_HUMAN_REVIEW" &&
    work.baseline_stale === true &&
    allowed("decision_request_changes")
  )
    actions.push("scope")
  const latestCheck = result?.actions
    ?.filter((a) => a.kind === "CHECK" && a.actor_id === subject)
    .at(-1)
  if (
    independent &&
    ["BUG", "INCIDENT"].includes(work.type) &&
    work.state === "ACCEPTED" &&
    work.accepted_at &&
    work.accepted_decision_id &&
    work.baseline_stale === false &&
    allowed("work_item_write") &&
    allowed("decision_approve") &&
    latestCheck?.verdict === "PASSED"
  ) {
    if (work.defect_stage === "VERIFIED") actions.push("CLOSED")
    else if (
      work.defect_stage === null ||
      ["MERGED", "DEPLOYED"].includes(work.defect_stage ?? "")
    )
      actions.push("VERIFIED")
  }
  return actions
}
