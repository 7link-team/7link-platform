import { useTranslations } from "@workspace/ui/i18n/intl-provider"
import { useState, type ReactElement } from "react"
import {
  AppWindow,
  Bot,
  Boxes,
  BriefcaseBusiness,
  Building2,
  ChevronDown,
  ClipboardCheck,
  FileClock,
  Folder,
  FolderSync,
  KeyRound,
  Landmark,
  LayoutDashboard,
  ListChecks,
  MailPlus,
  Network,
  SlidersHorizontal,
  Users,
  UsersRound,
} from "lucide-react"
import { NavLink, useLocation } from "react-router"
import type { EffectiveMenu } from "../lib/iam-api"
import {
  effectiveMenuPaths,
  isPlatformRoute,
  menuLabel,
  tenantNavigation,
} from "../lib/navigation"
import { can, type EffectivePermissions } from "../lib/permissions"

const routeIcons: Record<string, ReactElement> = {
  "/files": <Folder />,
  "/users": <Users />,
  "/invitations": <MailPlus />,
  "/service-accounts": <Bot />,
  "/entities": <Network />,
  "/departments": <Building2 />,
  "/positions": <BriefcaseBusiness />,
  "/groups": <UsersRound />,
  "/roles": <KeyRound />,
  "/access-reviews": <ListChecks />,
  "/menus": <Boxes />,
  "/oauth-clients": <AppWindow />,
  "/scim-directories": <FolderSync />,
  "/audit/events": <FileClock />,
  "/audit-governance": <FileClock />,
  "/field-policies": <SlidersHorizontal />,
}

export function AppSidebar({
  permissions = null,
  menus,
  platformTenantAccess = false,
  platformAuditAccess = false,
  fieldPolicyAccess = false,
  className = "",
  language = "",
  entityID = "",
  scopedFileEntityID = "",
  tenantID = "",
}: {
  permissions?: EffectivePermissions
  menus: EffectiveMenu[]
  platformTenantAccess?: boolean
  platformAuditAccess?: boolean
  fieldPolicyAccess?: boolean
  className?: string
  language?: string
  entityID?: string
  scopedFileEntityID?: string
  tenantID?: string
}) {
  const t = useTranslations("iam")

  const location = useLocation()
  const tenantBase = tenantID
    ? `/tenants/${encodeURIComponent(tenantID)}`
    : ""
  const platformView = isPlatformRoute(location.pathname + location.search)
  menus = tenantNavigation(menus)
  const paths = effectiveMenuPaths(menus)
  const fileAccess = can(permissions, "file_view")
  const scopedFileAccess = Boolean(scopedFileEntityID)
  if (entityID && !platformView) {
    const base = `${tenantBase}/entities/${encodeURIComponent(entityID)}`
    return (
      <aside
        className={`w-64 shrink-0 overflow-y-auto border-e border-sidebar-border bg-sidebar p-3 ${className}`}
      >
        <nav className="space-y-2" aria-label={t("mainNav")}>
          <h2 className="px-3 py-2 text-xs font-semibold text-muted-foreground">
            {t("entityPerspective")}
          </h2>
          {can(permissions, "entity_view") && (
            <SideLink
              to={`${base}/overview`}
              icon={<LayoutDashboard />}
              label={t("entityOverview")}
            />
          )}
          {can(permissions, "entity_view") && (
            <SideLink
              to={`${base}/roles`}
              icon={<KeyRound />}
              label={t("entityRoles")}
            />
          )}
          {can(permissions, "entity_view") && (
            <SideLink
              to={`${base}/resources`}
              icon={<Boxes />}
              label={t("entityResources")}
            />
          )}
          {fileAccess && (
            <SideLink
              to={`${base}/files`}
              icon={<Folder />}
              label={t("files")}
            />
          )}
          {can(permissions, "entity_view") && (
            <SideLink
              to={`${base}/manage`}
              icon={<SlidersHorizontal />}
              label={t("entitySettings")}
            />
          )}
        </nav>
      </aside>
    )
  }
  return (
    <aside
      className={`w-64 shrink-0 overflow-y-auto border-e border-sidebar-border bg-sidebar p-3 ${className}`}
    >
      <nav className="space-y-5" aria-label={t("mainNav")}>
        <SideLink
          to={platformView ? tenantBase || "/me" : tenantBase}
          icon={<LayoutDashboard />}
          label={t(platformView ? "backWorkspace" : "overview")}
        />
        {platformView && (
          <section aria-label={t("platformManagement")}>
            <h2 className="mb-2 px-3 text-xs font-semibold text-muted-foreground">
              {t("platformManagement")}
            </h2>
            {platformTenantAccess && (
              <SideLink
                to="/platform/tenants"
                icon={<Landmark />}
                label={t("tenantManagement")}
              />
            )}
            {platformAuditAccess && (
              <SideLink
                to="/platform/audit-governance"
                icon={<FileClock />}
                label={t("platformAudit")}
              />
            )}
          </section>
        )}
        {!platformView &&
          (menus.length > 0 ||
            fieldPolicyAccess ||
            fileAccess ||
            scopedFileAccess) && (
          <section aria-label={t("tenantMenu")}>
            <h2 className="mb-2 px-3 text-xs font-semibold text-muted-foreground">
              {t("currentTenant")}
            </h2>
            <ul className="space-y-1">
              {menus.map((menu) => (
                <MenuBranch
                  key={menu.id}
                  menu={menu}
                  language={language}
                  tenantBase={tenantBase}
                />
              ))}
            </ul>
            {fileAccess && !paths.has("/files") && (
              <SideLink
                to={`${tenantBase}/files`}
                icon={<Folder />}
                label={t("files")}
              />
            )}
            {!fileAccess && scopedFileAccess && (
              <SideLink
                to={`${tenantBase}/entities/${encodeURIComponent(scopedFileEntityID)}/files`}
                icon={<Folder />}
                label={t("files")}
              />
            )}
            {fieldPolicyAccess && !paths.has("/field-policies") && (
              <SideLink
                to={`${tenantBase}/field-policies`}
                icon={<SlidersHorizontal />}
                label={t("fieldPermissions")}
              />
            )}
          </section>
        )}
        {!platformView && (
          <SideLink
            to={`${tenantBase}/access-requests`}
            icon={<ClipboardCheck />}
            label={t("accessRequests")}
          />
        )}
      </nav>
    </aside>
  )
}

