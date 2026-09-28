import {
  useCallback,
  useEffect,
  useMemo,
  useState,
  useSyncExternalStore,
  Fragment,
  type ReactNode,
} from "react"
import {
  adoptSessionTenant,
  iamApi,
  setTenant,
  type LoginResult,
  type Session,
} from "../lib/iam-api"
import { getPasskeyCredential } from "../lib/webauthn"
import { AuthContext, sessionFailureStatus, type AuthStatus } from "./auth"
import {
  getAccountView,
  subscribeAccountView,
  exitAccountView,
} from "../lib/account-view"
import { AccountViewBanner } from "./account-view-banner"
import { logoutDestination } from "../lib/auth-entrance"

export function AuthProvider({ children }: { children: ReactNode }) {
  const [session, setSession] = useState<Session | null>(null)
  const [status, setStatus] = useState<AuthStatus>("loading")
  const view = useSyncExternalStore(
    subscribeAccountView,
    getAccountView,
    () => null
  )
  const viewID = view?.id ?? ""
  const [verifiedViewID, setVerifiedViewID] = useState(viewID)

  const refresh = useCallback(async () => {
    setStatus("loading")
    try {
      const next = await iamApi.session()
      adoptSessionTenant(next)
      setSession(next)
      setVerifiedViewID(getAccountView()?.id ?? "")
      setStatus("authenticated")
    } catch (cause) {
      setSession(null)
      setStatus(sessionFailureStatus(cause))
      throw cause
    }
  }, [])

  useEffect(() => {
    let active = true
    void iamApi.session().then(
      (next) => {
        if (!active) return
        adoptSessionTenant(next)
        setSession(next)
        setVerifiedViewID(viewID)
        setStatus("authenticated")
      },
      (cause: unknown) => {
        if (!active) return
        setSession(null)
        setStatus(sessionFailureStatus(cause))
      }
    )
    return () => {
      active = false
    }
  }, [viewID])

  const finishLogin = useCallback(
    async (result: LoginResult) => {
      if (result.status !== "authenticated") return result
      await refresh()
      window.location.assign(result.return_url)
      return result
    },
    [refresh]
  )

  const login = useCallback(
    async (loginName: string, password: string, returnUrl: string) => {
      const result = await iamApi.login({
        login_name: loginName.trim(),
        password,
        return_url: returnUrl,
      })
      return finishLogin(result)
    },
    [finishLogin]
  )

  const verifyLoginMfa = useCallback(
    async (challengeId: string, code: string) =>
      finishLogin(
        await iamApi.verifyLoginMfa({ challenge_id: challengeId, code })
      ),
    [finishLogin]
  )

  const loginWithPasskey = useCallback(
    async (returnUrl: string) => {
      const options = await iamApi.beginPasskeyLogin(returnUrl)
      const credential = await getPasskeyCredential(options.options)
      return finishLogin(
        await iamApi.finishPasskeyLogin({
          challenge_id: options.challenge_id,
          credential,
        })
      )
    },
    [finishLogin]
  )

  const logout = useCallback(async () => {
    if (getAccountView()) {
      await exitAccountView()
      return
    }
    const result = await iamApi.logout()
    setTenant("")
    setSession(null)
    setStatus("anonymous")
    window.location.assign(logoutDestination(result?.logout_url))
  }, [])

  const value = useMemo(
    () => ({
      session: verifiedViewID === viewID ? session : null,
      status: verifiedViewID === viewID ? status : ("loading" as AuthStatus),
      login,
      verifyLoginMfa,
      loginWithPasskey,
      logout,
      refresh,
    }),
    [
      session,
      status,
      verifiedViewID,
      viewID,
      login,
      verifyLoginMfa,
      loginWithPasskey,
      logout,
      refresh,
    ]
  )
  return (
    <AuthContext.Provider value={value}>
      {view ? (
        <div className="flex h-svh flex-col">
          <AccountViewBanner view={view} />
          <div className="min-h-0 flex-1 [&>div]:h-full">
            <Fragment key={viewID}>{children}</Fragment>
          </div>
        </div>
      ) : (
        <Fragment key={viewID}>{children}</Fragment>
      )}
    </AuthContext.Provider>
  )
}
