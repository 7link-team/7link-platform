// What the console may offer, as opposed to what it may show.
//
// Navigation is filtered by the effective menu tree, but a menu entry carries a
// single page-level code and says nothing about verbs, so a member with only
// `user_view` was still offered create, edit and delete buttons that the server
// then refused. These codes come from `/iam/me/permissions` and are the
// caller's own, for the tenant currently selected plus whatever platform
// authority they hold.
//
// Hiding a control is not a security measure -- every route still checks on the
// server. It exists so that what the console offers matches what the person can
// actually do, which is also what makes a permission mistake visible instead of
// arriving as a wall of predictable 403s.
import { useCallback } from "react"
import { useOutletContext } from "react-router"

import type { EffectiveMenu } from "./iam-api"

// Unknown permission reads cannot authorize a protected action. The shell
// surfaces failed reads and offers a retry instead of exposing forbidden verbs.
export type EffectivePermissions = ReadonlySet<string> | null

export interface ConsoleOutletContext {
  menus: EffectiveMenu[] | null
  permissions: EffectivePermissions
  // False while the first read is still in flight. Unresolved is not the same as
  // unavailable: protected actions wait for a successful answer in either case.
  permissionsResolved: boolean
}

export function can(permissions: EffectivePermissions, code: string): boolean {
  return permissions?.has(code) ?? false
}

/**
 * useAllowed reports whether the current caller holds one permission code.
 *
 * Codes are `resource_verb`, the same strings the route guards and role grants
 * use, so a page asks for exactly what the endpoint behind the button checks.
 */
export function useAllowed(): (code: string) => boolean {
  const context = useOutletContext<ConsoleOutletContext | null>()
  const permissions = context?.permissions ?? null
  // A view rendered outside the console shell has no context and no actions to
  // gate, so it counts as resolved rather than permanently pending.
  const resolved = context ? context.permissionsResolved : true
  return useCallback(
    (code: string) => resolved && can(permissions, code),
    [permissions, resolved]
  )
}
