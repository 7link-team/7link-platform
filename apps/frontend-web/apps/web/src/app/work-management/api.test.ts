import { describe, expect, it, spyOn } from "bun:test"
import {
  basePath,
  setCell,
  worklogPeriodQuery,
  write,
  previewWorkflow,
  workflowDefinition,
  applyCellConditions,
  type Workflow,
} from "./api"
import { messages } from "./messages"
import { planningLabels } from "./planning-labels"
import { iamApi } from "../../lib/iam-api"
describe("workflow matrix", () => {
  it("previews the exact unsaved definition and version in the selected scope", async () => {
    const request = spyOn(iamApi, "deliveryResultRequest").mockResolvedValue({})
    const signal = new AbortController().signal
    const definition = {
      carry_over_mode: "BACKLOG" as const,
      states: [],
      transitions: [],
    }
    try {
      await previewWorkflow(
        "/tenants/1/projects/99/workflow-configs/BUG?team_default=true",
        3,
        definition,
        signal
      )
      expect(request.mock.calls[0]?.[0]).toEqual({
        method: "POST",
        url: "/tenants/1/projects/99/workflow-configs/BUG/previews?team_default=true",
      })
      expect(JSON.parse(request.mock.calls[0]?.[1]?.body as string)).toEqual({
        version: 3,
        remove: false,
        definition,
      })
      expect(request.mock.calls[0]?.[1]?.signal).toBe(signal)
      expect(request.mock.calls[0]?.[1]?.headers).toBeUndefined()
      await previewWorkflow(
        "/workflow-configs/TASK",
        4,
        definition,
        signal,
        true
      )
      expect(JSON.parse(request.mock.calls[1]?.[1]?.body as string)).toEqual({
        version: 4,
        remove: true,
        definition,
      })
    } finally {
      request.mockRestore()
    }
  })
  it("keeps an absent cell's condition draft across navigation and serialization", () => {
    const original: Workflow = {
      version: 4,
      carry_over_mode: "BACKLOG",
      states: [],
      transitions: [],
    }
    const selected = { from: "draft", to: "ready" }
    const condition = { kind: "REQUIRED_FIELDS", fields: ["estimate_minutes"] }
    const result = applyCellConditions(
      original,
      selected,
      JSON.stringify([condition])
    )
    expect(result.transitions).toEqual([
      {
        ...selected,
        enabled: false,
        manual_allowed: true,
        conditions: [condition],
      },
    ])
    expect(workflowDefinition(result)).toEqual({
      carry_over_mode: "BACKLOG",
      states: [],
      transitions: result.transitions,
    })
    expect(original.transitions).toEqual([])
    expect(applyCellConditions(result, null, "invalid")).toBe(result)
    expect(() => applyCellConditions(result, selected, "{}")).toThrow()
    expect(() => applyCellConditions(result, selected, "invalid")).toThrow()
    expect(result.transitions[0]?.conditions).toEqual([condition])
  })
  it("preserves exact work-item and half-open date filters", () => {
    expect(worklogPeriodQuery("77", "2026-10-20", "2026-10-21")).toBe(
      "work_item_id=77&from=2026-10-20&until=2026-10-21"
    )
    expect(worklogPeriodQuery("", "", "")).toBe("")
  })
  it("preserves evidence and manual restrictions when toggling one cell", () => {
    const workflow: Workflow = {
      carry_over_mode: "BACKLOG",
      version: 3,
      states: [],
      transitions: [
        {
          from: "ready",
          to: "done",
          enabled: true,
          manual_allowed: false,
          conditions: [{ kind: "EVIDENCE", rule: "INTERNAL_QA_PASSED" }],
        },
        {
          from: "draft",
          to: "ready",
          enabled: true,
          manual_allowed: true,
          conditions: [],
        },
      ],
    }
    const result = setCell(workflow, "ready", "done", false)
    expect(result.version).toBe(3)
    expect(result.carry_over_mode).toBe("BACKLOG")
    expect(result.transitions.find((t) => t.from === "ready")).toEqual({
      ...workflow.transitions[0]!,
      enabled: false,
    })
    expect(result.transitions.filter((t) => t.from === "draft")).toEqual([
      workflow.transitions[1]!,
    ])
    expect(workflow.transitions[0]!.enabled).toBe(true)
  })
  it("encodes each scope segment", () => {
    expect(basePath("1/other", "2/other")).toBe(
      "/tenants/1%2Fother/projects/2%2Fother"
    )
  })
  it("has complete readable three-language UI text", () => {
    for (const locale of Object.values(messages)) {
      expect(Object.keys(locale)).toEqual(Object.keys(messages["en-US"]))
      for (const value of Object.values(locale)) {
        expect(value.length).toBeGreaterThan(0)
        expect(value.includes("??")).toBe(false)
      }
    }
  })
  it("sends the zero version precondition when creating initial planning membership", async () => {
    const request = spyOn(iamApi, "deliveryResultRequest").mockResolvedValue({
      version: 1,
    })
    try {
      await write(
        "/tenants/1/projects/2/work-items/3/planning",
        "PUT",
        { iteration_id: "4" },
        "retry-key",
        0
      )
      expect(request.mock.calls[0]?.[1]?.headers).toEqual({
        "Idempotency-Key": "retry-key",
        "If-Match": '"0"',
      })
    } finally {
      request.mockRestore()
    }
  })
  it("has complete planning labels in all deployment locales", () => {
    for (const locale of Object.values(planningLabels)) {
      expect(Object.keys(locale)).toEqual(Object.keys(planningLabels["en-US"]!))
      for (const value of Object.values(locale)) {
        expect(value.length).toBeGreaterThan(0)
        expect(value.includes("??")).toBe(false)
      }
    }
  })
})
