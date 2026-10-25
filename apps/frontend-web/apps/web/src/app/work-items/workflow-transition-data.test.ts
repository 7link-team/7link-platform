import { expect, test } from "bun:test"
import { transitionFields, transitionMessages, transitionTargets } from "./workflow-transition-data"
import type { Workflow } from "../work-management/api"

test("workflow choices use exact configured source and exclude archived, disabled and system cells", () => {
  const states = ["ready", "scheduled", "archived", "system", "disabled"].map(key => ({ key, display_name: key, group: "TODO", maps_to: "READY", archived: key === "archived" }))
  const workflow: Workflow = { version: 1, states, transitions: states.map(state => ({ from: "ready", to: state.key, enabled: state.key !== "disabled", manual_allowed: state.key !== "system", conditions: [] })) }
  expect(transitionTargets(workflow, "ready").map(state => state.key)).toEqual(["scheduled"])
  expect(transitionTargets(workflow, "scheduled")).toEqual([])
})

test("transition completion sends bounded numeric estimates and exact result acceptance references", () => {
  expect(transitionFields("READY", "35", null, "")).toEqual({ estimate_minutes: 35 })
  expect(transitionFields("ACCEPTED", "", "9007199254740993", "Reviewed exact evidence")).toEqual({ result_id: "9007199254740993", reason: "Reviewed exact evidence" })
  expect(transitionFields("CLAIMED", "35", null, "")).toEqual({})
  for (const bad of ["-1", "0", "14", "1.2", "NaN", "4801"]) expect(() => transitionFields("READY", bad, null, "")).toThrow()
  for (const labels of Object.values(transitionMessages)) {
    expect(Object.keys(labels)).toEqual(Object.keys(transitionMessages["en-US"]))
    for (const text of Object.values(labels)) { expect(text.length).toBeGreaterThan(0); expect(text).not.toContain("??") }
  }
})
