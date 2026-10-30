import type { EffectiveMenu, Menu } from "./iam-api"
import { defaultLocale } from "@workspace/ui/i18n/config"
import { projectSurfaces, projectSurface } from "./project-surfaces"

export type SystemNavigationItem = { path: string; label: string }

export const workspaceAreas = [
  "workbench",
  "conversation",
  "collaboration",
  "team",
  "governance",
] as const
export type WorkspaceArea = (typeof workspaceAreas)[number]

// These are existing IAM permissions, not a second membership catalogue.
export const workspaceEntityPermissions = [
  "file_view",
  "project_read",
  "work_item_read",
  "requirement_read",
  "goal_read",
  "plan_submit",
  "plan_confirm",
  "report_read",
  "channel_read",
  "memory_approve",
	"execution_read",
  "dept_view",
  "entity_view",
] as const

export function workspaceResource(pathname: string): string {
  return pathname
    .replace(/^\/admin\/[^/]+(?:\/entities\/[^/]+(?=\/))?/, "")
    .replace(/\/$/, "")
}

export function workspaceArea(path: string): WorkspaceArea {
  const resource = path.startsWith("/admin/")
    ? workspaceResource(path)
    : path.replace(/\/$/, "")
  if (["/metrics"].includes(resource)) return "workbench"
  if (resource === "/performance") return "workbench"
  if (["/runners", "/tool-catalog"].includes(resource)) return "team"
  if (resource === "/gateway") return "governance"
  if (resource.startsWith("/workspace/")) return "collaboration"
  if (resource === "/my-work") return "workbench"
  if (resource === "/worklogs") return "collaboration"
  if (resource === "/intake-channels") return "governance"
  if (resource === "/review-sheet") return "collaboration"
  if (resource === "/workflow-config") return "governance"
  if (projectSurface(resource.slice(1))) return "collaboration"
  if (resource === "/goals" || resource === "/templates" || resource.startsWith("/templates/")) return "team"
  if (resource.startsWith("/plans/") || resource === "/plans" || resource === "/reports") return "workbench"
  if (resource === "/machines" || resource.startsWith("/machines/")) return "team"
  if (resource === "/agents") return "collaboration"
  if (resource === "/executions" || resource.startsWith("/executions/")) return "collaboration"
  if (["", "/project", "/projects", "/overview"].includes(resource))
    return "workbench"
  if (["/conversations", "/knowledge"].includes(resource) || resource.startsWith("/knowledge/")) return "conversation"
  if (["/files", "/work-items", "/requirements"].includes(resource))
    return "collaboration"
  if (
    [
      "/users",
      "/invitations",
      "/service-accounts",
      "/departments",
      "/groups",
    ].includes(resource)
  )
    return "team"
  return "governance"
}

export function workspaceAreaMenus(
  menus: EffectiveMenu[],
  area: WorkspaceArea
): EffectiveMenu[] {
  return menus.flatMap((menu) => {
    const children = workspaceAreaMenus(menu.children ?? [], area)
    const own = Boolean(menu.path && workspaceArea(menu.path) === area)
    return own || children.length
      ? [{ ...menu, path: own ? menu.path : "", children }]
      : []
  })
}

