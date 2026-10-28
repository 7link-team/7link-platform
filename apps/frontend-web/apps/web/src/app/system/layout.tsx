import { useWorkspaceLayout } from "../../lib/page-layout"
import { useEffect, useState } from "react"
import {
  NavLink,
  Navigate,
  Outlet,
  useParams,
  Link,
  useLocation,
  useNavigate,
  useOutletContext,
} from "react-router"
import { ArrowRight, ShieldCheck, Menu, UserRound } from "lucide-react"
import { useTranslations, useLocale } from "@workspace/ui/i18n/intl-provider"
import { Button } from "@workspace/ui/components/button"
import {
  Sheet,
  SheetContent,
  SheetTitle,
  SheetTrigger,
} from "@workspace/ui/components/sheet"
import { useAuth } from "../../components/auth"
import { SessionVerification } from "../../components/session-verification"
import { PendingRoute } from "../../components/pending-route"
import { ThemeModeButton } from "../../components/theme-mode-button"
import { LanguageSwitcher } from "@workspace/ui/components/i18n/language-switcher"
import { UserMenu } from "../../components/user-menu"
import { Alert } from "../../components/alert"
import { WorkspaceSwitcher } from "../../components/workspace-switcher"
import {
  iamApi,
  setTenant,
  type EffectiveMenu,
  type Tenant,
} from "../../lib/iam-api"
import { getAccountView } from "../../lib/account-view"
import { menuLabel, firstEffectiveMenuPath } from "../../lib/navigation"
import type { ConsoleOutletContext } from "../../lib/permissions"

