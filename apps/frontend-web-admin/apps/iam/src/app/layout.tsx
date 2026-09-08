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
import { ThemeModeButton } from "../components/theme-mode-button"
import { UserMenu } from "../components/user-menu"
import {
  getTenant,
  iamApi,
  setTenant,
  type EffectiveMenu,
  type Tenant,
  type Entity,
} from "../lib/iam-api"
import { appName } from "../lib/branding"
import { LanguageSwitcher } from "@workspace/ui/components/i18n/language-switcher"
import { useClientLocale } from "@workspace/ui/i18n/intl-provider"

export default function AppLayout() {
  const t = useTranslations("iam")

  const { session, status } = useAuth()
  const [menus, setMenus] = useState<EffectiveMenu[] | null>(null)
  const [tenantValue, setTenantValue] = useState(getTenant())
  const [platformTenants, setPlatformTenants] = useState<Tenant[] | null>(null)
  const [fieldPolicyAccess, setFieldPolicyAccess] = useState("")
  const { locale: menuLanguage } = useClientLocale()
  const location = useLocation()
  const navigate = useNavigate()
  const entityMatch = useMatch("/iam/entities/:entityID/:view")
  const entityID = entityMatch?.params.entityID ?? ""
  const [entityCatalog, setEntityCatalog] = useState<{
    tenant: string
    items: Entity[]
    error: string
  } | null>(null)
  const catalog = entityCatalog?.tenant === tenantValue ? entityCatalog : null
  const selectedEntity = catalog?.items.find((entity) => entity.id === entityID)

  useEffect(() => {
    if (status !== "authenticated" || !tenantValue) return
    let active = true
    const refresh = () => {
      void iamApi.entities().then(
        (items) => {
          if (active)
            setEntityCatalog({ tenant: tenantValue, items, error: "" })
        },
        (cause: Error) => {
          if (active)
            setEntityCatalog({
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
  }, [status, tenantValue])
  const loginState = useMemo(
    () => ({ from: location.pathname }),
    [location.pathname]
  )

  useEffect(() => {
    if (status !== "authenticated" || !tenantValue || !session) return
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
  }, [status, tenantValue, session])

  useEffect(() => {
    let active = true
    const refresh = () => {
      if (status === "authenticated") {
        void iamApi
          .effectiveMenus()
          .then((next) => {
            if (active) setMenus(next)
          })
          .catch(() => {
            if (active) setMenus([])
          })
      }
    }
    refresh()
    window.addEventListener("menu-catalog-change", refresh)
    return () => {
      active = false
      window.removeEventListener("menu-catalog-change", refresh)
    }
  }, [status, tenantValue])

  useEffect(() => {
    if (status !== "authenticated") return
    const refresh = () => {
      void iamApi
        .tenants()
        .then(setPlatformTenants)
        .catch(() => setPlatformTenants(null))
    }
    refresh()
    window.addEventListener("tenant-catalog-change", refresh)
    return () => window.removeEventListener("tenant-catalog-change", refresh)
  }, [status])

  useEffect(() => {
    const sync = () => {
      const next = getTenant()
      setTenantValue(next)
    }
    window.addEventListener("tenant-change", sync)
    return () => window.removeEventListener("tenant-change", sync)
  }, [])

  if (status === "loading")
    return (
      <div className="grid min-h-svh place-items-center bg-background text-sm text-muted-foreground">
        <div className="grid justify-items-center gap-3">
          <ShieldCheck className="size-8 animate-pulse" />
          {t("verifyingSession")}
        </div>
      </div>
    )
  if (!session) return <Navigate to="/login" replace state={loginState} />

  const commitTenant = (next: string) => {
    if (!next || next === tenantValue) return
    setMenus(null)
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
                menus={menus ?? []}
                platformTenantAccess={platformTenants !== null}
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
            tenants={platformTenants}
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
            {platformTenants?.find((tenant) => tenant.id === tenantValue)
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
          menus={menus ?? []}
          platformTenantAccess={platformTenants !== null}
          fieldPolicyAccess={
            Boolean(tenantValue) &&
            fieldPolicyAccess === `${tenantValue}:${session.subject}`
          }
          className="hidden md:block"
        />
        <main className="min-w-0 flex-1 overflow-auto">
          <div className="mx-auto w-full max-w-[1440px] p-4 sm:p-6">
            {entityID && catalog?.error && <Alert>{catalog.error}</Alert>}
            {entityID && !catalog ? (
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
