import { describe, expect, it } from "bun:test"
import {
  membershipState,
  membershipWindow,
  toDateTimeLocal,
  toISOString,
} from "./membership-presentation"

describe("directory membership presentation", () => {
  const now = Date.parse("2026-08-02T12:00:00Z")

  it("classifies scheduled membership windows", () => {
    expect(membershipState({}, now)).toBe("active")
    expect(membershipState({ starts_at: "2026-08-03T00:00:00Z" }, now)).toBe(
      "scheduled"
    )
    expect(membershipState({ ends_at: "2026-08-02T12:00:00Z" }, now)).toBe(
      "ended"
    )
    expect(
      membershipState(
        { starts_at: "2026-08-01T00:00:00Z", ends_at: "2026-08-03T00:00:00Z" },
        now
      )
    ).toBe("active")
  })

  it("converts timestamps for controls and API requests", () => {
    expect(toDateTimeLocal()).toBe("")
    expect(toDateTimeLocal("2026-08-02T12:34:00Z")).toMatch(
      /^2026-08-02T\d{2}:34$/
    )
    expect(toISOString("")).toBeUndefined()
    expect(toISOString("not-a-date")).toBeUndefined()
    expect(toISOString("2026-08-02T12:34")).toMatch(
      /^2026-08-02T\d{2}:34:00\.000Z$/
    )
  })

  it("formats open and bounded membership windows", () => {
    expect(membershipWindow({}, "zh-CN", "不限")).toBe("不限 - 不限")
    expect(membershipWindow({}, "en-US", "Unrestricted")).toBe(
      "Unrestricted - Unrestricted"
    )
    const bounded = membershipWindow(
      {
        starts_at: "2026-08-02T00:00:00Z",
        ends_at: "2026-08-03T00:00:00Z",
      },
      "zh-CN",
      "不限"
    )
    expect(bounded).not.toContain("Invalid Date")
    expect(bounded).not.toContain("不限")
  })
})
