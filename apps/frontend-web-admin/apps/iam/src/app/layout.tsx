import { useTranslations } from "@workspace/ui/i18n/intl-provider"
import { useEffect, useMemo, useState } from "react"
import { Building2, Menu as MenuIcon, ShieldCheck } from "lucide-react"
import {
  Navigate,
  Outlet,
  useLocation,
  useNavigate,
  useParams,
} from "react-router"
import { Alert } from "../components/alert"
import { WorkspaceSwitcher } from "../components/workspace-switcher"
import { Button } from "@workspace/ui/components/button"
import {
  Sheet,
  SheetContent,
  SheetTitle,
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
} from "../lib/iam-api"
import {
  entityWorkspaceView,
  tenantPerspectiveOffered,
} from "../lib/navigation"
import { useBranding } from "../lib/branding"
import {
  entityIDFromPath,
  tenantIDFromPath,
} from "../lib/organization-scope"
import { entrancePath } from "../lib/auth-entrance"
import { getAccountView } from "../lib/account-view"
import type { EffectivePermissions } from "../lib/permissions"
import { LanguageSwitcher } from "@workspace/ui/components/i18n/language-switcher"
import { useClientLocale } from "@workspace/ui/i18n/intl-provider"

export default function AppLayout() {
  const { name: appName } = useBranding()
  const t = useTranslations("iam")

  const { session, status } = useAuth()
  const accountView = getAccountView()
  const location = useLocation()
  const [menuCatalog, setMenuCatalog] = useState<{
    subject: string
    tenant: string
    items: EffectiveMenu[] | null
    permissions: EffectivePermissions
    error: string
  } | null>(null)
  const routeParams = useParams<{ tenantID?: string; entityID?: string }>()
  // One detector for the addressed tenant, shared with request construction
  // (getTenant), so the shell never shows a tenant requests do not target.
  const routeTenantID = tenantIDFromPath(location.pathname)
  const [selectedTenantValue, setTenantValue] = useState(
    routeTenantID || getTenant()
  )
  const tenantValue = routeTenantID || selectedTenantValue
  const subject = session?.subject ?? ""
  const [tenantCatalog, setTenantCatalog] = useState<{
    subject: string
    items: Pick<Tenant, "id" | "slug" | "name" | "status">[]
    error: string
  } | null>(null)
  const currentTenants =
    tenantCatalog?.subject === subject ? tenantCatalog : null
  const directoryReady = Boolean(currentTenants && !currentTenants.error)
  const availableTenants = currentTenants?.items ?? null
  const routeTenantAdmitted = Boolean(
    routeTenantID &&
    availableTenants?.some((tenant) => tenant.id === routeTenantID)
  )
  // A malformed or inaccessible route tenant goes back to the chooser instead
  // of being shown while another tenant serves the requests.
  const routeTenantRejected =
    routeParams.tenantID !== undefined &&
    (!routeTenantID || (directoryReady && !routeTenantAdmitted))
  const currentMenus =
    menuCatalog?.subject === subject && menuCatalog?.tenant === tenantValue
      ? menuCatalog
      : null
  const menus = currentMenus?.items ?? null
  const permissions = currentMenus?.permissions ?? null
  // The shell loads the menu tree and the caller's own codes together, so one
  // completed load answers both.
  const permissionsResolved = currentMenus !== null
  const menuError = currentMenus?.error ?? ""
  // Field policy access is read from the caller's own permission codes. It
  // used to be discovered by requesting the field catalogue and treating the
  // refusal as the answer: an authorization failure per page load, logged in
  // everybody's console, to learn something the permission set already said.
  const fieldPolicyAccess = Boolean(permissions?.has("field_policy_view"))
  const { locale: menuLanguage } = useClientLocale()
  // The tenant directory is reachable by permission, not by membership: it is
  // where every tenant in the deployment is administered, paged and searched.
  // The switcher is the other thing entirely -- see the loader below.
  const platformTenantAccess = Boolean(permissions?.has("tenant_view"))
  const systemPath = platformTenantAccess
    ? "/system/tenants"
    : permissions?.has("platform_administer")
      ? "/system/audit-governance"
      : ""
  // The navigation panel overlays the page on a small screen, so choosing a
  // destination has to dismiss it; leaving it open hides the page the person
  // just asked for behind the menu they used to ask.
  const [navOpen, setNavOpen] = useState(false)
  const [operatingPlatforms, setOperatingPlatforms] = useState<Tenant[]>([])
  useEffect(() => {
    if (status !== "authenticated" || getAccountView()) return
    let active = true
    void iamApi.myOperatingPlatforms().then(
      (items) => {
        if (active) setOperatingPlatforms(items)
      },
      () => {}
    )
    return () => {
      active = false
    }
  }, [status, subject])

  const navigate = useNavigate()
  const entityID = routeParams.entityID ?? entityIDFromPath(location.pathname)
  // Views that live inside an entity but do not read the entity record itself.
  // The checks below wait for the entity catalogue and refuse an entity that is
  // not in it; these pages need neither, and gating them on it turned a page the
  // account could use into "this entity is unavailable".
  const entityOwnPage =
    Boolean(entityID) &&
    (location.pathname.endsWith("/files") ||
      location.pathname.endsWith("/departments"))
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
  const tenantName = availableTenants?.find(
    (tenant) => tenant.id === tenantValue
  )?.name
  const entityNames = useMemo(
    () =>
      Object.fromEntries(
        (catalog?.items ?? []).map((entity) => [entity.id, entity.name])
      ),
    [catalog]
  )
  // Whether this account has any standing at tenant level, which is what decides
  // whether the tenant's perspective may be offered to it at all. Inside an
  // entity the permission set above is that entity's, so the tenant answer is
  // asked for separately -- and until it arrives the perspective is not offered,
  // because a view is shown once it is known to be allowed rather than until it
  // is known not to be.
  const [tenantStanding, setTenantStanding] = useState<{
    subject: string
    tenant: string
    standing: boolean
  } | null>(null)
  const tenantPerspective = tenantPerspectiveOffered(
    Boolean(entityID),
    tenantStanding?.subject === subject && tenantStanding.tenant === tenantValue
      ? tenantStanding.standing
      : null,
    permissions?.size ?? 0,
    platformTenantAccess || Boolean(systemPath)
  )

  useEffect(() => {
    if (!routeTenantAdmitted) return
    setTenant(routeTenantID)
  }, [routeTenantID, routeTenantAdmitted])

  useEffect(() => {
    if (
      accountView?.scope_type === "entity" ||
      status !== "authenticated" ||
      !tenantValue ||
      !directoryReady ||
      !permissionsResolved
    )
      return
    let active = true
    // No client-side pre-judgement here any more. This used to decide the list
    // was empty whenever the caller lacked entity_view, because the endpoint it
    // called refused without it -- and entity_view is the permission to
    // administer every entity in the tenant, which somebody belonging to one
    // does not have. The endpoint below answers for the caller, so the caller
    // is no longer the one deciding what it may see.
    const refresh = () => {
      // file_view is what the console does inside an entity, and the switcher
      // offers the entities it can take somebody into. Asking for the entities
      // this caller may administer instead was why a member of exactly one
      // entity was shown none of them.
      // The tenant-level answer, asked here because this effect already runs for
      // the same subject and tenant inside an entity as well as outside one. A
      // data-scoped grant is absent from this list unless its constraint admits
      // everything, which is what makes "nothing here" mean "no tenant standing"
      // rather than "no administration permissions".
      void iamApi.myPermissions(tenantValue).then(
        (codes) => {
          if (active)
            setTenantStanding({
              subject,
              tenant: tenantValue,
              standing: (codes ?? []).length > 0,
            })
        },
        () => {
          if (active)
            setTenantStanding({ subject, tenant: tenantValue, standing: false })
        }
      )
      void iamApi.memberEntities("file_view").then(
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
  }, [
    status,
    tenantValue,
    subject,
    directoryReady,
    permissions,
    permissionsResolved,
    accountView?.scope_type,
  ])

  const loginState = useMemo(
    () => ({ from: location.pathname + location.search }),
    [location.pathname, location.search]
  )

  useEffect(() => {
    let active = true
    let generation = 0
    const refresh = () => {
      if (status === "authenticated" && directoryReady) {
        const request = ++generation
        void Promise.allSettled([
          tenantValue && accountView?.scope_type !== "entity"
            ? iamApi.effectiveMenus(tenantValue)
            : Promise.resolve([]),
          iamApi.myPermissions(tenantValue || undefined, entityID || undefined),
        ]).then(([menuResult, permissionResult]) => {
          if (!active || request !== generation) return
          // Failed permission reads remain unknown and protected actions stay
          // unavailable until the caller retries successfully.
          const permissions =
            permissionResult.status === "fulfilled"
              ? new Set(permissionResult.value)
              : null
          setMenuCatalog({
            subject,
            tenant: tenantValue,
            items: menuResult.status === "fulfilled" ? menuResult.value : null,
            permissions,
            error:
              menuResult.status === "rejected"
                ? ((menuResult.reason as Error | undefined)?.message ?? "")
                : permissionResult.status === "rejected"
                  ? ((permissionResult.reason as Error | undefined)?.message ??
                    "")
                  : "",
          })
        })
      }
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
  }, [
    status,
    tenantValue,
    entityID,
    subject,
    directoryReady,
    accountView?.scope_type,
  ])

  useEffect(() => {
    if (status !== "authenticated" || !subject) return
    let active = true
    let generation = 0
    const refresh = () => {
      const request = ++generation
      if (accountView) {
        void iamApi.accountViewWorkspace().then(
          (metadata) => {
            if (!active || request !== generation) return
            const tenant = metadata.tenant
            if (!tenant) {
              setTenantCatalog({
                subject,
                items: [],
                error: t("identityUnavailable"),
              })
              return
            }
            setTenant(tenant.id)
            setTenantValue(tenant.id)
            setTenantCatalog({
              subject,
              items: [tenant],
              error: "",
            })
            if (metadata.scope_type === "entity")
              setEntityCatalog({
                subject,
                tenant: tenant.id,
                items: metadata.entity ? [metadata.entity] : [],
                error: "",
              })
          },
          (cause: Error) => {
            if (active && request === generation)
              setTenantCatalog({
                subject,
                items: [],
                error: cause.message,
              })
          }
        )
        return
      }
      // The switcher offers the tenants this caller belongs to, and only those.
      // It deliberately does not add the tenants a permission merely lets them
      // administer: those are every tenant in the deployment, they would be
      // indistinguishable here from the caller's own, and the switcher has no
      // room to page or search them. A privileged caller reaches another tenant
      // through the account view, which is scoped, time-boxed and can be
      // read-only -- and which hides this switcher while it is active.
      void iamApi.myTenants().then(
        (mine) => {
          if (!active || request !== generation) return
          const items = mine.filter((tenant) => tenant.status === "active")
          const selected = routeTenantID || getTenant()
          const admitted = items.some((tenant) => tenant.id === selected)
          const next = admitted
            ? selected
            : (items.find((tenant) => tenant.id === session?.organization_id)
                ?.id ??
              items[0]?.id ??
              "")
          // A rejected route tenant is redirected, not replaced by a fallback.
          if (admitted || !routeTenantID) {
            if (next !== selected) setTenant(next)
            setTenantValue(next)
          }
          setTenantCatalog({
            subject,
            items,
            error: "",
          })
        },
        (cause: Error) => {
          if (active && request === generation)
            setTenantCatalog({
              subject,
              items: [],
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
  }, [status, subject, session?.organization_id, routeTenantID, accountView, t])

  useEffect(() => {
    const sync = () => {
      const next = getTenant()
      setTenantValue(next)
    }
    window.addEventListener("tenant-change", sync)
    return () => window.removeEventListener("tenant-change", sync)
  }, [])

  if (status === "loading" || status === "error") return <SessionVerification />
  if (!session) return <Navigate to="/admin/login" replace state={loginState} />
  if (routeTenantRejected)
    return <Navigate to={entrancePath("tenant", "workspaces")} replace />

  const commitTenant = (next: string) => {
    if (!next || !availableTenants?.some((tenant) => tenant.id === next)) return
    setMenuCatalog(null)
    setTenant(next)
    setTenantValue(next)
    // The tenant route decides which of its perspectives this caller gets. Not
    // the workspace home: that answers "nowhere was asked for" and sends an
    // operator with system authority to /system, which would bounce somebody who
    // had just picked a tenant straight back out of it.
    navigate(`/admin/${encodeURIComponent(next)}`)
  }

  return (
    <div className="flex h-svh flex-col bg-background">
      <header className="sticky top-0 z-40 border-b bg-background shadow-sm">
        <div className="flex min-h-14 flex-wrap items-center gap-2 px-4 py-2 sm:gap-3">
          <Sheet open={navOpen} onOpenChange={setNavOpen}>
            {/* A Button, not a bare trigger: the shared coarse-pointer rule in
                globals.css grows [data-slot="button"] to 44 px, while the
                icon-lg size keeps the desktop density at its usual 36 px. */}
            <SheetTrigger
              render={
                <Button
                  variant="outline"
                  size="icon-lg"
                  aria-label={t("openMainNav")}
                  className="rounded-md border-input md:hidden"
                />
              }
            >
              <MenuIcon className="size-5" />
            </SheetTrigger>
            <SheetContent side="left" className="w-72 p-0">
              {/* The panel is a dialog, and a dialog needs a name even when the
                  design has no visible heading for it. */}
              <SheetTitle className="sr-only">{t("mainNav")}</SheetTitle>
              {/* Choosing a destination dismisses the panel. Only a link does:
                  the expand and collapse controls inside the tree have to leave
                  it open to be usable at all. */}
              <div
                className="h-full"
                onClick={(event) => {
                  if ((event.target as HTMLElement).closest("a"))
                    setNavOpen(false)
                }}
              >
                <AppSidebar
                  permissions={permissions}
                  tenantID={tenantValue}
                  entityID={entityID}
                  language={menuLanguage}
                  menus={directoryReady ? (menus ?? []) : []}
                  fieldPolicyAccess={Boolean(tenantValue) && fieldPolicyAccess}
                  className="block h-full w-full border-0"
                />
              </div>
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
          {!accountView && (
            <WorkspaceSwitcher
              operatingPlatforms={operatingPlatforms}
              onOperatingPlatform={(id) =>
                navigate(`/platform/${encodeURIComponent(id)}`)
              }
              systemPath={systemPath}
              onSystem={() => navigate(systemPath)}
              onPersonal={() => navigate("/user/profile")}
              tenantID={tenantValue}
              entityID={entityID}
              tenants={availableTenants}
              tenantError={currentTenants?.error ?? ""}
              onRetryTenants={() =>
                window.dispatchEvent(new Event("tenant-catalog-change"))
              }
              tenantPerspective={tenantPerspective}
              entities={catalog?.items ?? null}
              error={entityOwnPage ? "" : (catalog?.error ?? "")}
              onTenant={commitTenant}
              onEntity={(id) => {
                const owner = tenantValue
                // The overview reads the entity itself, which is tenant-wide
                // entity administration. Sending somebody who merely belongs to
                // the entity there turned the whole perspective into one refusal,
                // so they go to the view they do have.
                navigate(
                  id
                    ? `/admin/${encodeURIComponent(owner)}/entities/${encodeURIComponent(id)}/${entityWorkspaceView(permissions ?? [])}`
                    : `/admin/${encodeURIComponent(owner)}`
                )
              }}
            />
          )}
          <div className="ml-auto flex min-w-0 items-center gap-1 sm:gap-2">
            <LanguageSwitcher />
            <ThemeModeButton />
            <UserMenu
              tenantID={directoryReady ? tenantValue : ""}
              platformTenantAccess={platformTenantAccess}
              platformAuditAccess={
                permissions?.has("platform_administer") ?? false
              }
            />
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
          permissions={permissions}
          tenantID={tenantValue}
          entityID={entityID}
          language={menuLanguage}
          menus={directoryReady ? (menus ?? []) : []}
          fieldPolicyAccess={Boolean(tenantValue) && fieldPolicyAccess}
          className="hidden md:block"
        />
        <main className="min-h-0 min-w-0 flex-1 overflow-auto">
          <div
            className={
              location.pathname.endsWith("/files")
                ? "h-full w-full max-w-none"
                : "mx-auto w-full max-w-[1440px] p-4 sm:p-6"
            }
          >
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
            ) : entityID && !entityOwnPage && catalog?.error ? (
              <Alert>{catalog.error}</Alert>
            ) : entityID && !entityOwnPage && !catalog ? (
              <p role="status">{t("loading")}</p>
            ) : entityID &&
              !entityOwnPage &&
              (!selectedEntity || selectedEntity.status !== "active") ? (
              <Alert>{t("entityUnavailable")}</Alert>
            ) : (
              <Outlet
                key={`${tenantValue}:${entityID}`}
                context={{
                  menus,
                  permissions,
                  permissionsResolved,
                  platformTenantAccess,
                  fieldPolicyAccess: Boolean(tenantValue) && fieldPolicyAccess,
                  tenantID: tenantValue,
                  tenantName,
                  entityID,
                  entityNames,
                }}
              />
            )}
          </div>
        </main>
      </div>
    </div>
  )
}
