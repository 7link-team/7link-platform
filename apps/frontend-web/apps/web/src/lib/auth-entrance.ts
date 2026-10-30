export const authEntrances = ["system", "platform", "tenant", "user"] as const

// Extract a credential only; never navigate to or fetch an entered URL.
export function workspaceInvitationToken(value: string): string | null {
  let token = value.trim()
  if (!token.startsWith("inv1_")) {
    try {
      const link = new URL(token, "https://workspace.invalid")
      if (!["http:", "https:"].includes(link.protocol) || !["/admin/accept-invitation", "/accept-invitation"].includes(link.pathname)) return null
      token = link.searchParams.get("token") ?? ""
    } catch { return null }
  }
  return token.length >= 40 && token.length <= 256 && /^inv1_[1-9][0-9]*\.[A-Za-z0-9_-]+$/.test(token) ? token : null
}
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
  // A gateway handoff has no workspace entrance. Continue only this exact
  // local exchange page after sign-in; never accept an external redirect.
  if (from === "/preview-access" || from?.startsWith("/preview-access?")) return from
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
