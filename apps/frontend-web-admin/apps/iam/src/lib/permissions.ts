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

// null means "not answered": the read failed, or this view is outside the
// console shell. Actions stay visible in that case, because a transient
// authorization outage that silently removes every button is worse than an
// occasional refused request.
export type EffectivePermissions = ReadonlySet<string> | null

export interface ConsoleOutletContext {
  menus: EffectiveMenu[] | null
  permissions: EffectivePermissions
  // False while the first read is still in flight. Unresolved is not the same as
  // unavailable: offering actions during the wait would show a view-only member
  // buttons that vanish a moment later, so actions wait for the answer and only
  // a failed answer falls back to showing them.
  permissionsResolved: boolean
}

export function can(permissions: EffectivePermissions, code: string): boolean {
  return permissions === null || permissions.has(code)
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
