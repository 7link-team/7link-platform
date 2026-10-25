import { describe, expect, it } from "bun:test"
import { safeNotificationLink } from "./notification-api"
import en from "../messages/en-US.json"
import zh from "../messages/zh-CN.json"
import ms from "../messages/ms-MY.json"

describe("notification navigation", () => {
  it("keeps in-app destinations and rejects external or malformed destinations", () => {
    expect(safeNotificationLink("/admin/7/machines/91")).toBe("/admin/7/machines/91")
    for (const path of ["", "https://example.test", "//example.test", "javascript:alert(1)", "/\\example.test", "/path\r\nLocation: elsewhere"]) {
      expect(safeNotificationLink(path)).toBe("/user/notifications")
    }
  })
})

describe("notification locale integrity", () => {
  it("has readable matching notification messages in all three locales", () => {
    const keys = Object.keys(en).filter(key => key.startsWith("notification"))
    for (const dictionary of [en, zh, ms]) {
      for (const key of keys) {
        const value = dictionary[key as keyof typeof dictionary]
        expect(typeof value).toBe("string")
        expect(value.length).toBeGreaterThan(0)
        expect(value).not.toContain("??")
      }
    }
  })
})
