import type { EffectiveMenu, Menu } from "./iam-api"
import { defaultLocale } from "@workspace/ui/i18n/config"

export type SystemNavigationItem = { path: string; label: string }

export const systemAdministrationMenus = [
  {
    label: "navigationOrganization",
    children: [
      {
        path: "/system/accounts",
        label: "systemAccounts",
        code: "system_account_view",
      },
      {
        path: "/system/operating-platforms",
        label: "operatingPlatforms",
        code: "operating_platform_view",
      },
      {
        path: "/system/tenants",
        label: "tenantManagement",
        code: "tenant_view",
      },
    ],
  },
  {
    label: "navigationAccess",
    children: [
      {
        path: "/system/administrators",
        label: "systemAdministrators",
        code: "platform_administer",
      },
    ],
  },
  {
    label: "navigationAudit",
    children: [
      {
        path: "/system/audit-governance",
        label: "platformAudit",
        code: "platform_administer",
      },
      {
        path: "/system/closure-requests",
        label: "closureRequests",
        code: "platform_administer",
      },
    ],
  },
]

export function systemAdministrationNavigation(
  permissions: ReadonlySet<string> | null
) {
  return systemAdministrationMenus
    .map((group) => ({
      ...group,
      children: group.children.filter((item) => permissions?.has(item.code)),
    }))
    .filter((group) => group.children.length)
}

export function platformNavigation(
  tenantAccess: boolean,
  administrator: boolean
): SystemNavigationItem[] {
  return [
    ...(tenantAccess
      ? [{ path: "/system/tenants", label: "tenantManagement" }]
      : []),
    ...(administrator
      ? [
          { path: "/system/audit-governance", label: "platformAudit" },
          { path: "/system/closure-requests", label: "closureRequests" },
        ]
      : []),
  ]
}

export const personalNavigation: SystemNavigationItem[] = [
  { path: "/me/profile", label: "profile" },
  { path: "/me/security", label: "security" },
  { path: "/me/mfa", label: "twoFactor" },
  { path: "/me/tokens", label: "personalTokens" },
  { path: "/me/files", label: "myFiles" },
  { path: "/me/memberships", label: "myMemberships" },
  { path: "/me/identities", label: "linkedIdentities" },
  { path: "/me/closure", label: "accountClosure" },
  { path: "/me/applications", label: "authorizedApplications" },
]

export function entityNavigation(
  entityAccess: boolean,
  fileAccess: boolean
): SystemNavigationItem[] {
  return [
    ...(entityAccess
      ? [
          { path: "/overview", label: "entityOverview" },
          { path: "/roles", label: "entityAccessControl" },
        ]
      : []),
    ...(fileAccess ? [{ path: "/files", label: "files" }] : []),
    ...(entityAccess ? [{ path: "/manage", label: "entitySettings" }] : []),
  ]
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

export function isPlatformRoute(route: string): boolean {
  const url = new URL(route, "https://console.invalid")
  return url.pathname === "/system" || url.pathname.startsWith("/system/")
}

export function workspacePath(tenantID: string): string {
  return tenantID ? `/tenants/${encodeURIComponent(tenantID)}` : "/me"
}

export function tenantNavigation(menus: EffectiveMenu[]): EffectiveMenu[] {
  return menus.flatMap((menu) => {
    const children = tenantNavigation(menu.children ?? [])
    if (!menu.path && children.length === 0) return []
    if (menu.path && isPlatformRoute(menu.path))
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
