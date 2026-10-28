import { redirect, type LoaderFunctionArgs } from "react-router"
import { getTenant, iamApi, setTenant } from "./lib/iam-api"
import {
  defaultWorkspacePath,
  entityWorkspaceView,
  workspaceEntityPermissions,
} from "./lib/navigation"
import { accountViewPath, getAccountView } from "./lib/account-view"
import {
  authEntrance,
  publicAuthEntrance,
  entrancePath,
} from "./lib/auth-entrance"

export function legacyRouteLoader({ request }: LoaderFunctionArgs) {
  const url = new URL(request.url)
  const old = url.pathname
  let path = old
    .replace(/^\/me(?=\/|$)/, "/user")
    .replace(/^\/tenants(?=\/|$)/, "/admin")
    .replace(/^\/operating-platforms(?=\/|$)/, "/platform")
  const auth =
    /^\/(login|register|recover|workspaces)(?:\/(system|platform|tenant|user))?$/.exec(
      old
    )
  if (auth)
    path = entrancePath(
      authEntrance(auth[2]) ??
        publicAuthEntrance(url.searchParams.get("entry")),
      auth[1] as "login" | "register" | "recover" | "workspaces"
    )
  if (old === "/verify-email")
    path = entrancePath(
      publicAuthEntrance(url.searchParams.get("entry")),
      "verify-email"
    )
  if (old === "/accept-invitation") path = "/admin/accept-invitation"
  if (path === old) throw new Response("Not Found", { status: 404 })
  return redirect(
    path +
      url.search +
      (typeof window === "undefined" ? url.hash : window.location.hash)
  )
}

// entityWorkspacePath answers where an account whose standing is inside one
// entity belongs. The tenant perspective is shaped for somebody who administers
// the tenant; an account that administers an entity has no business landing in
// it, even though the shell there offers it nothing but self-service.
//
// The service is the authority on both halves, so neither is inferred: a grant
// narrowed to an entity is deliberately absent from the tenant-level permission
// list, which makes "nothing at tenant level" an answer rather than a guess, and
// the entity listing reports exactly which entities admit this caller for a
// named permission. An account with any tenant standing keeps the tenant
// perspective, and one the service cannot answer for keeps it too -- a failed
// lookup must not move anybody.
//
// Both lookups name the tenant. Discovery can begin before navigation commits
// and finish after it; the selected tenant must not follow the address bar's
// timing or inherit the tenant or platform the caller is leaving.
async function entityWorkspacePath(tenantID: string, projectHome = false): Promise<string> {
  if (!tenantID) return ""
  const none: { id: string; status: string }[] = []
  const tenantPermissions = await iamApi.myPermissions(tenantID).catch(() => ["unknown"])
  if ((tenantPermissions ?? []).length > 0) return ""
  const entities = await iamApi
    .memberEntities(workspaceEntityPermissions, tenantID)
    .catch(() => none)
  // The same listing the switcher offers, filtered the way it filters, so the
  // two name the same entity; and the files view because an account with no
  // tenant standing has no tenant-wide entity administration either -- the
  // overview would be one refusal.
  const entity = entities.find(
    (candidate) =>
      candidate.status === "active" &&
      (!("type" in candidate) || candidate.type !== "client")
  )
  if (!entity) return ""
  // Explicit Home navigation admits project members. Its source API applies
  // project and row authority; this does not grant tenant administration.
  if (projectHome && "type" in entity && entity.type === "project") return ""
  const granted = await iamApi
    .myPermissions(tenantID, entity.id)
    .catch(() => [])
  return `/admin/${encodeURIComponent(tenantID)}/entities/${encodeURIComponent(entity.id)}/${entityWorkspaceView(granted, "type" in entity ? String(entity.type) : undefined)}`
}

// tenantWorkspaceLoader decides which perspective of one named tenant the caller
// gets. Entering a tenant is a destination, so this is not the workspace home:
// that one answers "where do I go with nowhere asked for" and sends an operator
// with system authority to /system, which would bounce anybody who had just
// picked a tenant straight back out of it.
//
// Only an account with nothing at tenant level is moved, and only into an entity
// that admits it. Everybody else -- a member with any standing, an operator whose
// authority comes from above and who has no tenant grants at all -- gets the
// tenant's own perspective, which is what they asked for.
export function tenantWorkspaceLoader({ params }: LoaderFunctionArgs) {
  const tenantID = String(params.tenantID ?? "")
  // Commit the destination before its perspective lookup finishes. Returning
  // this promise as data keeps a slow permission read from pinning a click to
  // the chooser's URL; the destination waits before rendering its contents.
  return {
    workspacePath: !tenantID || getAccountView()
      ? Promise.resolve("")
      : entityWorkspacePath(tenantID, true),
  }
}

export async function workspaceHomeLoader() {
  const view = getAccountView()
  if (view) return redirect(accountViewPath(view, getTenant()))
  try {
    // A system administrator need not belong to a tenant. Its landing must not
    // depend on tenant-scoped discovery succeeding first.
    const systemPermissions = await iamApi.systemPermissions().catch(() => [])
    if (systemPermissions.length > 0) return redirect("/system")
    const [memberships, platforms] =
      await Promise.all([
        iamApi.myTenants(),
        iamApi.myOperatingPlatforms().catch(() => []),
      ])
    if (
      platforms.length &&
      !memberships.some(
        (tenant) => tenant.status === "active" && tenant.id === getTenant()
      )
    )
      return redirect(`/platform/${encodeURIComponent(platforms[0]!.id)}`)
    const tenants = memberships.filter((tenant) => tenant.status === "active")
    const selected = getTenant()
    const tenantID = tenants.some((tenant) => tenant.id === selected)
      ? selected
      : (tenants[0]?.id ?? "")
    if (tenantID) setTenant(tenantID)
    const entityHome = await entityWorkspacePath(tenantID)
    if (entityHome) return redirect(entityHome)
    const permissions = await iamApi.myPermissions(tenantID || undefined).catch(() => [])
    return redirect(defaultWorkspacePath(tenantID, new Set(permissions)))
  } catch {
    return redirect("/user")
  }
}
