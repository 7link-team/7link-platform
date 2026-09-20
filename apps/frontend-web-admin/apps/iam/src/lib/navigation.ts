import type { EffectiveMenu, Menu } from "./iam-api"
import { defaultLocale } from "@workspace/ui/i18n/config"

export function isPlatformRoute(route: string): boolean {
  const url = new URL(route, "https://console.invalid")
  return url.pathname === "/platform" || url.pathname.startsWith("/platform/")
}

export function tenantNavigation(menus: EffectiveMenu[]): EffectiveMenu[] {
  return menus.flatMap((menu) => {
    const children = tenantNavigation(menu.children ?? [])
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
    (a, b) => a.sort_order - b.sort_order || a.id.localeCompare(b.id)
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
