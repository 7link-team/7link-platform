export interface MembershipDates {
  starts_at?: string
  ends_at?: string
}

export function systemRoleLabel(
  role: { name: string; builtin?: boolean },
  fullAdministrator: string,
  importedPermissions: string
): string {
  if (role.builtin) return fullAdministrator
  return /^__system_legacy_[1-9][0-9]*$/u.test(role.name)
    ? importedPermissions
    : role.name
}

export function membershipState(
  member: MembershipDates,
  now = Date.now()
): "scheduled" | "active" | "ended" {
  if (member.starts_at && Date.parse(member.starts_at) > now) return "scheduled"
  if (member.ends_at && Date.parse(member.ends_at) <= now) return "ended"
  return "active"
}

export function toDateTimeLocal(value?: string): string {
  if (!value) return ""
  const date = new Date(value)
  const offset = date.getTimezoneOffset() * 60_000
  return new Date(date.getTime() - offset).toISOString().slice(0, 16)
}

export function toISOString(value: string): string | undefined {
  if (!value) return undefined
  const date = new Date(value)
  return Number.isNaN(date.getTime()) ? undefined : date.toISOString()
}

export function membershipWindow(
  member: MembershipDates,
  locale: string,
  unbounded: string
): string {
  const format = (value?: string) =>
    value
      ? new Intl.DateTimeFormat(locale, {
          dateStyle: "medium",
          timeStyle: "short",
        }).format(new Date(value))
      : unbounded
  return `${format(member.starts_at)} - ${format(member.ends_at)}`
}