// A scope change never carries an object ID, file location or preview into the
// next scope. Unsupported tenant-only resources stay in their current scope.
export function workspaceScopePath(
  pathname: string,
  tenantID: string,
  entityID: string,
  permissions: Iterable<string> = [],
  entityType?: string
): string | null {
  const resource = workspaceResource(pathname)
  const base = `/admin/${encodeURIComponent(tenantID)}`
  const target = entityID
    ? `${base}/entities/${encodeURIComponent(entityID)}`
    : base
  if (resource === "/worklogs" || resource === "/workflow-config")
    return entityID && entityType === "project" && new Set(permissions).has(resource === "/worklogs" ? "work_item_read" : "workflow_manage") ? `${target}${resource}` : null
  if (resource === "/my-work")
    return (!entityID || entityType === "project") && new Set(permissions).has("work_item_read") ? `${target}/my-work` : null
  const surface = projectSurface(resource.slice(1))
  if (surface)
    return entityID && entityType === "project" && (new Set(permissions).has(surface.permission) || ("alternatePermission" in surface && new Set(permissions).has(surface.alternatePermission)) || ("triagePermission" in surface && new Set(permissions).has(surface.triagePermission)) || ("submissionPermission" in surface && new Set(permissions).has(surface.submissionPermission))) ? `${target}${resource}` : null
  const planningResource = /^\/plans\/(?:me|[1-9][0-9]*)$/.test(resource) ? "/plans" : resource
  if (["/goals", "/plans", "/reports"].includes(planningResource)) {
    const permission = planningResource === "/goals" ? "goal_read" : planningResource === "/plans" ? "plan_submit" : "report_read"
    return (!entityID || entityType === "project") && (new Set(permissions).has(permission) || (planningResource === "/plans" && new Set(permissions).has("plan_confirm"))) ? `${target}${planningResource === "/plans" && !entityID ? "/plans/me" : planningResource}` : null
  }
  if (resource.startsWith("/workspace/"))
    return entityID && entityType === "project" && new Set(permissions).has("execution_read") ? `${target}/executions` : null
  if (["/gateway", "/runners", "/tool-catalog", "/metrics"].includes(resource)) {
    const permission = resource === "/gateway" ? "gateway_manage" : resource === "/runners" ? "machine_read" : resource === "/tool-catalog" ? "tool_catalog_manage" : "work_item_read"
    return !entityID && new Set(permissions).has(permission) ? `${base}${resource}` : null
  }
  if (resource === "/executions" || resource.startsWith("/executions/"))
    return entityID && entityType === "project" && new Set(permissions).has("execution_read") ? `${target}/executions` : null
  if (resource === "/machines" || resource.startsWith("/machines/"))
    return !entityID && new Set(permissions).has("machine_read") ? `${base}/machines` : null
  if (resource === "/agents")
    return !entityID && new Set(permissions).has("agent_read") ? `${base}/agents` : null
  if (resource === "/review-sheet" || resource === "/intake-channels")
    return entityID && entityType === "project" && new Set(permissions).has(resource === "/review-sheet" ? "requirement_read" : "intake_read") ? `${target}${resource}` : null
  if (resource === "/knowledge/settings")
    return !entityID && new Set(permissions).has("knowledge_manage") ? `${base}/knowledge/settings` : null
  if (resource.startsWith("/knowledge/sources"))
    return (!entityID || entityType === "project") && new Set(permissions).has("knowledge_read") ? `${target}/knowledge/sources` : null
  if (resource === "/knowledge/debug")
    return (!entityID || entityType === "project") && new Set(permissions).has("knowledge_manage") ? `${target}/knowledge/debug` : null
  if (resource === "/knowledge")
    return (!entityID || entityType === "project") && new Set(permissions).has("knowledge_read") ? `${target}/knowledge` : null
  if (resource === "/conversations")
    return !entityID ? `${base}/conversations` : entityType === "project" && new Set(permissions).has("channel_read") ? `${target}/conversations` : null
  if (resource === "/requirements")
    return entityID &&
      entityType === "project" &&
      new Set(permissions).has("requirement_read")
      ? `${target}/requirements`
      : null
  if (resource === "/governance/memory-review")
    return (!entityID || entityType === "project") && new Set(permissions).has("memory_approve") ? `${target}/governance/memory-review` : null
  if (resource === "/work-items")
    return entityID &&
      entityType === "project" &&
      new Set(permissions).has("work_item_read")
      ? `${target}/work-items`
      : null
  if (["/files", "/departments"].includes(resource))
    return `${target}${resource}`
  if (["", "/project", "/projects", "/overview"].includes(resource))
    return entityID
      ? `${target}/${entityWorkspaceView(permissions, entityType)}`
      : base
  if (!entityID)
    return ["/manage", "/resources"].includes(resource)
      ? `${base}/entities`
      : `${base}${resource}`
  if (
    /^\/admin\/[^/]+\/entities\/[^/]+\//.test(pathname) &&
    ["/roles", "/resources", "/manage"].includes(resource)
  )
    return `${target}${resource}`
  return null
}

