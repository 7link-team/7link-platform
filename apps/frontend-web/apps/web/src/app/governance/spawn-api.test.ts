import { describe, expect, it } from "bun:test"
import { spawnFormInput } from "./spawn-api"
import { tenantSystemNavigation, workspaceArea } from "../../lib/navigation"
import zh from "../../messages/zh-CN.json"
import en from "../../messages/en-US.json"
import ms from "../../messages/ms-MY.json"

const form = {supervisor: "9007199254740993", projects: "99, 100", machines: "5", codex_models: "model-a", claude_models: "model-b", roles: ["EXECUTOR"], active: 3, daily: 5, budget: 50000000, per_agent: 15000000, ttl: 168, idle: 48, effort: "medium", approval: "REQUIRE_APPROVAL", duration: 168, reason: "Approved bounded capacity", team_confirm: false}

describe("spawn grant form boundaries", () => {
  it("ships readable labels in all three locales", () => {
    for (const [key, value] of Object.entries(zh).filter(([key]) => key.startsWith("spawn") || key.startsWith("delegation"))) {
      expect(value).not.toMatch(/^\?+[\s?]*$/)
      expect(key in en).toBe(true)
      expect(key in ms).toBe(true)
    }
    expect(zh.spawnTitle).toBe("编制授权")
  })
  it("preserves exact snowflake IDs and separates actual runtime inventories", () => {
    const value = spawnFormInput(form)
    expect(value.grantee_agent_id).toBe("9007199254740993")
    expect(value.project_ids).toEqual(["99", "100"])
    expect(value.allowed_models).toEqual([{runtime: "CODEX", model: "model-a"}, {runtime: "CLAUDE_CODE", model: "model-b"}])
  })
  it("rejects unsafe costs, review roles, quota overflow, and automatic team grants", () => {
    for (const patch of [{budget: "9007199254740993"}, {per_agent: 50000001}, {roles: ["REVIEWER"]}, {roles: ["LEAD"]}, {active: 11}, {daily: 21}, {projects: "", approval: "AUTO"}, {supervisor: "1e3"}, {machines: ""}]) {
      expect(() => spawnFormInput({...form, ...patch})).toThrow()
    }
  })
  it("uses the single governance navigation area and requires current read permission", () => {
    const route = "/governance/spawn-grants"
    expect(workspaceArea(route)).toBe("governance")
    expect(tenantSystemNavigation(new Set(), false, false).some(item => item.path === route)).toBe(false)
    expect(tenantSystemNavigation(new Set(), false, false, false, false, false, false, ["policy_read"]).filter(item => item.path === route)).toHaveLength(1)
  })
})
