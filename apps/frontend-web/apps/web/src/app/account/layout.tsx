import { NotificationBell } from "../../components/notification-bell"
import { useTranslations } from "@workspace/ui/i18n/intl-provider"
import { useState, type ReactNode } from "react"
import {
  ArrowLeft,
  Menu,
  ShieldCheck,
  Smartphone,
  KeyRound,
  Folder,
  UserRound,
} from "lucide-react"
import { Link, Navigate, NavLink, Outlet, useLocation } from "react-router"
import {
  Sheet,
  SheetContent,
  SheetTitle,
  SheetTrigger,
} from "@workspace/ui/components/sheet"
import { LanguageSwitcher } from "@workspace/ui/components/i18n/language-switcher"
import { useAuth } from "../../components/auth"
import { SessionVerification } from "../../components/session-verification"
import { ThemeModeButton } from "../../components/theme-mode-button"
import { UserMenu } from "../../components/user-menu"
import { getTenant } from "../../lib/iam-api"
import { NavigationGroup } from "../../components/navigation-group"
import { personalNavigation } from "../../lib/navigation"
import { getAccountView } from "../../lib/account-view"
import { useWorkspaceLayout } from "../../lib/page-layout"

export function AccountNavigation({ onNavigate }: { onNavigate?: () => void }) {
  const t = useTranslations("iam")

  return (
    <nav aria-label={t("accountNav")} className="space-y-1 p-3">
      <NavigationGroup label={t("account")}>
        {personalNavigation.map(({ path, label }) => {
          const Icon =
            path === "/user/mfa"
              ? Smartphone
              : path === "/user/files"
                ? Folder
                : ["/user/tokens", "/user/applications"].includes(path)
                  ? KeyRound
                  : ["/user/security", "/user/closure"].includes(path)
                    ? ShieldCheck
                    : UserRound
          return (
            <NavLink
              key={path}
              to={path}
              onClick={onNavigate}
              className={({ isActive }) =>
                `flex min-h-11 items-center gap-2 rounded-md px-3 py-2 text-sm focus-visible:ring-2 focus-visible:ring-ring ${isActive ? "bg-sidebar-primary font-medium text-sidebar-primary-foreground" : "text-sidebar-foreground hover:bg-sidebar-accent"}`
              }
            >
              <Icon className="size-4 shrink-0" aria-hidden="true" />
              {t(label)}
            </NavLink>
          )
        })}
      </NavigationGroup>
    </nav>
  )
}

export function AccountContent({
  pathname,
  children,
  workspace = false,
}: {
  pathname: string
  children: ReactNode
  workspace?: boolean
}) {
  const className =
    pathname.replace(/\/+$/, "") === "/user/files"
      ? "h-full w-full max-w-none"
      : workspace
        ? "mx-auto flex h-full min-h-0 w-full flex-col p-4 sm:p-6"
        : "mx-auto max-w-5xl p-4 sm:p-6"
  return <div className={className}>{children}</div>
}

export default function AccountLayout() {
  const t = useTranslations("iam")

  const { session, status } = useAuth()
  const location = useLocation()
  const workspaceLayout = useWorkspaceLayout()
  const [open, setOpen] = useState(false)
  if (status === "loading" || status === "error") return <SessionVerification />
  if (!session)
    return (
      <Navigate
        to="/user/login"
        replace
        state={{ from: location.pathname + location.search }}
      />
    )
  return (
    <div className="flex h-svh flex-col bg-background">
      <header className="flex h-14 shrink-0 items-center gap-2 border-b px-4">
        <Sheet open={open} onOpenChange={setOpen}>
          <SheetTrigger
            aria-label={t("openAccountNav")}
            className="grid size-11 place-items-center rounded-md md:hidden"
          >
            <Menu className="size-5" />
          </SheetTrigger>
          <SheetContent side="left" className="w-64 p-0">
            <SheetTitle className="sr-only">{t("accountNav")}</SheetTitle>
            <AccountNavigation onNavigate={() => setOpen(false)} />
          </SheetContent>
        </Sheet>
        <Link
          to="/"
          className="flex min-h-11 items-center gap-2 text-sm hover:underline"
        >
          <ArrowLeft className="size-4" />
          {t("backWorkspace")}
        </Link>
        <div className="ml-auto flex items-center gap-1">
          <LanguageSwitcher />
          <NotificationBell />
            <ThemeModeButton />
          <UserMenu tenantID={getTenant()} />
        </div>
      </header>
      <div className="flex min-h-0 flex-1">
        <aside className="hidden w-64 shrink-0 overflow-y-auto border-e bg-sidebar md:block">
          <AccountNavigation />
        </aside>
        <main
          className={`min-h-0 min-w-0 flex-1 overflow-auto ${workspaceLayout ? "lg:overflow-hidden" : ""}`}
        >
          <AccountContent
            pathname={location.pathname}
            workspace={workspaceLayout}
          >
            <fieldset
              className="contents"
              disabled={
                Boolean(getAccountView()) &&
                (getAccountView()?.mode === "read_only" ||
                  location.pathname !== "/user/files")
              }
            >
              <Outlet key={location.pathname} />
            </fieldset>
          </AccountContent>
        </main>
      </div>
    </div>
  )
}
