export interface DevLoginAccount {
  id: string
  role: string
  loginName: string
  password: string
  tenantID?: string
  platformID?: string
}

export function parseDevLoginAccounts(
  raw: string | undefined,
  enabled = import.meta.env.DEV &&
    import.meta.env.VITE_DEV_LOGIN_ACCOUNTS_ENABLED === "true"
): DevLoginAccount[] {
  if (!enabled || !raw) return []

  try {
    const value: unknown = JSON.parse(raw)
    if (!Array.isArray(value)) return []

    return value.filter(
      (account): account is DevLoginAccount =>
        typeof account === "object" &&
        account !== null &&
        typeof account.id === "string" &&
        account.id.length > 0 &&
        typeof account.role === "string" &&
        account.role.length > 0 &&
        typeof account.loginName === "string" &&
        account.loginName.length > 0 &&
        typeof account.password === "string" &&
        account.password.length > 0 &&
        (account.tenantID === undefined ||
          (typeof account.tenantID === "string" &&
            account.tenantID.length > 0)) &&
        (account.platformID === undefined ||
          (typeof account.platformID === "string" &&
            account.platformID.length > 0))
    )
  } catch {
    return []
  }
}

export const devLoginAccounts = parseDevLoginAccounts(
  import.meta.env.VITE_DEV_LOGIN_ACCOUNTS
)

export const prefillDevLoginAccount =
  import.meta.env.DEV && import.meta.env.VITE_DEV_LOGIN_PREFILL === "true"