export function firstEffectiveMenuPath(menus: EffectiveMenu[]): string {
  for (const menu of menus) {
    const child = firstEffectiveMenuPath(menu.children ?? [])
    if (child) return child
    if (menu.path) return menu.path
  }
  return ""
}

export const personalNavigation: SystemNavigationItem[] = [
  { path: "/user/notifications", label: "notificationCenter" },
  { path: "/user/profile", label: "profile" },
  { path: "/user/security", label: "security" },
  { path: "/user/mfa", label: "twoFactor" },
  { path: "/user/tokens", label: "personalTokens" },
  { path: "/user/files", label: "myFiles" },
  { path: "/user/memberships", label: "myMemberships" },
  { path: "/user/identities", label: "linkedIdentities" },
  { path: "/user/closure", label: "accountClosure" },
  { path: "/user/applications", label: "authorizedApplications" },
]

// entityNavigation offers each view behind the permission that governs its page,
// taken from the caller's permissions in this entity. Nothing here is a fixed
// list of what an entity member gets: a grant the account holds and a view it is
// offered are the same decision, so a permission with a page has an entry and a
// permission without one is a gap in the console rather than a policy.
export function entityNavigation(
  permissions: Iterable<string>,
  entityType?: string
): SystemNavigationItem[] {
  const held = new Set(permissions)
  const entityAccess = held.has("entity_view")
  return [
    ...(entityType === "project" ? [
      ...(held.has("goal_read") ? [{ path: "/goals", label: "planningGoals" }] : []),
      ...((held.has("plan_submit") || held.has("plan_confirm")) ? [{ path: "/plans", label: "planningPlans" }] : []),
      ...(held.has("report_read") ? [{ path: "/reports", label: "planningReports" }] : []),
    ] : []),
    ...(entityType === "project" && held.has("workflow_manage") ? [{path:"/workflow-config",label:"workflowConfigTitle"}] : []),
    ...(entityType === "project" && held.has("work_item_read") ? [{path:"/plan-review",label:"planReviewTitle"}] : []),
    ...(entityType === "project" && held.has("audit_read") ? [{path:"/escapes",label:"qualityEscapesTitle"}] : []),
    ...(entityType === "project" && held.has("execution_read")
      ? [{path: "/executions", label: "executions"}]
      : []),
    ...(entityType === "project" && held.has("intake_read") ? [{path: "/intake-channels", label: "intakeChannels"}] : []),
    ...(entityType === "project" && held.has("knowledge_read")
      ? [{ path: "/knowledge", label: "knowledge" }]
      : []),
    ...(entityType === "project" && held.has("memory_approve")
      ? [{ path: "/governance/memory-review", label: "memoryReviewTitle" }]
      : []),
    ...(entityType === "project" && held.has("channel_read")
      ? [{ path: "/conversations", label: "conversations" }]
      : []),
    ...(entityType === "project" && held.has("work_item_read")
      ? [{ path: "/work-items", label: "workItems" }]
      : []),
    ...(entityType === "project" && held.has("requirement_read")
      ? [{ path: "/requirements", label: "requirements" }]
      : []),
    ...(entityType === "project" && held.has("requirement_read") ? [{path: "/review-sheet", label: "reviewSheet"}] : []),
    ...(entityType === "project" && held.has("project_read")
      ? [{ path: "/project", label: "instanceOverview" }]
      : []),
    ...(entityAccess
      ? [
          ...(entityType === "project" && held.has("project_read")
            ? []
            : [{ path: "/overview", label: "entityOverview" }]),
          { path: "/roles", label: "entityAccessControl" },
        ]
      : []),
    ...(held.has("file_view") ? [{ path: "/files", label: "files" }] : []),
    ...(held.has("dept_view")
      ? [{ path: "/departments", label: "departmentManagement" }]
      : []),
    ...(entityAccess ? [{ path: "/manage", label: "entitySettings" }] : []),
    ...(entityType === "project" && held.has("work_item_read") ? [{ path: "/my-work", label: "personalWorkTitle" }] : []),
    ...(entityType === "project" ? projectSurfaces.filter((entry) => (held.has(entry.permission) || ("alternatePermission" in entry && held.has(entry.alternatePermission)) || ("triagePermission" in entry && held.has(entry.triagePermission)) || ("submissionPermission" in entry && held.has(entry.submissionPermission)))).map((entry) => ({ path: `/${entry.path}`, label: entry.label })) : []),
  ]
}

