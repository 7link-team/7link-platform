import { expect, it } from "bun:test"
import { authEntrance, entrancePath, publicAuthEntrance } from "./auth-entrance"

it("uses view prefixes while retaining tenant as the authentication scope", () => {
  expect(authEntrance("admin")).toBe("tenant")
  expect(publicAuthEntrance("admin")).toBe("tenant")
  expect(publicAuthEntrance("system")).toBeNull()
  expect(publicAuthEntrance("platform")).toBeNull()
  expect(entrancePath("system", "login")).toBe("/system/login")
  expect(entrancePath("platform", "workspaces")).toBe("/platform/workspaces")
  expect(entrancePath("tenant", "register")).toBe("/admin/register")
  expect(entrancePath("tenant", "recover")).toBe("/admin/recover")
  expect(entrancePath("user", "verify-email")).toBe("/user/verify-email")
})
