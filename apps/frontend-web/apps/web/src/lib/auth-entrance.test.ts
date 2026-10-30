import { expect, it } from "bun:test"
import {
  authEntrance,
  entrancePath,
  loginReturnPath,
  logoutDestination,
  publicAuthEntrance,
  workspaceInvitationToken,
} from "./auth-entrance"

it("extracts invitation credentials without following supplied links", () => {
  const token = `inv1_17.${"x".repeat(43)}`
  expect(workspaceInvitationToken(` ${token} `)).toBe(token)
  expect(workspaceInvitationToken(`https://another-domain.example/admin/accept-invitation?token=${token}`)).toBe(token)
  expect(workspaceInvitationToken(`/accept-invitation?token=${token}`)).toBe(token)
  for (const bad of ["", "javascript:alert(1)", "https://example.test/other?token=" + token, "inv1_0." + "x".repeat(43), "inv1_17.short", token + "#fragment"]) expect(workspaceInvitationToken(bad)).toBeNull()
})

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

it("continues sign-in only to pages inside the same entrance", () => {
  expect(loginReturnPath("tenant", "/admin/7/users?page=2")).toBe(
    "/admin/7/users?page=2"
  )
  expect(loginReturnPath("system", "/system/tenants")).toBe("/system/tenants")
  expect(loginReturnPath("user", "/user/security")).toBe("/user/security")
  expect(loginReturnPath("tenant", "/system/tenants")).toBe("/admin/workspaces")
  expect(loginReturnPath("platform", "/platformer/1")).toBe(
    "/platform/workspaces"
  )
  expect(loginReturnPath("system", "/system")).toBe("/system/workspaces")
  expect(loginReturnPath("user", "https://evil.test/user/x")).toBe(
    "/user/workspaces"
  )
  expect(loginReturnPath(null, "/user/files")).toBe("/user/files")
  expect(loginReturnPath("tenant", undefined)).toBe("/admin/workspaces")
})

it("returns to the current entrance login unless the service names another destination", () => {
  const original = globalThis.window
  const at = (pathname: string) =>
    Object.assign(globalThis, {
      window: { location: { pathname, origin: "http://console.test" } },
    })
  try {
    at("/admin/7/users")
    expect(logoutDestination()).toBe("/admin/login")
    at("/system/tenants")
    expect(logoutDestination()).toBe("/system/login")
    at("/platform/3")
    expect(logoutDestination("/login")).toBe("/platform/login")
    expect(logoutDestination("http://console.test/admin/login")).toBe(
      "/platform/login"
    )
    at("/unknown")
    expect(logoutDestination()).toBe("/user/login")
    expect(logoutDestination("https://idp.test/logout")).toBe(
      "https://idp.test/logout"
    )
    expect(logoutDestination("/goodbye")).toBe("/goodbye")
    expect(logoutDestination("http://console.test/admin/login/x")).toBe(
      "http://console.test/admin/login/x"
    )
  } finally {
    Object.assign(globalThis, { window: original })
  }
})
