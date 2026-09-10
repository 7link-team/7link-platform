import { useTranslations } from "@workspace/ui/i18n/intl-provider"
import { useEffect, useMemo, useState } from "react"
import { Building2, Menu as MenuIcon, ShieldCheck } from "lucide-react"
import {
  Navigate,
  Outlet,
  useLocation,
  useNavigate,
  useMatch,
} from "react-router"
import { Alert } from "../components/alert"
import { WorkspaceSwitcher } from "../components/workspace-switcher"
import {
  Sheet,
  SheetContent,
  SheetTrigger,
} from "@workspace/ui/components/sheet"
import { AppSidebar } from "../components/app-sidebar"
import { useAuth } from "../components/auth"
import { SessionVerification } from "../components/session-verification"
import { ThemeModeButton } from "../components/theme-mode-button"
import { UserMenu } from "../components/user-menu"
import {
  getTenant,
  iamApi,
  setTenant,
  type EffectiveMenu,
  type Tenant,
  type Entity,
  ApiError,
} from "../lib/iam-api"
import { appName } from "../lib/branding"
import { LanguageSwitcher } from "@workspace/ui/components/i18n/language-switcher"
import { useClientLocale } from "@workspace/ui/i18n/intl-provider"

export default function AppLayout() {
  const t = useTranslations("iam")

  const { session, status } = useAuth()
  const [menuCatalog, setMenuCatalog] = useState<{
    subject: string
    tenant: string
    items: EffectiveMenu[] | null
    error: string
  } | null>(null)
  const [tenantValue, setTenantValue] = useState(getTenant())
  const subject = session?.subject ?? ""
  const [tenantCatalog, setTenantCatalog] = useState<{
    subject: string
    items: Tenant[]
    platformAccess: boolean
    error: string
  } | null>(null)
  const currentTenants =
    tenantCatalog?.subject === subject ? tenantCatalog : null
  const directoryReady = Boolean(currentTenants && !currentTenants.error)
  const platformTenantAccess =
    directoryReady && Boolean(currentTenants?.platformAccess)
  const availableTenants = currentTenants?.items ?? null
  const currentMenus =
    menuCatalog?.subject === subject && menuCatalog?.tenant === tenantValue
      ? menuCatalog
      : null
  const menus = currentMenus?.items ?? null
  const menuError = currentMenus?.error ?? ""
  const [fieldPolicyAccess, setFieldPolicyAccess] = useState("")
  const { locale: menuLanguage } = useClientLocale()
  const location = useLocation()
  const navigate = useNavigate()
  const entityMatch = useMatch("/iam/entities/:entityID/:view")
  const entityID = entityMatch?.params.entityID ?? ""
  const [entityCatalog, setEntityCatalog] = useState<{
    subject: string
    tenant: string
    items: Entity[]
    error: string
  } | null>(null)
  const catalog =
    directoryReady &&
    entityCatalog?.subject === subject &&
    entityCatalog?.tenant === tenantValue
      ? entityCatalog
      : null
  const selectedEntity = catalog?.items.find((entity) => entity.id === entityID)

  useEffect(() => {
    if (status !== "authenticated" || !tenantValue || !directoryReady) return
    let active = true
    const refresh = () => {
      void iamApi.entities().then(
        (items) => {
          if (active)
            setEntityCatalog({ subject, tenant: tenantValue, items, error: "" })
        },
        (cause: Error) => {
          if (active)
            setEntityCatalog({
              subject,
              tenant: tenantValue,
              items: [],
              error: cause.message,
            })
        }
      )
    }
    refresh()
    window.addEventListener("entity-catalog-change", refresh)
    return () => {
      active = false
      window.removeEventListener("entity-catalog-change", refresh)
    }
  }, [status, tenantValue, subject, directoryReady])
  const loginState = useMemo(
    () => ({ from: location.pathname }),
    [location.pathname]
  )

  useEffect(() => {
    if (
      status !== "authenticated" ||
      !tenantValue ||
      !session ||
      !directoryReady
    )
      return
    let active = true
    void iamApi.fieldCatalog().then(
      () => {
        if (active) setFieldPolicyAccess(`${tenantValue}:${session.subject}`)
      },
      () => {
        if (active) setFieldPolicyAccess("")
      }
    )
    return () => {
      active = false
    }
  }, [status, tenantValue, session, directoryReady])

  useEffect(() => {
    let active = true
    let generation = 0
    const refresh = () => {
      if (status === "authenticated" && directoryReady && tenantValue) {
        const request = ++generation
        void iamApi
          .effectiveMenus()
          .then((next) => {
            if (active && request === generation) {
              setMenuCatalog({
                subject,
                tenant: tenantValue,
                items: next,
                error: "",
              })
            }
          })
          .catch((cause: Error) => {
            if (active && request === generation) {
              setMenuCatalog({
                subject,
                tenant: tenantValue,
                items: null,
                error: cause.message,
              })
            }
          })
      }
    }
    refresh()
    window.addEventListener("menu-catalog-change", refresh)
    return () => {
      active = false
      window.removeEventListener("menu-catalog-change", refresh)
    }
  }, [status, tenantValue, subject, directoryReady])

  useEffect(() => {
    if (status !== "authenticated" || !subject) return
    let active = true
    let generation = 0
    const refresh = () => {
      const request = ++generation
      void Promise.all([
        iamApi.myTenants(),
        iamApi.tenants().catch((cause: unknown) => {
          if (cause instanceof ApiError && cause.status === 403) return null
          throw cause
        }),
      ]).then(
        ([mine, platform]) => {
          if (!active || request !== generation) return
          const items = [
            ...new Map(
              [...mine, ...(platform ?? [])]
                .filter((tenant) => tenant.status === "active")
                .map((tenant) => [tenant.id, tenant])
            ).values(),
          ]
          const selected = getTenant()
          const next = items.some((tenant) => tenant.id === selected)
            ? selected
            : (items.find((tenant) => tenant.id === session?.organization_id)
                ?.id ??
              items[0]?.id ??
              "")
          if (next !== selected) setTenant(next)
          setTenantValue(next)
          setTenantCatalog({
            subject,
            items,
            platformAccess: platform !== null,
            error: "",
          })
        },
        (cause: Error) => {
          if (active && request === generation)
            setTenantCatalog({
              subject,
              items: [],
              platformAccess: false,
              error: cause.message,
            })
        }
      )
    }
    refresh()
    window.addEventListener("tenant-catalog-change", refresh)
    return () => {
      active = false
      window.removeEventListener("tenant-catalog-change", refresh)
    }
  }, [status, subject, session?.organization_id])

  useEffect(() => {
    const sync = () => {
      const next = getTenant()
      setTenantValue(next)
    }
    window.addEventListener("tenant-change", sync)
    return () => window.removeEventListener("tenant-change", sync)
  }, [])

  if (status === "loading" || status === "error") return <SessionVerification />
  if (!session) return <Navigate to="/login" replace state={loginState} />

  const commitTenant = (next: string) => {
    if (
      !next ||
      next === tenantValue ||
      !availableTenants?.some((tenant) => tenant.id === next)
    )
      return
    setMenuCatalog(null)
    setTenant(next)
    setTenantValue(next)
    navigate("/")
  }

  return (
    <div className="flex h-svh flex-col bg-background">
      <header className="sticky top-0 z-40 border-b bg-background shadow-sm">
        <div className="flex min-h-14 flex-wrap items-center gap-2 px-4 py-2 sm:gap-3">
          <Sheet>
            <SheetTrigger
              aria-label={t("openMainNav")}
              className="grid size-9 place-items-center rounded-md border border-input md:hidden"
            >
              <MenuIcon className="size-5" />
            </SheetTrigger>
            <SheetContent side="left" className="w-72 p-0">
              <AppSidebar
                entityID={entityID}
                language={menuLanguage}
                menus={directoryReady ? (menus ?? []) : []}
                platformTenantAccess={platformTenantAccess}
                fieldPolicyAccess={
                  Boolean(tenantValue) &&
                  fieldPolicyAccess === `${tenantValue}:${session.subject}`
                }
                className="block h-full w-full border-0"
              />
            </SheetContent>
          </Sheet>
          <div
            className="flex min-w-0 items-center gap-2 font-semibold"
            aria-label={appName}
          >
            <span className="grid size-8 shrink-0 place-items-center rounded-md bg-primary text-primary-foreground">
              <ShieldCheck className="size-4" />
            </span>
            <span className="hidden truncate lg:inline">{appName}</span>
          </div>
          <WorkspaceSwitcher
            tenantID={tenantValue}
            entityID={entityID}
            tenants={availableTenants}
            tenantError={currentTenants?.error ?? ""}
            onRetryTenants={() =>
              window.dispatchEvent(new Event("tenant-catalog-change"))
            }
            entities={catalog?.items ?? null}
            error={catalog?.error ?? ""}
            onTenant={commitTenant}
            onEntity={(id) =>
              navigate(
                id ? `/iam/entities/${encodeURIComponent(id)}/overview` : "/"
              )
            }
          />
          <div className="ml-auto flex min-w-0 items-center gap-1 sm:gap-2">
            <LanguageSwitcher />
            <ThemeModeButton />
            <UserMenu />
          </div>
        </div>
      </header>
      <div
        className="flex flex-wrap items-center justify-between gap-2 border-b bg-muted/30 px-4 py-2 text-sm"
        aria-label={t("currentWorkspace")}
      >
        <div className="flex min-w-0 flex-wrap items-center gap-2">
          <Building2 className="size-4 shrink-0 text-muted-foreground" />
          <span className="text-muted-foreground">{t("currentTenant")}</span>
          <strong className="break-words">
            {availableTenants?.find((tenant) => tenant.id === tenantValue)
              ?.name ?? (tenantValue ? t("tenantWorkspace") : t("notSelected"))}
          </strong>
          <span className="font-mono text-xs text-muted-foreground">
            {tenantValue}
          </span>
          {entityID && (
            <>
              <span aria-hidden="true">/</span>
              <span>{t("entityPerspective")}</span>
              <strong className="break-all">
                {selectedEntity?.name ?? entityID}
              </strong>
            </>
          )}
        </div>
      </div>
      <div className="flex min-h-0 flex-1">
        <AppSidebar
          entityID={entityID}
          language={menuLanguage}
          menus={directoryReady ? (menus ?? []) : []}
          platformTenantAccess={platformTenantAccess}
          fieldPolicyAccess={
            Boolean(tenantValue) &&
            fieldPolicyAccess === `${tenantValue}:${session.subject}`
          }
          className="hidden md:block"
        />
        <main className="min-w-0 flex-1 overflow-auto">
          <div className="mx-auto w-full max-w-[1440px] p-4 sm:p-6">
            {menuError && directoryReady && (
              <Alert>
                <p>{t.has(menuError) ? t(menuError) : menuError}</p>
                <button
                  type="button"
                  className="min-h-11 underline"
                  onClick={() =>
                    window.dispatchEvent(new Event("menu-catalog-change"))
                  }
                >
                  {t("retryNow")}
                </button>
              </Alert>
            )}
            {currentTenants?.error ? (
              <Alert>{currentTenants.error}</Alert>
            ) : !directoryReady ? (
              <p role="status">{t("loading")}</p>
            ) : !tenantValue ? (
              <p>{t("noAvailableTenants")}</p>
            ) : entityID && catalog?.error ? (
              <Alert>{catalog.error}</Alert>
            ) : entityID && !catalog ? (
              <p role="status">{t("loading")}</p>
            ) : entityID &&
              (!selectedEntity || selectedEntity.status !== "active") ? (
              <Alert>{t("entityUnavailable")}</Alert>
            ) : (
              <Outlet key={`${tenantValue}:${entityID}`} context={{ menus }} />
            )}
          </div>
        </main>
      </div>
    </div>
  )
}
