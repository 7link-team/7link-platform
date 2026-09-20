import { describe, expect, it } from "bun:test"
import type { EffectiveMenu } from "./iam-api"
import {
  effectiveMenuPaths,
  flattenEffectiveMenus,
  workspacePath,
} from "./navigation"

const menus: EffectiveMenu[] = [
  {
    id: "iam",
    label: "IAM",
    permission_code: "",
    sort_order: 0,
    children: [
      {
        id: "users",
        label: "Users",
        path: "/users",
        permission_code: "user_view",
        sort_order: 10,
      },
    ],
  },
]

describe("effective navigation", () => {
  it("returns to the current tenant workspace without capturing startup state", () => {
    expect(workspacePath("38001505780891648")).toBe(
      "/tenants/38001505780891648"
    )
    expect(workspacePath("tenant / 测试")).toBe(
      "/tenants/tenant%20%2F%20%E6%B5%8B%E8%AF%95"
    )
    expect(workspacePath("")).toBe("/me")
  })

  it("flattens nested authorized menu paths", () => {
    expect(flattenEffectiveMenus(menus).map((menu) => menu.id)).toEqual([
      "iam",
      "users",
    ])
    expect([...effectiveMenuPaths(menus)]).toEqual(["/users"])
  })

  it("keeps an empty authorization result empty", () => {
    expect(flattenEffectiveMenus([])).toEqual([])
    expect(effectiveMenuPaths([]).size).toBe(0)
  })
})