// tenantPerspectiveOffered says whether a tenant in the switcher may be entered.
// The switcher lists memberships, so entering one goes by the same standing
// everybody else goes by: an operator acting as an operator uses the system area,
// which is its own navigation, not this list.
//
// It is withheld only on evidence that this account's place in the tenant is an
// entity -- nothing at tenant level, and an entity that admits it. An answer that
// has not arrived withholds nothing. Reading an empty permission set as "no
// standing" is what took every tenant away from the super administrator and the
// system administrator: in the system area the set in hand is tenant-scoped and
// therefore empty, while their standing in that tenant is 146 and 10 codes.
export function tenantPerspectiveOffered(
  tenantStanding: boolean | null,
  anEntityAdmitsCaller: boolean
): boolean {
  return !(tenantStanding === false && anEntityAdmitsCaller)
}

// tenantStanding reads one tenant-level permission lookup as standing in that
// tenant: any code is standing and none is its absence. A lookup that failed is
// no answer at all, which tenantPerspectiveOffered reads as withholding nothing
// -- the same rule by which a failed lookup moves nobody on entering a tenant.
export async function tenantStanding(
  lookup: Promise<readonly string[] | null>
): Promise<boolean | null> {
  try {
    return ((await lookup) ?? []).length > 0
  } catch {
    return null
  }
}

// entityWorkspaceView says which view entering an entity lands on. The overview
// reads the entity record itself, which is tenant-wide entity administration:
// without that permission the page is a single refusal, so an account that
// merely belongs to the entity lands on the view it does have. One function
// because the switcher and the post-login redirect must not disagree.
export function entityWorkspaceView(
  tenantPermissions: Iterable<string>,
  entityType?: string
):
  | "project"
  | "baseline"
  | "overview"
  | "files"
  | "departments"
  | "work-items"
  | "requirements"
  | "goals"
  | "plans"
  | "reports" {
  const held = new Set(tenantPermissions)
  if (entityType === "project" && held.has("project_read")) return "project"
  if (held.has("entity_view")) return "overview"
  if (entityType === "project" && held.has("work_item_read"))
    return "work-items"
  if (entityType === "project" && held.has("requirement_read"))
    return "requirements"
  if (entityType === "project" && held.has("goal_read")) return "goals"
  if (entityType === "project" && (held.has("plan_submit") || held.has("plan_confirm"))) return "plans"
  if (entityType === "project" && held.has("report_read")) return "reports"
  if (entityType === "project" && held.has("channel_read")) return "baseline"
  if (entityType === "project" && held.has("channel_read")) return "baseline"
  if (!held.has("file_view") && held.has("dept_view")) return "departments"
  return "files"
}

