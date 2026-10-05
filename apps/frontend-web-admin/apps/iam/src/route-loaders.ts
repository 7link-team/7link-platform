import { redirect, type LoaderFunctionArgs } from "react-router"
import { getTenant, iamApi, setTenant } from "./lib/iam-api"
import { defaultWorkspacePath } from "./lib/navigation"
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
async function entityWorkspacePath(tenantID: string): Promise<string> {
  if (!tenantID) return ""
  const none: { id: string }[] = []
  const [tenantPermissions, administered, files] = await Promise.all([
    iamApi.myPermissions(tenantID).catch(() => ["unknown"]),
    iamApi.memberEntities("entity_view").catch(() => none),
    iamApi.memberEntities("file_view").catch(() => none),
  ])
  if ((tenantPermissions ?? []).length > 0) return ""
  const base = `/admin/${encodeURIComponent(tenantID)}/entities`
  // Administering one is the fuller view; reaching only its files is still that
  // entity's perspective rather than the tenant's.
  const entity = administered[0]
  if (entity) return `${base}/${encodeURIComponent(entity.id)}/overview`
  const fileEntity = files[0]
  if (fileEntity) return `${base}/${encodeURIComponent(fileEntity.id)}/files`
  return ""
}

export async function workspaceHomeLoader() {
  const view = getAccountView()
  if (view) return redirect(accountViewPath(view, getTenant()))
  try {
    const [memberships, permissions, platforms, systemPermissions] =
      await Promise.all([
        iamApi.myTenants(),
        iamApi.myPermissions(),
        iamApi.myOperatingPlatforms(),
        iamApi.systemPermissions(),
      ])
    if (systemPermissions.length > 0) return redirect("/system")
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
    return redirect(defaultWorkspacePath(tenantID, new Set(permissions)))
  } catch {
    return redirect("/user")
  }
}
