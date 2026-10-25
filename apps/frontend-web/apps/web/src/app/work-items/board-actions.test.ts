import { describe, expect, it } from "bun:test"
import type { WorkItem, WorkItemDetail } from "../../lib/iam-api"
import { boardAction, moveBoardWork } from "./board-actions"

const ready = { id: "7", project_id: "3", state: "READY", version: 4, paused_at: null, archived_at: null, is_container: false } as WorkItem

describe("guarded board transitions", () => {
  it("never turns a drag into an approval, result, runtime launch or cancellation", () => {
    for (const target of ["IN_PROGRESS", "ACCEPTED", "INTERNAL_QA", "CANCELLED", "REWORK", "PLAN_REVIEW"])
      expect(boardAction(ready, target, () => true)).toBe("blocked")
    expect(boardAction(ready, "CLAIMED", () => false)).toBe("blocked")
    for (const patch of [{ paused_at: "2026-10-21T00:00:00Z" }, { andon_active: true }, { is_container: true }, { archived_at: "2026-10-21T00:00:00Z" }])
      expect(boardAction({ ...ready, ...patch }, "CLAIMED", () => true)).toBe("blocked")
  })

  function fixture(patch: Partial<WorkItem> = {}, active = true) {
    let current = { ...ready, ...patch } as WorkItemDetail
    const calls: unknown[][] = []
    const api = {
      workItem: async () => current,
      project: async () => ({ stage: active ? "ACTIVE" : "CLOSED", archived_at: null }),
      session: async () => ({ subject: "11" }),
      claimWork: async (...args: unknown[]) => { calls.push(args); current = { ...current, state: "CLAIMED", version: 5 } },
      checkWorkReadiness: async (...args: unknown[]) => { calls.push(args) },
    } as unknown as Parameters<typeof moveBoardWork>[0]
    return { api, calls }
  }

  it("uses the current Human and exact displayed version for the canonical claim", async () => {
    const { api, calls } = fixture()
    const updated = await moveBoardWork(api, "3", ready, "CLAIMED", () => true, "safe-retry")
    expect(calls).toEqual([["7", 4, "11", false, "safe-retry"]])
    expect(updated.state).toBe("CLAIMED")
  })

  it("rejects stale, differently scoped, paused and closed work before any mutation", async () => {
    for (const patch of [{ version: 5 }, { project_id: "8" }, { state: "ACCEPTED" }, { paused_at: "2026-10-21T00:00:00Z" }]) {
      const { api, calls } = fixture(patch)
      await expect(moveBoardWork(api, "3", ready, "CLAIMED", () => true, "key")).rejects.toThrow()
      expect(calls).toEqual([])
    }
    const { api, calls } = fixture({}, false)
    await expect(moveBoardWork(api, "3", ready, "CLAIMED", () => true, "key")).rejects.toThrow()
    expect(calls).toEqual([])
  })

  it("rechecks readiness without pretending the requested state was reached", async () => {
    const draft = { ...ready, state: "DRAFT" }
    const { api, calls } = fixture({ state: "DRAFT" })
    const current = await moveBoardWork(api, "3", draft, "READY", () => true, "recheck")
    expect(calls).toEqual([["3", "7", 4, "recheck"]])
    expect(current.state).toBe("DRAFT")
  })
})
