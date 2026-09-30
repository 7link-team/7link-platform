import { authEntrances, type AuthEntrance } from "./auth-entrance"

export interface DevLoginAccount {
  id: string
  role: string
  loginName: string
  password: string
  tenantID?: string
  platformID?: string
  entrances?: AuthEntrance[]
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
        (account.entrances === undefined ||
          (Array.isArray(account.entrances) &&
            account.entrances.length > 0 &&
            account.entrances.every((entry: unknown) =>
              authEntrances.includes(entry as AuthEntrance)
            ))) &&
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

// The environment is read only inside the development branch, so a production
// build folds the branch away with the credential literal Vite inlined into it.
export const devLoginAccounts =
  import.meta.env.DEV &&
  import.meta.env.VITE_DEV_LOGIN_ACCOUNTS_ENABLED === "true"
    ? parseDevLoginAccounts(import.meta.env.VITE_DEV_LOGIN_ACCOUNTS)
    : []

export function devAccountsForEntrance(
  entry: AuthEntrance | null,
  accounts = devLoginAccounts
): DevLoginAccount[] {
  return accounts.filter((account) =>
    account.entrances?.includes(entry ?? "user")
  )
}

export const prefillDevLoginAccount =
  import.meta.env.DEV && import.meta.env.VITE_DEV_LOGIN_PREFILL === "true"

// Read inside the development branch for the same reason as the personas: a
// production build folds the branch away rather than inlining the literal.
//
// Unlike the personas this is not gated on the persona switch. The acceptance
// runs drive the real form without asking for personas, and they need the
// challenge answered just the same. It is inert anyway against a service that
// configured no test answer, which is every service real people use.
export const devLoginCaptchaAnswer = import.meta.env.DEV
  ? (import.meta.env.VITE_DEV_LOGIN_CAPTCHA_ANSWER ?? "")
  : ""
