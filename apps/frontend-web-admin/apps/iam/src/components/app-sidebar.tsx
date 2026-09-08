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
import { effectiveMenuPaths, menuLabel } from "../lib/navigation"

const routeIcons: Record<string, ReactElement> = {
  "/iam/users": <Users />,
  "/iam/invitations": <MailPlus />,
  "/iam/service-accounts": <Bot />,
  "/iam/entities": <Network />,
  "/iam/departments": <Building2 />,
  "/iam/positions": <BriefcaseBusiness />,
  "/iam/groups": <UsersRound />,
  "/iam/roles": <KeyRound />,
  "/iam/access-reviews": <ListChecks />,
  "/iam/menus": <Boxes />,
  "/iam/oauth-clients": <AppWindow />,
  "/iam/scim-directories": <FolderSync />,
  "/iam/audit-events": <FileClock />,
  "/iam/audit-governance": <FileClock />,
  "/iam/field-policies": <SlidersHorizontal />,
}

export function AppSidebar({
  menus,
  platformTenantAccess = false,
  fieldPolicyAccess = false,
  className = "",
  language = "",
  entityID = "",
}: {
  menus: EffectiveMenu[]
  platformTenantAccess?: boolean
  fieldPolicyAccess?: boolean
  className?: string
  language?: string
  entityID?: string
}) {
  const t = useTranslations("iam")

  const location = useLocation()
  const platformAuditScope =
    new URLSearchParams(location.search).get("scope") === "platform"
  const paths = effectiveMenuPaths(menus)
  if (entityID) {
    const base = `/iam/entities/${encodeURIComponent(entityID)}`
    return (
      <aside
        className={`w-64 shrink-0 overflow-y-auto border-e border-sidebar-border bg-sidebar p-3 ${className}`}
      >
        <nav className="space-y-2" aria-label={t("mainNav")}>
          <h2 className="px-3 py-2 text-xs font-semibold text-muted-foreground">
            {t("entityPerspective")}
          </h2>
          <SideLink
            to={`${base}/overview`}
            icon={<LayoutDashboard />}
            label={t("entityOverview")}
          />
          <SideLink
            to={`${base}/roles`}
            icon={<KeyRound />}
            label={t("entityRoles")}
          />
          <SideLink
            to={`${base}/resources`}
            icon={<Boxes />}
            label={t("entityResources")}
          />
          <SideLink
            to={`${base}/manage`}
            icon={<SlidersHorizontal />}
            label={t("entitySettings")}
          />
        </nav>
      </aside>
    )
  }
  return (
    <aside
      className={`w-64 shrink-0 overflow-y-auto border-e border-sidebar-border bg-sidebar p-3 ${className}`}
    >
      <nav className="space-y-5" aria-label={t("mainNav")}>
        <SideLink to="/" icon={<LayoutDashboard />} label={t("overview")} />
        {platformTenantAccess && (
          <section aria-label={t("platformManagement")}>
            <h2 className="mb-2 px-3 text-xs font-semibold text-muted-foreground">
              {t("platformManagement")}
            </h2>
            <SideLink
              to="/iam/tenants"
              icon={<Landmark />}
              label={t("tenantManagement")}
            />
            <SideLink
              to="/iam/audit-governance?scope=platform"
              icon={<FileClock />}
              label={t("platformAudit")}
              scopeMatches={platformAuditScope}
            />
          </section>
        )}
        {(menus.length > 0 || fieldPolicyAccess) && (
          <section aria-label={t("tenantMenu")}>
            <h2 className="mb-2 px-3 text-xs font-semibold text-muted-foreground">
              {t("currentTenant")}
            </h2>
            <ul className="space-y-1">
              {menus.map((menu) => (
                <MenuBranch key={menu.id} menu={menu} language={language} />
              ))}
            </ul>
            {fieldPolicyAccess && !paths.has("/iam/field-policies") && (
              <SideLink
                to="/iam/field-policies"
                icon={<SlidersHorizontal />}
                label={t("fieldPermissions")}
              />
            )}
          </section>
        )}
        <SideLink
          to="/iam/access-requests"
          icon={<ClipboardCheck />}
          label={t("accessRequests")}
        />
      </nav>
    </aside>
  )
}

function MenuBranch({
  menu,
  language,
}: {
  menu: EffectiveMenu
  language: string
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
    ["/", "/security", "/iam/access-requests"].includes(menu.path ?? "") &&
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
            <SideLink to={menu.path} icon={icon} label={label} />
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
            <MenuBranch key={child.id} menu={child} language={language} />
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
      end={to === "/"}
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
