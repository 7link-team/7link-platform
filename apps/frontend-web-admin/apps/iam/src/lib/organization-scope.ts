// The tenant a console path addresses. Tenant IDs are snowflake numbers, so
// any other /admin/:tenantID segment names no tenant.
export function tenantIDFromPath(pathname: string): string {
  const match = /^\/admin\/([1-9][0-9]*)(?:\/|$)/.exec(pathname)
  return match ? decodeURIComponent(match[1] ?? "") : ""
}

// The entity a console path addresses, for any depth below it. Derived from the
// path's shape rather than matched against a pattern with a fixed number of
// segments: a pattern that named exactly one segment after the entity kept the
// perspective for /entities/:id/files and lost it for anything longer, so adding
// a view inside an entity silently moved somebody into the tenant's perspective.
export function entityIDFromPath(pathname: string): string {
  const match = /^\/admin\/[1-9][0-9]*\/entities\/([1-9][0-9]*)(?:\/|$)/.exec(
    pathname
  )
  return match ? decodeURIComponent(match[1] ?? "") : ""
}

export function routeTenantID(): string {
  if (typeof window === "undefined") return ""
  return tenantIDFromPath(window.location?.pathname ?? "")
}

export function operatingPlatformID(): string {
  if (typeof window === "undefined") return ""
  const match = /^\/platform\/([1-9][0-9]*)(?:\/|$)/.exec(
    window.location?.pathname ?? ""
  )
  return match ? decodeURIComponent(match[1] ?? "") : ""
}

// Member role and authorization-source reads exist only for tenants.
const tenantOnlyMemberReads =
  /^\/tenants\/[^/]+\/members\/[^/]+\/(roles|authorization-sources)(?=\/|\?|$)/

export function organizationRouteURL(url: string): string {
  const platform = operatingPlatformID()
  if (!platform || tenantOnlyMemberReads.test(url)) return url
  return url.replace(
    /^\/tenants\/[^/]+\/(roles|menus|permission-catalog|members|departments|principal-groups)(?=\/|\?|$)/,
    `/operating-platforms/${encodeURIComponent(platform)}/$1`
  )
}
