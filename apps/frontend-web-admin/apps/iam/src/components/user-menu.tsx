import { useTranslations } from "@workspace/ui/i18n/intl-provider"
import { LogOut, UserRound } from "lucide-react"
import { useNavigate } from "react-router"
import { useState } from "react"
import { Alert } from "./alert"
import { Button } from "@workspace/ui/components/button"
import { Avatar, AvatarFallback } from "@workspace/ui/components/avatar"
import {
  DropdownMenu,
  DropdownMenuContent,
  DropdownMenuItem,
  DropdownMenuLabel,
  DropdownMenuSeparator,
  DropdownMenuTrigger,
} from "@workspace/ui/components/dropdown-menu"
import { useAuth } from "./auth"

export function UserMenu() {
  const t = useTranslations("iam")

  const navigate = useNavigate()
  const { session, logout } = useAuth()
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
  const name = session.preferred_username ?? session.email ?? session.subject
  const initials = name.slice(0, 2).toUpperCase()

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
          <Avatar size="sm">
            <AvatarFallback>{initials}</AvatarFallback>
          </Avatar>
          <span className="hidden truncate text-xs sm:inline">{name}</span>
        </DropdownMenuTrigger>
        <DropdownMenuContent align="end" className="w-56">
          <DropdownMenuLabel className="font-normal">
            <p className="truncate text-sm font-medium">{name}</p>
            <p className="mt-1 truncate text-xs text-muted-foreground">
              {session.email ?? session.subject}
            </p>
          </DropdownMenuLabel>
          <DropdownMenuSeparator />
          <DropdownMenuItem onClick={() => navigate("/account/profile")}>
            <UserRound className="mr-2 size-4" />
            {t("account")}
          </DropdownMenuItem>
          <DropdownMenuSeparator />
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
