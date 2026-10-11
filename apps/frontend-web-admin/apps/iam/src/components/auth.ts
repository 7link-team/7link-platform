import { createContext, useContext } from "react"
import { ApiError, type LoginResult, type Session } from "../lib/iam-api"

export type AuthStatus = "loading" | "authenticated" | "anonymous" | "error"

export function sessionFailureStatus(cause: unknown): "anonymous" | "error" {
  return cause instanceof ApiError && cause.status === 401
    ? "anonymous"
    : "error"
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
