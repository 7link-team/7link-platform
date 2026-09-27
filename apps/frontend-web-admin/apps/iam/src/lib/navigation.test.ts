import { describe, expect, it } from "bun:test"
import type { EffectiveMenu, Menu } from "./iam-api"
import {
  effectiveMenuPaths,
  flattenEffectiveMenus,
  workspacePath,
  tenantNavigation,
  adjacentMenuMove,
  droppedMenuMove,
  defaultWorkspacePath,
  platformNavigation,
  tenantSystemNavigation,
  systemAdministrationNavigation,
  systemAdministrationMenus,
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

describe("menu subtree movement", () => {
  const tree = [
    { id: "1", parent_id: "", sort_order: 0 },
    { id: "2", parent_id: "1", sort_order: 0 },
    { id: "3", parent_id: "2", sort_order: 0 },
    { id: "4", parent_id: "1", sort_order: 1 },
    { id: "5", parent_id: "", sort_order: 1 },
    { id: "6", parent_id: "5", sort_order: 0 },
  ] as Menu[]
  it("moves through adjacent branches and exits parents at their boundaries", () => {
    expect(adjacentMenuMove(tree, "4", "up")).toEqual({
      parent_id: "2",
      placement: { kind: "last" },
    })
    expect(adjacentMenuMove(tree, "2", "up")).toEqual({
      parent_id: "",
      placement: { kind: "before", relative_to: "1" },
    })
    expect(adjacentMenuMove(tree, "4", "down")).toEqual({
      parent_id: "",
      placement: { kind: "after", relative_to: "1" },
    })
    expect(adjacentMenuMove(tree, "1", "down")).toEqual({
      parent_id: "5",
      placement: { kind: "first" },
    })
    expect(adjacentMenuMove(tree, "1", "up")).toBeNull()
    expect(adjacentMenuMove(tree, "5", "down")).toBeNull()
  })
  it("accepts before/inside/after/root targets and rejects descendant/self moves", () => {
    expect(droppedMenuMove(tree, "2", "5", "inside")).toEqual({
      parent_id: "5",
      placement: { kind: "last" },
    })
    expect(droppedMenuMove(tree, "2", "6", "before")).toEqual({
      parent_id: "5",
      placement: { kind: "before", relative_to: "6" },
    })
    expect(droppedMenuMove(tree, "2", "", "inside")).toEqual({
      parent_id: "",
      placement: { kind: "last" },
    })
    expect(droppedMenuMove(tree, "2", "3", "after")).toBeNull()
    expect(droppedMenuMove(tree, "2", "2", "inside")).toBeNull()
    expect(droppedMenuMove(tree, "missing", "1", "inside")).toBeNull()
    expect(droppedMenuMove(tree, "2", "missing", "inside")).toBeNull()
  })
})

describe("effective navigation", () => {
  it("groups all six system destinations and omits unauthorized empty branches", () => {
    expect(systemAdministrationMenus).toHaveLength(3)
    expect(
      systemAdministrationMenus.flatMap((group) => group.children)
    ).toHaveLength(6)
    const groups = systemAdministrationNavigation(
      new Set(["system_account_view"])
    )
    expect(groups).toHaveLength(1)
    expect(groups[0]?.children.map((item) => item.path)).toEqual([
      "/system/accounts",
    ])
    expect(systemAdministrationNavigation(null)).toEqual([])
  })
  it("selects a platform home only from global platform permissions", () => {
    expect(defaultWorkspacePath("456", new Set(["tenant_view"]))).toBe(
      "/system/tenants"
    )
    expect(defaultWorkspacePath("456", new Set(["platform_administer"]))).toBe(
      "/system/audit-governance"
    )
    expect(defaultWorkspacePath("456", new Set(["tenant_administer"]))).toBe(
      "/tenants/456"
    )
    expect(defaultWorkspacePath("", new Set())).toBe("/me")
  })
  it("keeps platform administration gates independent", () => {
    expect(platformNavigation(true, false).map((item) => item.path)).toEqual([
      "/system/tenants",
    ])
    expect(platformNavigation(false, false)).toEqual([])
    expect(platformNavigation(false, true).map((item) => item.path)).toEqual([
      "/system/audit-governance",
      "/system/closure-requests",
    ])
  })
  it("preserves configured entry placement and supplies missing self-service entries once", () => {
    expect(
      tenantSystemNavigation(
        new Set(["/files", "/field-policies", "/access-requests"]),
        true,
        true
      ).map((item) => item.path)
    ).toEqual([""])
    expect(
      tenantSystemNavigation(new Set(), false, false, "123").map(
        (item) => item.path
      )
    ).toEqual(["", "/access-requests", "/entities/123/files"])
  })
  it("omits empty authorized groups while keeping accessible descendants", () => {
    const empty = {
      id: "empty",
      label: "Empty",
      permission_code: "",
      sort_order: 0,
      children: [],
    }
    expect(tenantNavigation([empty, ...menus]).map((menu) => menu.id)).toEqual([
      "iam",
    ])
  })
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
