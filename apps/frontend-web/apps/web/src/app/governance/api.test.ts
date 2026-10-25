import { describe, expect, it } from "bun:test"
import {
  inboxKinds,
  inboxObjectHref,
  ruleNeedsStepUp,
  escalationRulePath,
  type InboxItem,
  type EscalationRule,
} from "./api"

describe("project escalation scope", () => {
  it("keeps tenant and project list/write URLs distinct", () => {
    expect(escalationRulePath()).toBe("/escalation-rules")
    expect(escalationRulePath(undefined, "99")).toBe("/escalation-rules?project_id=99")
    expect(escalationRulePath("LOW_SELF_CONFIDENCE", "99")).toBe("/escalation-rules/LOW_SELF_CONFIDENCE?project_id=99")
    for (const value of ["0", "../99", "99&project_id=1", "-1"]) expect(() => escalationRulePath(undefined, value)).toThrow()
  })
  it("requires proof only for actual project sensitive-path relaxation", () => {
    const old: EscalationRule = {kind: "SENSITIVE_PATH", version: 1, locked: false, enabled: true, params: {pattern_sets: [{key: "AUTH", enabled: true, globs: ["**/auth/**"]}]}, actions_autonomous: ["NOTIFY"], actions_hybrid: ["NOTIFY"]}
    const next = {...old, params: {pattern_sets: [{key: "AUTH", enabled: true, globs: ["**/auth/**", "**/security/**"]}]}}
    expect(ruleNeedsStepUp(old, next, true)).toBe(false)
    expect(ruleNeedsStepUp(old, next)).toBe(true)
    expect(ruleNeedsStepUp(next, old, true)).toBe(true)
    expect(ruleNeedsStepUp(old, {...old, params: {...old.params, max_paths_checked: 3000}}, true)).toBe(true)
  })
})

describe("governance source navigation", () => {
  const item = { object_id: "5", project_id: "3" } as InboxItem
  it("opens exact pending collaboration sources without confusing resource types", () => {
    expect(inboxObjectHref("1", {...item, id:"7", object_type:"execution", kind:"INTERACTION"})).toBe("/admin/1/entities/3/executions/5?approval_id=7")
    expect(inboxObjectHref("1", {...item, id:"7&other=1", object_type:"execution", kind:"INTERACTION"})).toBeNull()
    expect(inboxObjectHref("1", {...item, object_type: "escalation_incident", kind: "ESCALATION_ALERT"})).toBe("/admin/1/governance/policies?tab=incidents")
    expect(inboxObjectHref("1", {...item, object_type: "memory", kind: "MEMORY_RULE_APPROVAL"})).toBe("/admin/1/entities/3/governance/memory-review?entry_id=5")
    expect(inboxObjectHref("1", {...item, project_id: undefined, object_type: "memory", kind: "MEMORY_RULE_APPROVAL"})).toBe("/admin/1/governance/memory-review?entry_id=5")
    expect(inboxObjectHref("1", {...item, object_type: "memory", kind: "MEMORY_RULE_APPROVAL", source_scope: "AGENT", source_principal_id: "77"})).toBe("/admin/1/entities/3/governance/memory-review?entry_id=5&scope=AGENT&agent_principal_id=77")
    expect(inboxObjectHref("1", {...item, object_type: "memory", kind: "MEMORY_RULE_APPROVAL", source_scope: "AGENT"})).toBeNull()
    for (const [object_type, kind, path] of [
      ["review_plan", "PLAN_APPROVAL", "plan-review?plan_id=5"],
      ["dispatch_proposal", "DISPATCH_PROPOSAL", "dispatch?id=5"],
      ["tolerance_exception", "EXCEPTION_DECISION", "exceptions?collection=exceptions&id=5"],
      ["preview_share", "PREVIEW_SHARE_APPROVAL", "previews?collection=shares&id=5"],
    ] as const) {
      expect(inboxObjectHref("1", { ...item, object_type, kind })).toBe(`/admin/1/entities/3/${path}`)
      expect(inboxObjectHref("1", { ...item, object_type, kind: "WORK_ITEM_REVIEW" })).toBeNull()
    }
  })
  it("opens the exact review sheet from its invitation", () => {
    expect(inboxObjectHref("1", { ...item, object_type: "review_sheet", kind: "REVIEW_SHEET" })).toBe("/admin/1/entities/3/review-sheet?sheet=5")
    expect(inboxObjectHref("1", { ...item, object_type: "review_sheet", kind: "WORK_ITEM_REVIEW" })).toBeNull()
  })
  it("uses only canonical implemented source lists and valid identifiers", () => {
    for (const [object_type, suffix] of [
      ["work_item", "work-items?work=5"],
      ["plan", "plans"],
      ["report", "reports"],
      ["project", "project"],
    ]) {
      expect(inboxObjectHref("1", { ...item, object_type })).toBe(
        `/admin/1/entities/3/${suffix}${object_type === "plan" || object_type === "report" ? "?record_id=5" : ""}`
      )
    }
    for (const id of ["", "0", "-1", "../1", "1?x", "1/2"]) {
      expect(
        inboxObjectHref(id, { ...item, object_type: "work_item" })
      ).toBeNull()
      expect(
        inboxObjectHref("1", {
          ...item,
          project_id: id,
          object_type: "work_item",
        })
      ).toBeNull()
      expect(
        inboxObjectHref("1", {
          ...item,
          object_id: id,
          object_type: "work_item",
        })
      ).toBeNull()
    }
    expect(
      inboxObjectHref("1", { ...item, object_type: "javascript:alert(1)" })
    ).toBeNull()
    expect(
      inboxObjectHref("1", {
        ...item,
        object_type: "worklog",
        kind: "WORKLOG_APPROVAL",
      })
    ).toBe("/admin/1/entities/3/worklogs")
    expect(
      inboxObjectHref("1", {
        ...item,
        object_type: "quality_escape",
        kind: "QUALITY_ESCAPE",
      })
    ).toBe("/admin/1/entities/3/escapes?escape_id=5")
    expect(
      inboxObjectHref("1", {
        ...item,
        object_type: "quality_escape",
        kind: "WORK_ITEM_REVIEW",
      })
    ).toBeNull()
    expect(new Set(inboxKinds).size).toBe(26)
    expect(inboxObjectHref("1", { ...item, project_id: undefined, object_type: "spawn_request", kind: "AGENT_SPAWN_APPROVAL" })).toBe("/admin/1/governance/agent-operations?request_id=5")
    expect(inboxObjectHref("1", { ...item, object_type: "spawn_request", kind: "WORK_ITEM_REVIEW" })).toBeNull()
    expect(
      inboxObjectHref("1", {
        ...item,
        object_type: "execution",
        kind: "EXECUTION_ATTENTION",
      })
    ).toBe("/admin/1/entities/3/executions/5")
    expect(
      inboxObjectHref("1", {
        ...item,
        object_type: "execution",
        kind: "HUMAN_VERIFICATION",
      })
    ).toBeNull()
    expect(
      inboxObjectHref("1", {
        ...item,
        object_type: "report",
        source_scope: "TEAM",
        project_id: undefined,
      })
    ).toBe("/admin/1/reports?scope=TEAM&record_id=5")
    expect(
      inboxObjectHref("1", {
        ...item,
        object_type: "report",
        source_scope: "PROJECT",
      })
    ).toBe("/admin/1/entities/3/reports?scope=PROJECT&record_id=5")
    expect(
      inboxObjectHref("1", {
        ...item,
        object_type: "plan",
        source_scope: "MEMBER",
        source_principal_id: "4",
      })
    ).toBe("/admin/1/entities/3/plans?principal_id=4&record_id=5")
    expect(
      inboxObjectHref("1", {
        ...item,
        object_type: "report",
        source_principal_id: "../4",
      })
    ).toBeNull()
  })
})

