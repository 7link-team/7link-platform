import { expect, test } from "bun:test"
import { effortDraft, effortMessages } from "./effort-data"
test("contextual effort retains exact task and legacy unknowns without inventing confirmation", () => {
  const draft = effortDraft("9007199254740993", { id: "1", principal_id: "11", actor_kind: "HUMAN", category: "TEST", work_date: "2026-10-20", minutes: 15, cost_micros: null, note: "Reviewed result", state: "APPROVED", version: 7, timezone: null })
  expect(draft.work_item_id).toBe("9007199254740993")
  expect(draft.minutes).toBe(15); expect(draft.note).toBe("Reviewed result"); expect(draft.work_date).toBe("2026-10-20")
  expect(draft).not.toHaveProperty("confirmed_at"); expect(draft).not.toHaveProperty("approved_at"); expect(draft.reason).toBe("")
  for (const locale of ["zh-CN", "ms-MY"] as const) for (const key of Object.keys(effortMessages["en-US"]) as (keyof typeof effortMessages["en-US"])[]) {
    expect(effortMessages[locale][key]).toBeTruthy(); expect(effortMessages[locale][key]).not.toContain("???")
  }
})
