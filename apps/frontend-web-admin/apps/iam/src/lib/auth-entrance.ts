export const authEntrances = ["system", "platform", "tenant", "user"] as const
export type AuthEntrance = (typeof authEntrances)[number]

export function authEntrance(
  value: string | undefined | null
): AuthEntrance | null {
  if (value === "admin") return "tenant"
  return authEntrances.find((entry) => entry === value) ?? null
}

export function publicAuthEntrance(
  value: string | undefined | null
): "tenant" | "user" | null {
  if (value === "admin") return "tenant"
  return value === "tenant" || value === "user" ? value : null
}

export function entrancePath(
  entry: AuthEntrance | null,
  page: "login" | "workspaces" | "register" | "recover" | "verify-email"
): string {
  return `/${entry === "tenant" ? "admin" : (entry ?? "user")}/${page}`
}

export function entranceLoginPath(entry: AuthEntrance | null): string {
  return entrancePath(entry, "login")
}

// Where a sign-in continues: a requested page inside the same entrance,
// otherwise that entrance's workspace chooser.
export function loginReturnPath(
  entry: AuthEntrance | null,
  from: string | undefined
): string {
  const prefix = entrancePath(entry, "login").replace(/\/login$/, "")
  return from?.startsWith(`${prefix}/`)
    ? from
    : entrancePath(entry, "workspaces")
}

export function logoutDestination(logoutURL?: string): string {
  const entry = authEntrance(window.location.pathname.split("/")[1])
  if (logoutURL) {
    const target = new URL(logoutURL, window.location.origin)
    if (
      target.origin !== window.location.origin ||
      !/^\/(?:login|(?:system|platform|admin|user)\/login)$/.test(
        target.pathname
      )
    )
      return logoutURL
  }
  return entranceLoginPath(entry)
}
