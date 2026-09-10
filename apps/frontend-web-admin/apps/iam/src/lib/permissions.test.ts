import { describe, expect, it } from "bun:test"
import { can } from "./permissions"

describe("can", () => {
  it("answers from the caller's own codes", () => {
    const permissions = new Set(["user_view", "user_update"])
    expect(can(permissions, "user_update")).toBe(true)
    expect(can(permissions, "user_create")).toBe(false)
    expect(can(permissions, "user_delete")).toBe(false)
  })

  it("treats an unanswered read as permission unknown, not permission denied", () => {
    // A failed or absent /iam/me/permissions must not empty a console of every
    // action: the server is still enforcing, and a transient outage that hides
    // every button looks like a broken product rather than a refused request.
    expect(can(null, "user_create")).toBe(true)
  })

  it("denies everything for a caller who holds nothing", () => {
    expect(can(new Set<string>(), "user_view")).toBe(false)
  })
})
