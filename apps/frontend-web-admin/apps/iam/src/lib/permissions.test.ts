import { describe, expect, it } from "bun:test"
import { can } from "./permissions"

describe("can", () => {
  it("answers from the caller's own codes", () => {
    const permissions = new Set(["user_view", "user_update"])
    expect(can(permissions, "user_update")).toBe(true)
    expect(can(permissions, "user_create")).toBe(false)
    expect(can(permissions, "user_delete")).toBe(false)
  })

  it("does not offer protected actions when permission reads fail", () => {
    expect(can(null, "user_create")).toBe(false)
  })

  it("denies everything for a caller who holds nothing", () => {
    expect(can(new Set<string>(), "user_view")).toBe(false)
  })
})