// tenantSystemNavigation is what a tenant's sidebar shows beside its configured
// menu tree. Every entry except the overview is there only while the tree does
// not already name that path, which is what makes it a fallback rather than a
// second source: /files and /field-policies are seeded menus now, so a tenant
// created since carries its own rows and these entries fall silent. A tenant
// seeded before them has no rows, and nothing re-creates them behind an
// administrator's back -- a deleted menu cannot be told from one that never
// existed, so resurrecting them is an operator's deliberate act.
//
// The overview is unconditional because it is the workspace home rather than a
// page, and the access-request entry is unconditional-in-practice for a member
// without access_request_view: the seeded row is gated on the approver's
// permission, while filing a request is everybody's.
// Every entry here addresses the tenant. One used to address an entity: an
// account whose file access lives inside one was offered "files" in the tenant's
// sidebar pointing at /entities/{id}/files, so clicking what looked like a tenant
// item moved somebody into the entity perspective and the whole sidebar changed
// under them. That is what "the menu changes" was. Such an account belongs in the
// entity perspective, which it now lands in, and finds its files there.
export function tenantSystemNavigation(
  paths: Set<string>,
  fileAccess: boolean,
  fieldPolicyAccess: boolean,
  projectAccess = false,
  knowledgeAccess = false,
  machineAccess = false,
  agentAccess = false,
  permissions: Iterable<string> = []
): SystemNavigationItem[] {
  const held = new Set(permissions)
  const capabilityLinks = [
    ["knowledge_manage", "/knowledge/settings", "knowledgeSettings"],
    ["knowledge_read", "/knowledge/sources", "knowledgeSources"],
    ["gateway_manage", "/gateway", "fleetGatewayTitle"],
    ["machine_read", "/runners", "runtimeRunners"],
    ["tool_catalog_manage", "/tool-catalog", "toolCatalogTitle"],
    ["work_item_read", "/metrics", "runtimeMetrics"],
    ["work_item_read", "/my-work", "personalWorkTitle"],
    ["goal_read", "/goals", "planningGoals"],
    ["plan_submit", "/plans/me", "planningMyPlans"],
    ["report_read", "/reports", "planningReports"],
    ...(held.has("tenant_administer") ? [["performance_manage", "/performance", "performanceTitle"]] : []),
    ["template_read", "/templates", "templates"],
    ["policy_read", "/governance/policies", "govPoliciesTitle"],
    ["policy_read", "/governance/delegations", "delegationTitle"],
    ["audit_read", "/governance/escapes", "qualityEscapesTitle"],
    ["policy_read", "/governance/spawn-grants", "spawnTitle"],
    ["memory_approve", "/governance/memory-review", "memoryReviewTitle"],
    ["integration_manage", "/integrations", "planningIntegrations"],
  ].filter(([permission, path]) => held.has(permission!) && !paths.has(path!))
    .map(([, path, label]) => ({ path: path!, label: label! }))
  return [
    { path: "", label: "overview" },
    ...capabilityLinks,
    ...(!paths.has("/conversations") ? [{ path: "/conversations", label: "conversations" }] : []),
    ...(!paths.has("/governance/inbox") ? [{ path: "/governance/inbox", label: "govInboxTitle" }] : []),
    ...(!paths.has("/governance/agent-operations") ? [{ path: "/governance/agent-operations", label: "agentTab_operations" }] : []),
    ...(agentAccess && !paths.has("/agents") ? [{path:"/agents",label:"agents"}] : []),
    ...(machineAccess && !paths.has("/machines")
      ? [{path: "/machines", label: "machines"}]
      : []),
    ...(knowledgeAccess && !paths.has("/knowledge")
      ? [{ path: "/knowledge", label: "knowledge" }]
      : []),
    ...(projectAccess && !paths.has("/projects")
      ? [{ path: "/projects", label: "projects" }]
      : []),
    ...(!paths.has("/access-requests")
      ? [{ path: "/access-requests", label: "accessRequests" }]
      : []),
    ...(fileAccess && !paths.has("/files")
      ? [{ path: "/files", label: "files" }]
      : []),
    ...(fieldPolicyAccess && !paths.has("/field-policies")
      ? [{ path: "/field-policies", label: "fieldPermissions" }]
      : []),
  ]
}

export function defaultWorkspacePath(
  tenantID: string,
  permissions: ReadonlySet<string>
): string {
  if (permissions.has("tenant_view")) return "/system/tenants"
  if (permissions.has("platform_administer")) return "/system/audit-governance"
  return workspacePath(tenantID)
}

export function isSystemRoute(route: string): boolean {
  const url = new URL(route, "https://console.invalid")
  return url.pathname === "/system" || url.pathname.startsWith("/system/")
}

export function workspacePath(tenantID: string): string {
  return tenantID ? `/admin/${encodeURIComponent(tenantID)}` : "/admin/workspaces"
}

export function tenantNavigation(menus: EffectiveMenu[]): EffectiveMenu[] {
  return menus.flatMap((menu) => {
    if (
      menu.path === "/security" ||
      menu.path === "/account" ||
      menu.path?.startsWith("/account/")
    )
      return []
    const children = tenantNavigation(menu.children ?? [])
    if (!menu.path && children.length === 0) return []
    if (menu.path && isSystemRoute(menu.path))
      return children.length ? [{ ...menu, path: "", children }] : []
    return [{ ...menu, children }]
  })
}

