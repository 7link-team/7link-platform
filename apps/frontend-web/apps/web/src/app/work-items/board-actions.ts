import { type iamApi, type WorkItem } from "../../lib/iam-api"

export function boardAction(row: WorkItem, target: string, allowed: (code: string) => boolean) {
  if (row.paused_at || row.andon_active || row.is_container || row.archived_at) return "blocked"
  if (target === "CLAIMED" && row.state === "READY" && allowed("work_item_execute")) return "claim"
  if (target === "READY" && ["DRAFT", "BLOCKED"].includes(row.state) && allowed("work_item_read")) return "readiness"
  return "blocked"
}

export async function moveBoardWork(api: Pick<typeof iamApi, "workItem" | "project" | "session" | "claimWork" | "checkWorkReadiness">, project: string, row: WorkItem, target: string, allowed: (code: string) => boolean, key: string) {
  const action = boardAction(row, target, allowed)
  if (action === "blocked" || !row.id) throw new Error("workBoardGuarded")
  const [current, facts, session] = await Promise.all([api.workItem(project, row.id), api.project(project), api.session()])
  if (!current.id || current.project_id !== project || current.id !== row.id || current.version !== row.version || current.state !== row.state) throw new Error("workBoardStale")
  if (facts.stage !== "ACTIVE" || facts.archived_at || boardAction(current, target, allowed) !== action) throw new Error("workBoardGuarded")
  if (action === "claim") {
    if (!session.subject) throw new Error("workBoardGuarded")
    await api.claimWork(current.id, current.version, session.subject, false, key)
  } else await api.checkWorkReadiness(project, current.id, current.version, key)
  return api.workItem(project, current.id)
}
