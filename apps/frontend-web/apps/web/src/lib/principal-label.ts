export interface PrincipalLabel {
  login_name?: string
  display_name?: string
  anonymized?: boolean
}

/** Current permitted identity fields only; an ID is not an account name. */
export function principalLabel(
  principal: PrincipalLabel | null | undefined,
  unavailable: string,
  anonymized: string
): string {
  if (principal?.anonymized) return anonymized
  const display = principal?.display_name?.trim() ?? ""
  const login = principal?.login_name?.trim() ?? ""
  if (display && login && display !== login) return `${display}（${login}）`
  return display || login || unavailable
}
