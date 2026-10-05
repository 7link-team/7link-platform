import type { EffectiveMenu, Menu } from "./iam-api"
import { defaultLocale } from "@workspace/ui/i18n/config"

export type SystemNavigationItem = {
  path: string
  label: string
  /** The path hangs off the tenant rather than the entity being viewed. */
  tenantPath?: boolean
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
//
// tenantPath is absolute because the department register is a tenant-scoped page
// that narrows to the departments the grant names; the rest hang off this
// entity's own base.
export function entityNavigation(
  permissions: Iterable<string>
): SystemNavigationItem[] {
  const held = new Set(permissions)
  const entityAccess = held.has("entity_view")
  return [
    ...(entityAccess
      ? [
          { path: "/overview", label: "entityOverview" },
          { path: "/roles", label: "entityAccessControl" },
        ]
      : []),
    ...(held.has("file_view") ? [{ path: "/files", label: "files" }] : []),
    ...(held.has("dept_view")
      ? [{ path: "/departments", label: "departmentManagement", tenantPath: true }]
      : []),
    ...(entityAccess ? [{ path: "/manage", label: "entitySettings" }] : []),
  ]
}

// tenantPerspectiveOffered says whether the tenant's perspective may be offered
// at all. Inside an entity the permission set in hand is that entity's, so the
// tenant answer has to be the one asked for separately -- and an answer that has
// not arrived yet is not a yes, because a view is shown once it is known to be
// allowed rather than until it is known not to be.
export function tenantPerspectiveOffered(
  insideEntity: boolean,
  tenantStanding: boolean | null,
  perspectivePermissionCount: number
): boolean {
  return insideEntity ? tenantStanding === true : perspectivePermissionCount > 0
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

export function tenantSystemNavigation(
  paths: Set<string>,
  fileAccess: boolean,
  fieldPolicyAccess: boolean,
  scopedFileEntityID = ""
): SystemNavigationItem[] {
  return [
    { path: "", label: "overview" },
    ...(!paths.has("/access-requests")
      ? [{ path: "/access-requests", label: "accessRequests" }]
      : []),
    ...(fileAccess && !paths.has("/files")
      ? [{ path: "/files", label: "files" }]
      : []),
    ...(!fileAccess && scopedFileEntityID
      ? [
          {
            path: `/entities/${encodeURIComponent(scopedFileEntityID)}/files`,
            label: "files",
          },
        ]
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
