import { useTranslations } from "@workspace/ui/i18n/intl-provider"
import { FileClock, Landmark, LogOut, UserRound } from "lucide-react"
import { useNavigate } from "react-router"
import { useEffect, useState } from "react"
import { Alert } from "./alert"
import { Button } from "@workspace/ui/components/button"
import { UserAvatar } from "./user-avatar"
import {
  DropdownMenu,
  DropdownMenuContent,
  DropdownMenuItem,
  DropdownMenuLabel,
  DropdownMenuSeparator,
  DropdownMenuTrigger,
} from "@workspace/ui/components/dropdown-menu"
import { useAuth } from "./auth"
import { iamApi, type AvatarSettings } from "../lib/iam-api"
import { getAccountView } from "../lib/account-view"
import { selfProfileApi, type SelfProfile } from "../app/security/profile-api"

export function UserMenu(props: Parameters<typeof AccountUserMenu>[0]) {
  const view = getAccountView()
  if (view && view.scope_type !== "personal") return null
  return <AccountUserMenu {...props} />
}

function AccountUserMenu({
  platformTenantAccess = false,
  platformAuditAccess = false,
}: {
  tenantID: string
  platformTenantAccess?: boolean
  platformAuditAccess?: boolean
}) {
  const t = useTranslations("iam")

  const navigate = useNavigate()
  const { session, logout } = useAuth()
  const subject = session?.subject ?? ""
  const [avatar, setAvatar] = useState<{
    subject: string
    value: AvatarSettings
  } | null>(null)
  useEffect(() => {
    let active = true
    const refresh = () => {
      if (subject)
        void iamApi.myAvatar().then(
          (value) => {
            if (active) setAvatar({ subject, value })
          },
          () => {
            if (active) setAvatar(null)
          }
        )
    }
    refresh()
    window.addEventListener("avatar-change", refresh)
    window.addEventListener("focus", refresh)
    return () => {
      active = false
      window.removeEventListener("avatar-change", refresh)
      window.removeEventListener("focus", refresh)
    }
  }, [subject])
  const [profile, setProfile] = useState<{
    subject: string
    value: SelfProfile
  } | null>(null)
  useEffect(() => {
    // Account views use scoped bearer credentials; self profile requires the
    // principal's own browser session. Use the view's session labels instead.
    if (!session?.subject || getAccountView()) return
    const controller = new AbortController()
    const subject = session.subject
    void selfProfileApi.get(subject, controller.signal).then(
      (next) => {
        if (!controller.signal.aborted) setProfile({ subject, value: next })
      },
      () => {
        if (!controller.signal.aborted) setProfile(null)
      }
    )
    return () => {
      controller.abort()
    }
  }, [session])
  const [busy, setBusy] = useState(false)
  const [error, setError] = useState(false)
  const signOut = async () => {
    setBusy(true)
    setError(false)
    try {
      await logout()
    } catch {
      setError(true)
    } finally {
      setBusy(false)
    }
  }
  if (!session) return null
  const selfProfile =
    !getAccountView() && profile?.subject === subject ? profile.value : null
  const name =
    selfProfile?.display_name?.trim() ||
    session.preferred_username?.trim() ||
    selfProfile?.email?.trim() ||
    session.email?.trim() ||
    t("accountMenu")
  const email = selfProfile?.email?.trim() || session.email?.trim()

  return (
    <>
      {error && (
        <div className="fixed inset-x-4 top-16 z-50 mx-auto max-w-lg">
          <Alert>
            <p>{t("logoutFailed")}</p>
            <Button
              variant="outline"
              disabled={busy}
              onClick={() => void signOut()}
            >
              {t("retryNow")}
            </Button>
          </Alert>
        </div>
      )}
      <DropdownMenu>
        <DropdownMenuTrigger
          aria-label={t("accountMenu")}
          className="flex h-9 max-w-48 items-center gap-2 rounded-md px-2 text-sm text-muted-foreground transition-colors hover:bg-accent hover:text-accent-foreground focus-visible:ring-2 focus-visible:ring-ring"
        >
          <UserAvatar
            id={subject}
            avatar={avatar?.subject === subject ? avatar.value : null}
            className="size-7"
          />
          <span className="hidden truncate text-xs sm:inline">{name}</span>
        </DropdownMenuTrigger>
        <DropdownMenuContent align="end" className="w-56">
          <DropdownMenuLabel className="font-normal">
            <p className="truncate text-sm font-medium">{name}</p>
            {email && (
              <p className="mt-1 truncate text-xs text-muted-foreground">
                {email}
              </p>
            )}
          </DropdownMenuLabel>
          <DropdownMenuSeparator />
          <DropdownMenuItem onClick={() => navigate("/user/profile")}>
            <UserRound className="mr-2 size-4" />
            {t("account")}
          </DropdownMenuItem>
          <DropdownMenuSeparator />
          {(platformTenantAccess || platformAuditAccess) && (
            <>
              <DropdownMenuLabel>{t("platformManagement")}</DropdownMenuLabel>
              {platformTenantAccess && (
                <DropdownMenuItem onClick={() => navigate("/system/tenants")}>
                  <Landmark className="mr-2 size-4" />
                  {t("tenantManagement")}
                </DropdownMenuItem>
              )}
              {platformAuditAccess && (
                <DropdownMenuItem
                  onClick={() => navigate("/system/audit-governance")}
                >
                  <FileClock className="mr-2 size-4" />
                  {t("platformAudit")}
                </DropdownMenuItem>
              )}
              <DropdownMenuSeparator />
            </>
          )}
          <DropdownMenuItem
            disabled={busy}
            onClick={() => void signOut()}
            className="text-destructive focus:text-destructive"
          >
            <LogOut className="mr-2 size-4" />
            {t("logout")}
          </DropdownMenuItem>
        </DropdownMenuContent>
      </DropdownMenu>
    </>
  )
}
