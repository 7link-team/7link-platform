export const authEntrances = ["system", "platform", "tenant", "user"] as const
export type AuthEntrance = (typeof authEntrances)[number]

export function authEntrance(
  value: string | undefined | null
): AuthEntrance | null {
  return authEntrances.find((entry) => entry === value) ?? null
}

export function publicAuthEntrance(
  value: string | undefined | null
): "tenant" | "user" | null {
  return value === "tenant" || value === "user" ? value : null
}

export function entranceLoginPath(entry: AuthEntrance | null): string {
  return entry ? `/login/${entry}` : "/login"
}
