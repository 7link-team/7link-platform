export function routeTenantID(): string {
  if (typeof window === "undefined") return ""
  const match = /^\/admin\/([1-9][0-9]*)(?:\/|$)/.exec(
    window.location?.pathname ?? ""
  )
  return match ? decodeURIComponent(match[1] ?? "") : ""
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