export function menuLabel(
  menu: { label: string; translations?: Record<string, string> },
  locale: string
): string {
  const translations = menu.translations ?? {}
  const exact = Object.keys(translations).find(
    (key) => key.toLowerCase() === locale.toLowerCase()
  )
  return (
    (exact ? translations[exact] : translations[defaultLocale]) ||
    translations[defaultLocale] ||
    menu.label
  )
}

export function menuRows(
  menus: Menu[]
): { menu: Menu; depth: number; ancestors: string[] }[] {
  const ordered = [...menus].sort(
    (a, b) =>
      a.sort_order - b.sort_order ||
      a.id.localeCompare(b.id, undefined, { numeric: true })
  )
  const ids = new Set(menus.map((menu) => menu.id))
  const seen = new Set<string>()
  const rows: { menu: Menu; depth: number; ancestors: string[] }[] = []
  const visit = (menu: Menu, ancestors: string[]) => {
    if (seen.has(menu.id)) return
    seen.add(menu.id)
    rows.push({ menu, depth: ancestors.length, ancestors })
    for (const child of ordered.filter((child) => child.parent_id === menu.id))
      visit(child, [...ancestors, menu.id])
  }
  for (const menu of ordered.filter(
    (menu) => !menu.parent_id || !ids.has(menu.parent_id)
  ))
    visit(menu, [])
  for (const menu of ordered) visit(menu, [])
  return rows
}

export function flattenEffectiveMenus(menus: EffectiveMenu[]): EffectiveMenu[] {
  return menus.flatMap((menu) => [
    menu,
    ...flattenEffectiveMenus(menu.children ?? []),
  ])
}

export function effectiveMenuPaths(menus: EffectiveMenu[]): Set<string> {
  return new Set(
    flattenEffectiveMenus(menus)
      .map((menu) => menu.path)
      .filter((path): path is string => Boolean(path))
  )
}

export type MenuMove = {
  parent_id: string
  placement: {
    kind: "first" | "last" | "before" | "after"
    relative_to?: string
  }
}

export function adjacentMenuMove(
  menus: Menu[],
  id: string,
  direction: "up" | "down"
): MenuMove | null {
  const menu = menus.find((item) => item.id === id)
  if (!menu) return null
  const rows = menuRows(menus)
  const siblings = rows.filter(
    (row) => (row.menu.parent_id || "") === (menu.parent_id || "")
  )
  const index = siblings.findIndex((row) => row.menu.id === id)
  const adjacent = siblings[index + (direction === "up" ? -1 : 1)]?.menu
  if (adjacent) {
    if (menus.some((item) => item.parent_id === adjacent.id))
      return {
        parent_id: adjacent.id,
        placement: { kind: direction === "up" ? "last" : "first" },
      }
    return {
      parent_id: menu.parent_id || "",
      placement: {
        kind: direction === "up" ? "before" : "after",
        relative_to: adjacent.id,
      },
    }
  }
  const parent = menus.find((item) => item.id === menu.parent_id)
  return parent
    ? {
        parent_id: parent.parent_id || "",
        placement: {
          kind: direction === "up" ? "before" : "after",
          relative_to: parent.id,
        },
      }
    : null
}

export function droppedMenuMove(
  menus: Menu[],
  id: string,
  target: string,
  position: "before" | "inside" | "after"
): MenuMove | null {
  const source = menus.find((item) => item.id === id)
  if (!source || target === id) return null
  const targetRow = menuRows(menus).find((row) => row.menu.id === target)
  if (target && (!targetRow || targetRow.ancestors.includes(id))) return null
  if (!target) return { parent_id: "", placement: { kind: "last" } }
  return position === "inside"
    ? { parent_id: target, placement: { kind: "last" } }
    : {
        parent_id: targetRow!.menu.parent_id || "",
        placement: { kind: position, relative_to: target },
      }
}
