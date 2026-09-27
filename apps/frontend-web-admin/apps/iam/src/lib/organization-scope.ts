export function operatingPlatformID(): string {
  if (typeof window === "undefined") return ""
  const match = /^\/operating-platforms\/([^/]+)(?:\/|$)/.exec(
    window.location?.pathname ?? ""
  )
  return match ? decodeURIComponent(match[1] ?? "") : ""
}

export function organizationRouteURL(url: string): string {
  const platform = operatingPlatformID()
  if (!platform) return url
  return url.replace(
    /^\/tenants\/[^/]+\/(roles|menus|permission-catalog|members|departments|principal-groups)(?=\/|\?|$)/,
    `/operating-platforms/${encodeURIComponent(platform)}/$1`
  )
}
