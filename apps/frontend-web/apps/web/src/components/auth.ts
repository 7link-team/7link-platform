import { createContext, useContext } from "react"
import {
  ApiError,
  businessCode,
  type LoginResult,
  type Session,
} from "../lib/iam-api"

export type AuthStatus = "loading" | "authenticated" | "anonymous" | "error"

export function sessionFailureStatus(cause: unknown): "anonymous" | "error" {
  return cause instanceof ApiError && cause.status === 401
    ? "anonymous"
    : "error"
}

// loginError names the message for a failed sign-in step. Each step says what
// an unauthorized answer means for it -- a wrong password, a wrong code, an
// expired challenge -- and shares the rest.
export function loginError(
  cause: unknown,
  unauthorized = "invalidCredentials"
): string {
  if (!(cause instanceof ApiError)) return "identityUnavailable"
  if (cause.status === 401) return unauthorized
  // A rejected challenge arrives as 422 with its own code. Without this it
  // would read as "identity unavailable" and send the person away from a form
  // that only needed the image typed again.
  if (cause.businessCode === businessCode.captchaInvalid)
    return "invalidCaptcha"
  return "identityUnavailable"
}

export interface AuthContextValue {
  session: Session | null
  status: AuthStatus
  login: (
    loginName: string,
    password: string,
    returnUrl: string,
    captcha: { type: string; id: string; answer: string }
  ) => Promise<LoginResult>
  // Signing in by number is two calls: the first queues a code and says nothing
  // about whether the number reaches an account, the second spends it.
  requestPhoneLoginCode: (
    phone: string,
    captcha: { type: string; id: string; answer: string }
  ) => Promise<{ accepted: boolean }>
  loginWithPhoneCode: (
    phone: string,
    code: string,
    returnUrl: string
  ) => Promise<LoginResult>
  requestMfaPhoneCode: (challengeId: string) => Promise<{ accepted: boolean }>
  verifyLoginMfa: (challengeId: string, code: string) => Promise<LoginResult>
  loginWithPasskey: (returnUrl: string) => Promise<LoginResult>
  logout: () => Promise<void>
  refresh: () => Promise<void>
}

export const AuthContext = createContext<AuthContextValue | null>(null)

export function useAuth(): AuthContextValue {
  const value = useContext(AuthContext)
  if (!value) throw new Error("useAuth must be used inside AuthProvider")
  return value
}
