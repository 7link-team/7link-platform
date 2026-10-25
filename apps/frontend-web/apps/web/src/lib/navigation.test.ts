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
  workspaceArea,
  workspaceAreaMenus,
  workspaceScopePath,
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
  it("keeps work items as the collaboration landing when requirements are also readable", () => {
    const links = entityNavigation(
      ["requirement_read", "work_item_read"],
      "project"
    ).filter((item) => workspaceArea(item.path) === "collaboration")
    expect(links.map((item) => item.path)).toEqual([
      "/work-items",
      "/requirements",
      "/review-sheet",
      "/heavy",
      "/review",
      "/defects",
      "/video",
      "/novel",
      "/team-tasks",
    ])
  })

  it("offers a requirement-only member a usable instance entry without work or file grants", () => {
    expect(entityWorkspaceView(["requirement_read"], "project")).toBe(
      "requirements"
    )
    expect(entityNavigation(["requirement_read"], "project")).toEqual([
      { path: "/requirements", label: "requirements" },
      { path: "/review-sheet", label: "reviewSheet" },
    ])
    expect(entityNavigation(["requirement_read"], "company")).toEqual([])
    expect(workspaceArea("/admin/7/entities/11/requirements")).toBe(
      "collaboration"
    )
    expect(
      workspaceScopePath(
        "/admin/7/entities/11/requirements",
        "8",
        "12",
        ["requirement_read"],
        "project"
      )
    ).toBe("/admin/8/entities/12/requirements")
    expect(
      workspaceScopePath(
        "/admin/7/entities/11/requirements",
        "7",
        "12",
        [],
        "project"
      )
    ).toBeNull()
    expect(
      workspaceScopePath(
        "/admin/7/entities/11/requirements",
        "7",
        "",
        ["requirement_read"],
        "project"
      )
    ).toBeNull()
  })
  it("preserves resource pages across scopes and never transfers an object location", () => {
    for (const resource of ["files", "departments"]) {
      expect(
        workspaceScopePath(`/admin/7/entities/11/${resource}`, "7", "12")
      ).toBe(`/admin/7/entities/12/${resource}`)
      expect(
        workspaceScopePath(`/admin/7/entities/11/${resource}`, "8", "")
      ).toBe(`/admin/8/${resource}`)
      expect(workspaceScopePath(`/admin/7/${resource}`, "7", "12")).toBe(
        `/admin/7/entities/12/${resource}`
      )
    }
    expect(
      workspaceScopePath(
        "/admin/7/entities/11/project",
        "7",
        "12",
        ["project_read"],
        "project"
      )
    ).toBe("/admin/7/entities/12/project")
    expect(
      workspaceScopePath(
        "/admin/7/projects",
        "7",
        "12",
        ["file_view"],
        "project"
      )
    ).toBe("/admin/7/entities/12/files")
    expect(workspaceScopePath("/admin/7/users", "7", "12")).toBeNull()
    expect(workspaceScopePath("/admin/7/users", "8", "")).toBe("/admin/8/users")
    for (const resource of ["manage", "resources"]) {
      expect(
        workspaceScopePath(`/admin/7/entities/11/${resource}`, "8", "")
      ).toBe("/admin/8/entities")
      expect(
        workspaceScopePath(`/admin/7/entities/11/${resource}`, "7", "12")
      ).toBe(`/admin/7/entities/12/${resource}`)
    }
  })
  it("groups existing menus without losing custom descendants or adding business pages", () => {
    const tree = [
      {
        ...menus[0]!,
        children: [
          ...menus[0]!.children!,
          {
            ...menus[0]!.children![0]!,
            id: "custom",
            path: "/custom-resource",
          },
        ],
      },
    ]
    expect([...effectiveMenuPaths(workspaceAreaMenus(tree, "team"))]).toEqual([
      "/users",
    ])
    expect([
      ...effectiveMenuPaths(workspaceAreaMenus(tree, "governance")),
    ]).toEqual(["/custom-resource"])
    expect(workspaceAreaMenus(tree, "conversation")).toEqual([])
    expect(workspaceArea("/admin/7/entities/11/files")).toBe("collaboration")
    expect(workspaceArea("/admin/7/projects")).toBe("workbench")
    expect(workspaceArea("/admin/7/entities/11/roles")).toBe("governance")
  })
  it("has one project business overview and keeps IAM controls separate", () => {
    expect(entityWorkspaceView(["work_item_read"], "project")).toBe(
      "work-items"
    )
    expect(entityNavigation(["work_item_read"], "project")).toEqual([
      { path: "/plan-review", label: "planReviewTitle" },
      { path: "/work-items", label: "workItems" },
      { path: "/my-work", label: "personalWorkTitle" },
      { path: "/heavy", label: "heavyTitle" },
      { path: "/review", label: "projectSurfaceReview" },
      { path: "/defects", label: "projectSurfaceDefects" },
      { path: "/video", label: "studioVIDEO" },
      { path: "/novel", label: "studioNOVEL" },
      { path: "/team-tasks", label: "studioTEAM_TASKS" },
    ])
    expect(
      workspaceScopePath(
        "/admin/7/entities/11/work-items",
        "7",
        "12",
        ["work_item_read"],
        "project"
      )
    ).toBe("/admin/7/entities/12/work-items")
    expect(
      workspaceScopePath(
        "/admin/7/entities/11/work-items",
        "7",
        "12",
        ["work_item_read"],
        "company"
      )
    ).toBeNull()
    expect(workspaceArea("/work-items")).toBe("collaboration")
    const links = entityNavigation(
      ["project_read", "entity_view", "file_view"],
      "project"
    )
    expect(
      links.filter((item) => workspaceArea(item.path) === "workbench")
    ).toEqual([{ path: "/project", label: "instanceOverview" }])
    expect(links.map((item) => item.path)).toContain("/roles")
    expect(entityWorkspaceView(["project_read"], "project")).toBe("project")
    expect(entityWorkspaceView(["dept_view"], "project")).toBe("departments")
  })
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
    ).toEqual(["", "/conversations", "/governance/inbox", "/governance/agent-operations"])
    expect(
      tenantSystemNavigation(new Set(), false, false).map((item) => item.path)
    ).toEqual(["", "/conversations", "/governance/inbox", "/governance/agent-operations", "/access-requests"])
    expect(tenantSystemNavigation(new Set(["/conversations"]), false, false).filter(item => item.path === "/conversations")).toEqual([])
    expect(workspaceScopePath("/admin/7/conversations", "8", "", [], "")).toBe("/admin/8/conversations")
  })
  it("offers the project list only with read access and avoids duplicate entries", () => {
    const paths = (configured: Set<string>, allowed: boolean) =>
      tenantSystemNavigation(configured, false, false, allowed).map(
        (item) => item.path
      )
    expect(paths(new Set(), false)).not.toContain("/projects")
    expect(
      paths(new Set(), true).filter((path) => path === "/projects")
    ).toHaveLength(1)
    expect(paths(new Set(["/projects"]), true)).not.toContain("/projects")
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
    expect(workspacePath("38001505780891648")).toBe("/admin/38001505780891648")
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
  it("offers project details only with the endpoint's read permission", () => {
    expect(entityNavigation(["project_read"], "project")).toEqual([
      { path: "/project", label: "instanceOverview" },
      { path: "/test-cases", label: "projectSurfaceTests" },
      { path: "/pipelines", label: "projectSurfacePipelines" },
      { path: "/exceptions", label: "projectSurfaceExceptions" },
    ])
    expect(entityNavigation(["project_manage"], "project")).toContainEqual({
      path: "/project-settings", label: "projectSurfaceSettings",
    })
    for (const type of [undefined, "client", "organization"])
      expect(
        entityNavigation(["project_read"], type).some(
          (item) => item.path === "/project"
        )
      ).toBe(false)
    expect(
      entityNavigation(["project_manage"], "project").some(
        (item) => item.path === "/project"
      )
    ).toBe(false)
  })
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


describe("planning and governance navigation", () => {
  it("assigns each Lane E page to one top area including detail routes", () => {
    for (const path of ["/goals", "/templates", "/templates/42/workflow"])
      expect(workspaceArea(path)).toBe("team")
    for (const path of ["/plans/me", "/plans", "/plans/9007199254740993", "/reports"])
      expect(workspaceArea(path)).toBe("workbench")
    for (const path of ["/governance/inbox", "/governance/policies", "/integrations"])
      expect(workspaceArea(path)).toBe("governance")
  })
  it("offers member inbox and permission gated pages without duplicates", () => {
    const permissions = ["goal_read", "plan_submit", "report_read", "template_read", "policy_read", "integration_manage"]
    const paths = tenantSystemNavigation(new Set(), false, false, false, false, false, false, permissions).map(item => item.path)
    for (const path of ["/goals", "/plans/me", "/reports", "/templates", "/governance/inbox", "/governance/policies", "/integrations"])
      expect(paths.filter(value => value === path)).toHaveLength(1)
    expect(tenantSystemNavigation(new Set(["/templates"]), false, false, false, false, false, false, permissions).some(item => item.path === "/templates")).toBe(false)
    expect(tenantSystemNavigation(new Set(), false, false).some(item => item.path === "/goals")).toBe(false)
    expect(tenantSystemNavigation(new Set(), false, false).some(item => item.path === "/governance/inbox")).toBe(true)
  })
  it("keeps project planning grants inside project scope", () => {
    expect(entityNavigation(["goal_read", "plan_submit", "report_read"], "project").map(item => item.path)).toEqual(["/goals", "/plans", "/reports"])
    expect(entityNavigation(["goal_read"], "client")).toEqual([])
    expect(workspaceScopePath("/admin/1/entities/2/plans", "1", "3", ["plan_submit"], "project")).toBe("/admin/1/entities/3/plans")
    expect(workspaceScopePath("/admin/1/entities/2/plans", "1", "", ["plan_submit"])).toBe("/admin/1/plans/me")
    expect(workspaceScopePath("/admin/1/plans/9007199254740993", "1", "3", ["plan_submit"], "project")).toBe("/admin/1/entities/3/plans")
    expect(workspaceScopePath("/admin/1/entities/2/plans/9007199254740993", "1", "", ["plan_confirm"])).toBe("/admin/1/plans/me")
    expect(workspaceScopePath("/admin/1/plans/9007199254740993", "1", "3", [], "project")).toBeNull()
    expect(workspaceScopePath("/admin/1/plans/9007199254740993", "1", "3", ["plan_submit"], "client")).toBeNull()
    expect(workspaceScopePath("/admin/1/goals", "1", "3", [], "project")).toBeNull()
  })
})

it("keeps operation catalogue and pending memory rules in the single governance menu set", () => {
 const path = "/governance/agent-operations"
 expect(workspaceArea(path)).toBe("governance")
 expect(workspaceArea("/governance/memory-review")).toBe("governance")
 expect(tenantSystemNavigation(new Set(), false, false).filter(item => item.path === path)).toHaveLength(1)
 expect(tenantSystemNavigation(new Set([path]), false, false).some(item => item.path === path)).toBe(false)
 const without = tenantSystemNavigation(new Set(), false, false)
 expect(without.some(item => item.path === "/governance/memory-review")).toBe(false)
 const withApproval = tenantSystemNavigation(new Set(), false, false, false, false, false, false, ["memory_approve"])
 expect(withApproval.filter(item => item.path === "/governance/memory-review")).toHaveLength(1)
 expect(entityNavigation(["memory_approve"], "project").map(item => item.path)).toEqual(["/governance/memory-review"])
 expect(workspaceScopePath("/admin/1/governance/memory-review", "1", "3", ["memory_approve"], "project")).toBe("/admin/1/entities/3/governance/memory-review")
 expect(workspaceScopePath("/admin/1/entities/3/governance/memory-review", "1", "", ["memory_approve"])).toBe("/admin/1/governance/memory-review")
 expect(workspaceScopePath("/admin/1/governance/memory-review", "1", "3", [], "project")).toBeNull()
})

it("keeps worklogs and workflow configuration in their canonical project areas",()=>{
 expect(workspaceArea("/worklogs")).toBe("collaboration")
 expect(workspaceArea("/workflow-config")).toBe("governance")
 expect(entityNavigation(["workflow_manage"],"project")).toContainEqual({path:"/workflow-config",label:"workflowConfigTitle"})
 expect(workspaceScopePath("/admin/1/entities/2/worklogs","1","3",["work_item_read"],"project")).toBe("/admin/1/entities/3/worklogs")
 expect(workspaceScopePath("/admin/1/entities/2/workflow-config","1","3",[],"project")).toBeNull()
 expect(workspaceScopePath("/admin/1/entities/2/worklogs","1","",["work_item_read"])).toBeNull()
})


describe("Lane L canonical page navigation", () => {
  it("keeps each dedicated page in one top-menu area and drops execution IDs on scope changes", () => {
    for (const path of ["/runners", "/tool-catalog"]) expect(workspaceArea(path)).toBe("team")
    expect(workspaceArea("/gateway")).toBe("governance")
    expect(workspaceArea("/metrics")).toBe("workbench")
    expect(workspaceArea("/knowledge/sources/91")).toBe("conversation")
    expect(workspaceScopePath("/admin/1/entities/2/workspace/9007199254740993", "3", "4", ["execution_read"], "project")).toBe("/admin/3/entities/4/executions")
    expect(workspaceScopePath("/admin/1/knowledge/sources/91", "3", "4", ["knowledge_read"], "project")).toBe("/admin/3/entities/4/knowledge/sources")
    expect(workspaceScopePath("/admin/1/gateway", "3", "4", ["gateway_manage"], "project")).toBeNull()
  })
})

it("keeps owner performance configuration in one tenant workbench area",()=>{
 const links=(permissions:string[])=>tenantSystemNavigation(new Set(),false,false,false,false,false,false,permissions).filter(item=>item.path==="/performance")
 expect(links(["performance_manage"])).toEqual([])
 expect(links(["tenant_administer"])).toEqual([])
 expect(links(["tenant_administer","performance_manage"])).toHaveLength(1)
 expect(workspaceArea("/admin/1/performance")).toBe("workbench")
 expect(entityNavigation(["tenant_administer","performance_manage"],"project").some(item=>item.path==="/performance")).toBe(false)
})