export default function AdministrationLayout() {
  const workspaceLayout = useWorkspaceLayout()
  const t = useTranslations("iam")
  const locale = useLocale()
  const { session, status } = useAuth()
  const { platformID = "" } = useParams<{ platformID: string }>()
  const location = useLocation()
  const navigate = useNavigate()
  const [scopeCatalog, setScopeCatalog] = useState<{
    subject: string
    platformID: string
    permissions: ReadonlySet<string> | null
    menus: EffectiveMenu[]
    error: string
  } | null>(null)
  const currentCatalog =
    scopeCatalog?.subject === session?.subject &&
    scopeCatalog?.platformID === platformID
      ? scopeCatalog
      : null
  const permissions = currentCatalog?.permissions ?? null
  const menus = currentCatalog?.menus ?? []
  const error = currentCatalog?.error ?? ""
  const [open, setOpen] = useState(false)
  const [catalog, setCatalog] = useState<{
    tenants: Tenant[]
    platforms: Tenant[]
    system: boolean
  }>({ tenants: [], platforms: [], system: false })
  useEffect(() => {
    if (!session || getAccountView()) return
    let active = true
    void Promise.all([
      iamApi.myTenants(),
      iamApi.myOperatingPlatforms(),
      iamApi.systemPermissions(),
    ]).then(
      ([tenants, platforms, codes]) => {
        if (active)
          setCatalog({
            tenants,
            platforms,
            system: codes.length > 0,
          })
      },
      () => {}
    )
    return () => {
      active = false
    }
  }, [session])
  useEffect(() => {
    if (!session) return
    let active = true
    let generation = 0
    const refresh = () => {
      const request = ++generation
      void Promise.all([
        platformID
          ? iamApi.operatingPermissions(platformID)
          : iamApi.systemPermissions(),
        platformID
          ? iamApi.operatingNavigation(platformID)
          : iamApi.systemNavigation(),
      ]).then(
        ([codes, nextMenus]) => {
          if (active && request === generation) {
            setScopeCatalog({
              subject: session.subject,
              platformID,
              permissions: new Set(codes),
              menus: nextMenus,
              error: "",
            })
          }
        },
        (cause: Error) => {
          if (active && request === generation) {
            setScopeCatalog({
              subject: session.subject,
              platformID,
              permissions: null,
              menus: [],
              error: cause.message,
            })
          }
        }
      )
    }
    refresh()
    window.addEventListener("menu-catalog-change", refresh)
    window.addEventListener("focus", refresh)
    const timer = window.setInterval(refresh, 15_000)
    return () => {
      active = false
      window.clearInterval(timer)
      window.removeEventListener("menu-catalog-change", refresh)
      window.removeEventListener("focus", refresh)
    }
  }, [session, platformID])
  if (status === "loading" || status === "error") return <SessionVerification />
  if (!session)
    return (
      <Navigate
        to={platformID ? "/platform/login" : "/system/login"}
        replace
        state={{ from: location.pathname + location.search }}
      />
    )
  const base = platformID
    ? `/platform/${encodeURIComponent(platformID)}`
    : "/system"
  const renderMenus = (items: EffectiveMenu[]) =>
    items.map((item) => (
      <div key={item.id}>
        {item.path ? (
          <NavLink
            end={item.path === "/system/accounts"}
            onClick={() => setOpen(false)}
            to={
              item.path.startsWith(base)
                ? item.path
                : base + "/" + item.path.replace(/^\//, "")
            }
            className={({ isActive }) =>
              `block rounded-md px-3 py-2 text-sm ${isActive ? "bg-sidebar-accent font-medium" : "hover:bg-sidebar-accent"}`
            }
          >
            {menuLabel(item, locale)}
          </NavLink>
        ) : (
          <div className="px-3 py-2 text-sm font-medium">
            {menuLabel(item, locale)}
          </div>
        )}
        {item.children?.length ? (
          <div className="ms-3 border-s ps-2">{renderMenus(item.children)}</div>
        ) : null}
      </div>
    ))
  const navigation = (
    <nav aria-label={t("mainNav")} className="space-y-1 p-3">
      {renderMenus(menus)}
    </nav>
  )
  const context: ConsoleOutletContext = {
    menus,
    permissions,
    permissionsResolved: permissions !== null,
  }
  return (
    <div className="flex h-svh flex-col bg-background">
      <header className="flex min-h-14 shrink-0 flex-wrap items-center gap-2 border-b px-4 py-2">
        <Sheet open={open} onOpenChange={setOpen}>
          <SheetTrigger
            render={
              <Button
                size="icon"
                variant="outline"
                aria-label={t("openMainNav")}
                className="md:hidden"
              />
            }
          >
            <Menu className="size-4" />
          </SheetTrigger>
          <SheetContent side="left">
            <SheetTitle>
              {t(platformID ? "operatingPlatforms" : "systemAdministration")}
            </SheetTitle>
            {navigation}
          </SheetContent>
        </Sheet>
        <ShieldCheck className="size-5" />
        <Link
          to={
            platformID
              ? `/platform/${encodeURIComponent(platformID)}`
              : "/system"
          }
          className="text-sm font-semibold"
        >
          {t(platformID ? "operatingPlatforms" : "systemAdministration")}
          {platformID && ` · ${platformID}`}
        </Link>
        {!getAccountView() && (
          <WorkspaceSwitcher
            tenantID=""
            entityID=""
            tenants={catalog.tenants}
            entities={[]}
            error=""
            operatingPlatforms={catalog.platforms}
            operatingPlatformID={platformID}
            onOperatingPlatform={(id) =>
              navigate(`/platform/${encodeURIComponent(id)}`)
            }
            systemView={!platformID}
            systemPath={catalog.system ? "/system" : ""}
            onSystem={() => navigate("/system")}
            onPersonal={() => navigate("/user/profile")}
            onTenant={(id) => {
              setTenant(id)
              navigate(`/admin/${encodeURIComponent(id)}`)
            }}
            onEntity={() => {}}
          />
        )}
        <div className="ms-auto flex items-center gap-1">
          {!getAccountView() && (
            <Link to="/user/profile" title={t("account")}>
              <UserRound className="size-4" />
            </Link>
          )}
          <LanguageSwitcher />
          <ThemeModeButton />
          <UserMenu tenantID="" />
        </div>
      </header>
      <div className="flex min-h-0 flex-1">
        <aside className="hidden w-64 shrink-0 overflow-y-auto border-e bg-sidebar md:block">
          {navigation}
        </aside>
        <main
          className={`min-h-0 min-w-0 flex-1 overflow-auto p-4 sm:p-6 ${workspaceLayout ? "lg:flex lg:flex-col lg:overflow-hidden" : ""}`}
        >
          {error && <Alert>{error}</Alert>}
          {currentCatalog ? (
            <Outlet context={context} key={platformID} />
          ) : (
            <PendingRoute />
          )}
        </main>
      </div>
    </div>
  )
}

export function AdministrationWorkspaceHome() {
  const t = useTranslations("iam")
  const locale = useLocale()
  const { platformID = "" } = useParams<{ platformID: string }>()
  const { menus, permissionsResolved } =
    useOutletContext<ConsoleOutletContext>()
  if (!permissionsResolved) return <PendingRoute />
  const path = firstEffectiveMenuPath(menus ?? [])
  if (!path)
    return (
      <p className="text-sm text-muted-foreground">{t("noTokenPermissions")}</p>
    )
  if (!platformID) {
    const actions = new Map<string, EffectiveMenu>()
    const collect = (items: EffectiveMenu[]) => {
      for (const item of items) {
        if (item.path) {
          const destination = item.path.startsWith("/system/")
            ? item.path
            : "/system/" + item.path.replace(/^\//, "")
          if (destination !== "/system/") actions.set(destination, item)
        }
        collect(item.children ?? [])
      }
    }
    collect(menus ?? [])
    const descriptions: Record<string, string> = {
      "/system/accounts": "systemHomeAccounts",
      "/system/accounts/system": "systemHomeAdministrators",
      "/system/tenants": "systemHomeTenants",
      "/system/settings": "systemHomeSettings",
      "/system/audit/events": "systemHomeAudit",
      "/system/jobs": "systemHomeJobs",
      "/system/job-schedules": "systemHomeSchedules",
      "/system/domain-events": "systemHomeEvents",
    }
    return (
      <section className="mx-auto w-full max-w-5xl">
        <header className="border-b pb-5">
          <h1 className="text-2xl font-semibold">{t("systemAdministration")}</h1>
          <p className="mt-2 max-w-2xl text-sm text-muted-foreground">{t("systemHomeDescription")}</p>
        </header>
        <nav aria-label={t("systemHomeActions")} className="mt-4 grid gap-x-8 sm:grid-cols-2">
          {[...actions].map(([destination, item]) => (
            <Link key={destination} to={destination} className="group flex min-h-20 items-center justify-between gap-4 border-b py-4 focus-visible:outline-2 focus-visible:outline-ring">
              <span>
                <span className="font-medium group-hover:text-primary">{menuLabel(item, locale)}</span>
                <span className="mt-1 block text-sm text-muted-foreground">{t(descriptions[destination] ?? "systemHomeManage")}</span>
              </span>
              <ArrowRight className="size-4 shrink-0" aria-hidden="true" />
            </Link>
          ))}
        </nav>
      </section>
    )
  }
  const base = platformID
    ? `/platform/${encodeURIComponent(platformID)}`
    : "/system"
  return (
    <Navigate
      to={path.startsWith(base) ? path : base + "/" + path.replace(/^\//, "")}
      replace
    />
  )
}