describe("rule relaxation step-up", () => {
  const base: EscalationRule = {
    kind: "EXECUTION_STALLED",
    version: 1,
    locked: false,
    enabled: true,
    params: { stall_minutes: 30 },
    actions_autonomous: ["NOTIFY"],
    actions_hybrid: ["NOTIFY"],
  }
  it("requires proof for disabling, removing actions and less restrictive thresholds", () => {
    expect(ruleNeedsStepUp(base, { ...base, enabled: false })).toBe(true)
    expect(ruleNeedsStepUp(base, { ...base, actions_hybrid: [] })).toBe(true)
    expect(
      ruleNeedsStepUp(base, { ...base, params: { stall_minutes: 31 } })
    ).toBe(true)
    expect(
      ruleNeedsStepUp(base, { ...base, params: { stall_minutes: 29 } })
    ).toBe(false)
    const confidence = {
      ...base,
      kind: "LOW_SELF_CONFIDENCE",
      params: { min_confidence: 0.7 },
    }
    expect(
      ruleNeedsStepUp(confidence, {
        ...confidence,
        params: { min_confidence: 0.6 },
      })
    ).toBe(true)
    expect(
      ruleNeedsStepUp(confidence, {
        ...confidence,
        params: { min_confidence: 0.8 },
      })
    ).toBe(false)
  })
})

it("preserves exact work and ensemble plan identity instead of opening an unselected list",()=>{
 const base:InboxItem={id:"8",kind:"ENSEMBLE_SELECTION",object_id:"5",project_id:"3",object_type:"work_item",title:"Comparison",priority:"HIGH",created_at:"2026-10-23T00:00:00Z"}
 expect(inboxObjectHref("1",base)).toBe("/admin/1/entities/3/heavy?work_item_id=5&plan_id=8")
 expect(inboxObjectHref("1",{...base,kind:"WORK_ITEM_REVIEW"})).toBe("/admin/1/entities/3/work-items?work=5")
 expect(inboxObjectHref("1",{...base,id:"8&plan_id=9"})).toBeNull()
})
