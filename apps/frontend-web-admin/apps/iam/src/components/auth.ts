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