function MenuBranch({
  menu,
  language,
  tenantBase,
}: {
  menu: EffectiveMenu
  language: string
  tenantBase: string
}) {
  const t = useTranslations("iam")

  const [expanded, setExpanded] = useState(true)
  const children = menu.children ?? []
  if (
    menu.path === "/security" ||
    menu.path === "/account" ||
    menu.path?.startsWith("/account/")
  )
    return null
  if (
    ["/", "/security", "/access-requests"].includes(menu.path ?? "") &&
    children.length === 0
  )
    return null
  const label = menuLabel(menu, language)
  const icon = routeIcons[menu.path ?? ""] ?? <Folder />
  return (
    <li>
      <div className="flex min-w-0 items-center">
        {menu.path ? (
          <div className="min-w-0 flex-1">
            <SideLink
              to={`${tenantBase}${menu.path}`}
              icon={icon}
              label={label}
            />
          </div>
        ) : (
          <button
            type="button"
            onClick={() => setExpanded(!expanded)}
            aria-expanded={expanded}
            className="flex min-h-11 min-w-0 flex-1 items-center gap-2 rounded-md px-3 py-2 text-left text-sm font-medium hover:bg-sidebar-accent"
          >
            <span className="shrink-0 [&>svg]:size-4">{icon}</span>
            <span className="break-words">{label}</span>
          </button>
        )}
        {children.length > 0 && (
          <button
            type="button"
            className="grid size-11 shrink-0 place-items-center rounded-md hover:bg-sidebar-accent"
            aria-label={t(expanded ? "collapseNamed" : "expandNamed", {
              name: label,
            })}
            aria-expanded={expanded}
            onClick={() => setExpanded(!expanded)}
          >
            <ChevronDown
              className={`size-4 transition-transform ${expanded ? "" : "-rotate-90"}`}
            />
          </button>
        )}
      </div>
      {children.length > 0 && expanded && (
        <ul className="ms-4 space-y-1 border-s border-sidebar-border ps-2">
          {children.map((child) => (
            <MenuBranch
              key={child.id}
              menu={child}
              language={language}
              tenantBase={tenantBase}
            />
          ))}
        </ul>
      )}
    </li>
  )
}

function SideLink({
  to,
  icon,
  label,
  scopeMatches = true,
}: {
  to: string
  icon: ReactElement
  label: string
  scopeMatches?: boolean
}) {
  return (
    <NavLink
      to={to}
      end={to === "/" || /^\/tenants\/[^/]+$/u.test(to)}
      aria-current={scopeMatches ? undefined : false}
      className={({ isActive }) =>
        `flex min-h-11 items-center gap-2 rounded-md px-3 py-2 text-sm transition-colors ${isActive && scopeMatches ? "sidebar-active-item bg-sidebar-primary font-medium text-sidebar-primary-foreground" : "text-sidebar-foreground/90 hover:bg-sidebar-accent hover:text-sidebar-accent-foreground"}`
      }
    >
      <span className="shrink-0 [&>svg]:size-4" aria-hidden="true">
        {icon}
      </span>
      <span className="min-w-0 break-words">{label}</span>
    </NavLink>
  )
}
