import { useTranslations } from "@workspace/ui/i18n/intl-provider"
import { useState, type ReactElement } from "react"
import {
  AppWindow,
  Bot,
  Boxes,
  Building2,
  ChevronDown,
  ClipboardCheck,
  FileClock,
  Folder,
  FolderSync,
  KeyRound,
  LayoutDashboard,
  ListChecks,
  MailPlus,
  Network,
  SlidersHorizontal,
  Users,
  UsersRound,
} from "lucide-react"
import { NavLink } from "react-router"
import type { EffectiveMenu } from "../lib/iam-api"
import {
  effectiveMenuPaths,
  menuLabel,
  tenantNavigation,
  tenantSystemNavigation,
  entityNavigation,
} from "../lib/navigation"
import { can, type EffectivePermissions } from "../lib/permissions"

const routeIcons: Record<string, ReactElement> = {
  "/files": <Folder />,
  "/users": <Users />,
  "/invitations": <MailPlus />,
  "/service-accounts": <Bot />,
  "/entities": <Network />,
  "/departments": <Building2 />,
  "/groups": <UsersRound />,
  "/roles": <KeyRound />,
  "/access-reviews": <ListChecks />,
  "/menus": <Boxes />,
  "/oauth-clients": <AppWindow />,
  "/scim-directories": <FolderSync />,
  "/audit/events": <FileClock />,
  "/audit-governance": <FileClock />,
  "/field-policies": <SlidersHorizontal />,
  "/access-requests": <ClipboardCheck />,
}

export function AppSidebar({
  permissions = null,
  menus,
  fieldPolicyAccess = false,
  className = "",
  language = "",
  entityID = "",
  tenantID = "",
}: {
  permissions?: EffectivePermissions
  menus: EffectiveMenu[]
  fieldPolicyAccess?: boolean
  className?: string
  language?: string
  entityID?: string
  tenantID?: string
}) {
  const t = useTranslations("iam")

  const tenantBase = tenantID ? `/admin/${encodeURIComponent(tenantID)}` : ""
  menus = tenantNavigation(menus)
  const paths = effectiveMenuPaths(menus)
  const fileAccess = can(permissions, "file_view")
  const systemLinks = tenantSystemNavigation(paths, fileAccess, fieldPolicyAccess)
  if (entityID) {
    const base = `${tenantBase}/entities/${encodeURIComponent(entityID)}`
    // Taken from what this caller holds in this entity, not from a list of what
    // an entity member is assumed to get.
    //
    // Which makes "not known yet" and "holds nothing" two different things, and
    // reading the first as the second is what made the menu look unstable: a
    // null permission set rendered an empty sidebar for as long as the request
    // took -- three quarters of a second here -- and the links then appeared,
    // which from the outside is the menu changing on its own.
    const entityLinks = permissions ? entityNavigation(permissions) : null
    return (
      <aside
        className={`w-64 shrink-0 overflow-y-auto border-e border-sidebar-border bg-sidebar p-3 ${className}`}
      >
        {/* No group heading: the whole sidebar is this entity's perspective, and
            the switcher above already names which entity. A collapsible group
            labelled after the perspective it is the entirety of is a layer that
            says nothing and can be collapsed to hide everything. */}
        <nav className="space-y-1" aria-label={t("mainNav")}>
          {entityLinks === null && (
            <p role="status" className="px-3 py-2 text-sm text-sidebar-foreground/70">
              {t("loading")}
            </p>
          )}
          {(entityLinks ?? []).map((item) => (
            <SideLink
              key={item.path}
              to={`${base}${item.path}`}
              icon={
                routeIcons[item.path] ??
                (item.path === "/roles" ? (
                  <KeyRound />
                ) : item.path === "/manage" ? (
                  <SlidersHorizontal />
                ) : (
                  <LayoutDashboard />
                ))
              }
              label={t(item.label)}
            />
          ))}
        </nav>
      </aside>
    )
  }
  return (
    <aside
      className={`w-64 shrink-0 overflow-y-auto border-e border-sidebar-border bg-sidebar p-3 ${className}`}
    >
      <nav className="space-y-5" aria-label={t("mainNav")}>
        {menus.length > 0 && (
          <section aria-label={t("tenantMenu")}>
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
          </section>
        )}
        {/* Not a labelled group. Which of the two sources an entry came from --
            the configured menu tree above or the pages that have no menu row --
            is an implementation detail, and a heading that announces it buys a
            collapsible chevron that can hide entries somebody needs. */}
        <div className="space-y-1">
          {systemLinks.map((item) => (
            <SideLink
              key={item.path}
              to={`${tenantBase}${item.path}`}
              icon={routeIcons[item.path] ?? <LayoutDashboard />}
              label={t(item.label)}
            />
          ))}
        </div>
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
  if (["/", "/security"].includes(menu.path ?? "") && children.length === 0)
    return null
  const label = menuLabel(menu, language)
  const icon = routeIcons[menu.path ?? ""] ?? <Folder />
  return (
    <li data-sidebar-menu-id={menu.id}>
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
      end={
        to === "/" ||
        to === "/system/accounts" ||
        /^\/admin\/[^/]+$/u.test(to)
      }
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
