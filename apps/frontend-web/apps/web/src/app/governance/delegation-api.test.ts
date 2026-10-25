import { describe, expect, it, spyOn } from "bun:test"
import { delegationActions, delegationApi, delegationIsCurrent, delegationPageSearch, scheduledDelegationStart, type Delegation } from "./delegation-api"
import { iamApi } from "../../lib/iam-api"
import { tenantSystemNavigation, workspaceArea } from "../../lib/navigation"

describe("delegation lifecycle presentation", () => {
  it("requests a keyset lookahead and never renders the extra row", async () => {
    const rows = Array.from({length: 51}, (_, index) => ({id: String(100 - index)} as Delegation))
    const transport = spyOn(iamApi, "deliveryResultPage").mockResolvedValue({items: rows, total: 200})
    try {
      const signal = new AbortController().signal
      const page = await delegationApi("1").list({project_id: "99", state: "SCHEDULED", before_id: "101"}, signal)
      expect(transport).toHaveBeenCalledWith({method: "GET", url: "/tenants/1/delegations?limit=51&before_id=101&project_id=99&state=SCHEDULED"}, signal)
      expect(page.items).toHaveLength(50)
      expect(page.items.at(-1)?.id).toBe("51")
      expect(page.hasMore).toBe(true)
      transport.mockResolvedValue({items: rows.slice(0, 50), total: 50})
      expect((await delegationApi("1").list({})).hasMore).toBe(false)
    } finally {transport.mockRestore()}
  })
  it("restores keyset boundaries and scope through next, back and refresh", () => {
    const initial = new URLSearchParams("project_id=99&state=SCHEDULED&offset=50&delegation_id=101")
    const first = delegationPageSearch(initial, "100")
    const second = delegationPageSearch(new URLSearchParams(first.toString()), "50")
    expect(second.getAll("before_id")).toEqual(["100", "50"])
    expect(second.get("project_id")).toBe("99")
    expect(second.get("state")).toBe("SCHEDULED")
    expect(second.has("offset")).toBe(false)
    expect(second.has("delegation_id")).toBe(false)
    expect(delegationPageSearch(second).toString()).toBe(first.toString())
    expect(delegationPageSearch(first).getAll("before_id")).toEqual([])
    expect(initial.get("offset")).toBe("50")
  })
  const window = {state: "ACTIVE" as const, starts_at: "2040-01-01T00:00:00Z", ends_at: "2040-01-01T01:00:00Z"}
  it("never presents expired or revoked authority as current", () => {
    expect(delegationIsCurrent(window, Date.parse(window.starts_at) - 1)).toBe(false)
    expect(delegationIsCurrent(window, Date.parse(window.starts_at))).toBe(true)
    expect(delegationIsCurrent(window, Date.parse(window.ends_at) - 1)).toBe(true)
    expect(delegationIsCurrent(window, Date.parse(window.ends_at))).toBe(false)
    expect(delegationIsCurrent({...window, state: "REVOKED"}, Date.parse(window.starts_at))).toBe(false)
    expect(delegationIsCurrent({...window, state: "EXPIRED"}, Date.parse(window.starts_at))).toBe(false)
    expect(delegationIsCurrent({...window, state: "SCHEDULED"}, Date.parse(window.starts_at))).toBe(false)
    expect(delegationIsCurrent({...window, ends_at: "invalid"})).toBe(false)
  })
  it("serializes optional future start without confusing invalid dates with immediate grants", () => {
    const now = Date.parse("2039-01-01T00:00:00Z")
    expect(scheduledDelegationStart("", now)).toBeUndefined()
    expect(scheduledDelegationStart("2040-01-01T00:00:00+08:00", now)).toBe("2039-12-31T16:00:00.000Z")
    for (const value of ["invalid", "2038-01-01T00:00:00Z", "2039-01-01T00:00:00Z"]) expect(() => scheduledDelegationStart(value, now)).toThrow()
  })
  it("keeps the canonical detail page in governance and hides navigation without read permission", () => {
    const route = "/governance/delegations"
    expect(workspaceArea(route)).toBe("governance")
    expect(tenantSystemNavigation(new Set(), false, false).some(item => item.path === route)).toBe(false)
    expect(tenantSystemNavigation(new Set(), false, false, false, false, false, false, ["policy_read"]).filter(item => item.path === route)).toHaveLength(1)
    expect(tenantSystemNavigation(new Set([route]), false, false, false, false, false, false, ["policy_read"]).filter(item => item.path === route)).toHaveLength(0)
    expect(delegationActions).toEqual(["decision_approve", "decision_reject", "decision_request_changes", "code_merge", "intake_convert"])
  })
})
