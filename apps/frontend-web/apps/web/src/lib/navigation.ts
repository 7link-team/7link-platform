import type { EffectiveMenu, Menu } from "./iam-api"
import { defaultLocale } from "@workspace/ui/i18n/config"

export type SystemNavigationItem = { path: string; label: string }

export function firstEffectiveMenuPath(menus: EffectiveMenu[]): string {
  for (const menu of menus) {
    const child = firstEffectiveMenuPath(menu.children ?? [])
    if (child) return child
    if (menu.path) return menu.path
  }
  return ""
}

export const personalNavigation: SystemNavigationItem[] = [
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
    ...(entityType === "project" && held.has("project_read")
      ? [{ path: "/project", label: "projectDetails" }]
      : []),
    ...(entityAccess
      ? [
          { path: "/overview", label: "entityOverview" },
          { path: "/roles", label: "entityAccessControl" },
        ]
      : []),
    ...(held.has("file_view") ? [{ path: "/files", label: "files" }] : []),
    ...(held.has("dept_view")
      ? [{ path: "/departments", label: "departmentManagement" }]
      : []),
    ...(entityAccess ? [{ path: "/manage", label: "entitySettings" }] : []),
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
  tenantPermissions: Iterable<string>
): "overview" | "files" {
  for (const code of tenantPermissions) if (code === "entity_view") return "overview"
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
  fieldPolicyAccess: boolean
): SystemNavigationItem[] {
  return [
    { path: "", label: "overview" },
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
  return tenantID ? `/admin/${encodeURIComponent(tenantID)}` : "/user"
}

export function tenantNavigation(menus: EffectiveMenu[]): EffectiveMenu[] {
  return menus.flatMap((menu) => {
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
