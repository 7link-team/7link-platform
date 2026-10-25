import { expect, it } from "bun:test"
import { defectMessages, defectPayload, type DefectReport } from "./defect-data"
it("keeps nullable facts and exact duplicate IDs, excludes receipt metadata", () => {
  const report: DefectReport = { work_item_id: "9007199254740993", version: 7, type: "BUG", severity: "CRITICAL", priority: "HIGH", reproducibility: "ALWAYS", found_version: "v1", fixed_version: null, introduced_stage: null, introduced_reason: null, module: null, details: { environment: "staging", steps: "Open", expected: "Loads", actual: "Fails", root_cause: "", fix_suggestion: "", resolution: "DUPLICATE", duplicate_of_work_item_id: "9007199254740995" } }
  const payload = defectPayload(report)
  expect(payload.details.duplicate_of_work_item_id).toBe("9007199254740995")
  expect(payload.fixed_version).toBeNull()
  expect(payload).not.toHaveProperty("work_item_id"); expect(payload).not.toHaveProperty("version"); expect(payload).not.toHaveProperty("type")
  expect(defectPayload({ ...report, details: null }).details.steps).toBe("")
  expect(defectPayload({ ...report, details: null }).details.resolution).toBeNull()
  for (const locale of ["zh-CN", "ms-MY"] as const) for (const key of Object.keys(defectMessages["en-US"]) as (keyof typeof defectMessages["en-US"])[]) {
    expect(defectMessages[locale][key]).toBeTruthy()
    expect(defectMessages[locale][key]).not.toContain("�")
  }
})
