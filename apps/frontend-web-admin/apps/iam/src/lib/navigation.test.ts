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
  tenantSystemNavigation,
  firstEffectiveMenuPath,
  entityWorkspaceView,
  tenantPerspectiveOffered,
  tenantStanding,
  entityNavigation,
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
  it("chooses the first admitted destination from the actual persisted tree", () => {
    expect(firstEffectiveMenuPath(menus)).toBe("/users")
    expect(firstEffectiveMenuPath([])).toBe("")
    expect(
      firstEffectiveMenuPath([
        {
          ...menus[0]!,
          children: [
            { ...menus[0]!.children![0]!, path: "/system/audit/events" },
          ],
        },
      ])
    ).toBe("/system/audit/events")
  })
  it("selects a platform home only from global platform permissions", () => {
    expect(defaultWorkspacePath("456", new Set(["tenant_view"]))).toBe(
      "/system/tenants"
    )
    expect(defaultWorkspacePath("456", new Set(["platform_administer"]))).toBe(
      "/system/audit-governance"
    )
    expect(defaultWorkspacePath("456", new Set(["tenant_administer"]))).toBe(
      "/admin/456"
    )
    expect(defaultWorkspacePath("", new Set())).toBe("/user")
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
      tenantSystemNavigation(new Set(), false, false).map((item) => item.path)
    ).toEqual(["", "/access-requests"])
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
      "/admin/38001505780891648"
    )
    expect(workspacePath("tenant / 测试")).toBe(
      "/admin/tenant%20%2F%20%E6%B5%8B%E8%AF%95"
    )
    expect(workspacePath("")).toBe("/user")
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

describe("entityWorkspaceView", () => {
  // The overview reads the entity record, which is tenant-wide entity
  // administration. An account that merely belongs to the entity used to be sent
  // there and got one refusal where its whole perspective should have been.
  it("sends an account without tenant-wide entity administration to the view it has", () => {
    expect(entityWorkspaceView([])).toBe("files")
    expect(entityWorkspaceView(["file_view", "dept_view"])).toBe("files")
    expect(entityWorkspaceView(new Set(["entity_view"]))).toBe("overview")
  })
})

describe("tenantPerspectiveOffered", () => {
  // Withheld only on evidence that this account's place is an entity.
  it("sends an account whose place is an entity there rather than into the tenant", () => {
    expect(tenantPerspectiveOffered(false, true)).toBe(false)
  })
  it("offers the tenant to anybody with standing in it", () => {
    expect(tenantPerspectiveOffered(true, true)).toBe(true)
    expect(tenantPerspectiveOffered(true, false)).toBe(true)
  })
  // An operator in the system area holds no tenant-scoped set there, and an
  // account with nothing at tenant level and no entity has only the tenant's
  // self-service shell. Neither is evidence for withholding.
  it("withholds nothing without that evidence", () => {
    expect(tenantPerspectiveOffered(null, true)).toBe(true)
    expect(tenantPerspectiveOffered(null, false)).toBe(true)
    expect(tenantPerspectiveOffered(false, false)).toBe(true)
  })
})

describe("tenantStanding", () => {
  it("reads any tenant-level code as standing and none as its absence", async () => {
    expect(await tenantStanding(Promise.resolve(["user_view"]))).toBe(true)
    expect(await tenantStanding(Promise.resolve([]))).toBe(false)
    expect(await tenantStanding(Promise.resolve(null))).toBe(false)
  })
  // A failed lookup used to be recorded as no standing, which took the tenant
  // away from anybody an entity also admits whenever the service hiccupped.
  it("reads a failed lookup as no answer, which withholds nothing", async () => {
    const standing = await tenantStanding(Promise.reject(new Error("offline")))
    expect(standing).toBeNull()
    expect(tenantPerspectiveOffered(standing, true)).toBe(true)
  })
})

describe("entityNavigation", () => {
  // What an entity account is offered follows the grants it holds in that
  // entity. The list used to be two booleans, so a permission whose page exists
  // -- the department register -- had no entry at all and the grant was
  // unreachable from the console.
  it("offers each view behind the permission that governs it", () => {
    expect(entityNavigation([]).map((item) => item.path)).toEqual([])
    expect(entityNavigation(["file_view"]).map((item) => item.path)).toEqual([
      "/files",
    ])
    expect(entityNavigation(["dept_view"]).map((item) => item.path)).toEqual([
      "/departments",
    ])
    expect(
      entityNavigation(["entity_view", "file_view", "dept_view"]).map(
        (item) => item.path
      )
    ).toEqual(["/overview", "/roles", "/files", "/departments", "/manage"])
  })
  // Every entry hangs off the entity being viewed, the department register
  // included: reaching it must not leave the perspective, which is what a link
  // to the tenant's own page did.
  it("keeps every entry inside the entity", () => {
    for (const item of entityNavigation([
      "entity_view",
      "file_view",
      "dept_view",
    ]))
      expect(item.path.startsWith("/")).toBe(true)
    expect(entityNavigation(["dept_view"])[0]?.path).toBe("/departments")
  })
})
