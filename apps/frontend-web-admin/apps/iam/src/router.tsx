import { createBrowserRouter, Navigate, Outlet, redirect } from "react-router"
import { AuthProvider } from "./components/auth-provider"
import { PendingRoute } from "./components/pending-route"
import { RouteError } from "./components/route-error"
import { getTenant, iamApi, setTenant } from "./lib/iam-api"
import { defaultWorkspacePath } from "./lib/navigation"
import { getAccountView } from "./lib/account-view"

async function workspaceHomeLoader() {
  const view = getAccountView()
  if (view)
    return redirect(
      view.scope_type === "system"
        ? "/system"
        : view.scope_type === "platform"
          ? `/operating-platforms/${encodeURIComponent(view.scope_id)}`
          : view.scope_type === "personal"
            ? "/me/profile"
            : view.scope_type === "entity"
              ? `/tenants/${encodeURIComponent(getTenant())}/entities/${encodeURIComponent(view.scope_id)}/overview`
              : `/tenants/${encodeURIComponent(view.scope_id)}`
    )
  try {
    const [memberships, permissions, platforms, systemPermissions] =
      await Promise.all([
        iamApi.myTenants(),
        iamApi.myPermissions(),
        iamApi.myOperatingPlatforms(),
        iamApi.systemPermissions(),
      ])
    if (systemPermissions.length > 0) return redirect("/system")
    if (
      platforms.length &&
      !memberships.some(
        (tenant) => tenant.status === "active" && tenant.id === getTenant()
      )
    )
      return redirect(
        `/operating-platforms/${encodeURIComponent(platforms[0]!.id)}`
      )
    const tenants = memberships.filter((tenant) => tenant.status === "active")
    const selected = getTenant()
    const tenantID = tenants.some((tenant) => tenant.id === selected)
      ? selected
      : (tenants[0]?.id ?? "")
    if (tenantID) setTenant(tenantID)
    return redirect(defaultWorkspacePath(tenantID, new Set(permissions)))
  } catch {
    return redirect("/me")
  }
}

export const router = createBrowserRouter([
  {
    element: <Outlet />,
    hydrateFallbackElement: <PendingRoute />,
    errorElement: <RouteError />,
    children: [
      {
        path: "/register",
        lazy: async () => ({
          Component: (await import("./app/register/page")).default,
        }),
      },
      {
        path: "/recover",
        lazy: async () => ({
          Component: (await import("./app/recover/page")).default,
        }),
      },
      {
        path: "/verify-email",
        lazy: async () => ({
          Component: (await import("./app/verify-email/page")).default,
        }),
      },
      {
        path: "/accept-invitation",
        lazy: async () => ({
          Component: (await import("./app/accept-invitation/page")).default,
        }),
      },
      {
        element: (
          <AuthProvider>
            <Outlet />
          </AuthProvider>
        ),
        children: [
          {
            lazy: async () => ({
              Component: (await import("./app/system/layout")).default,
            }),
            children: [
              {
                path: "/system",
                lazy: async () => ({
                  Component: (await import("./app/system/layout"))
                    .AdministrationWorkspaceHome,
                }),
              },
              {
                path: "/system/menus",
                lazy: async () => ({
                  Component: (await import("./app/menus/page")).SystemMenusPage,
                }),
              },
              {
                path: "/system/audit/events",
                lazy: async () => ({
                  Component: (await import("./app/audit-events/page"))
                    .SystemAuditEventsPage,
                }),
              },
              {
                path: "/system/departments",
                lazy: async () => ({
                  Component: (await import("./app/departments/page"))
                    .SystemDepartmentsPage,
                }),
              },
              {
                path: "/system/roles",
                lazy: async () => ({
                  Component: (await import("./app/roles/page")).SystemRolesPage,
                }),
              },
              {
                path: "/system/files",
                lazy: async () => ({
                  Component: (await import("./app/files/page")).SystemFilesPage,
                }),
              },
              {
                path: "/system/administrators",
                element: <Navigate to="/system/accounts/system" replace />,
              },
              {
                path: "/system/accounts/system",
                lazy: async () => ({
                  Component: (await import("./app/system/accounts"))
                    .SystemMemberAccountsPage,
                }),
              },
              {
                path: "/system/accounts/platform",
                lazy: async () => ({
                  Component: (await import("./app/system/accounts"))
                    .PlatformAccountsPage,
                }),
              },
              {
                path: "/system/accounts/tenant",
                lazy: async () => ({
                  Component: (await import("./app/system/accounts"))
                    .TenantAccountsPage,
                }),
              },
              {
                path: "/system/accounts/platform/:ownerID",
                lazy: async () => ({
                  Component: (await import("./app/system/owner-members"))
                    .PlatformOwnerMembersPage,
                }),
              },
              {
                path: "/system/accounts/tenant/:ownerID",
                lazy: async () => ({
                  Component: (await import("./app/system/owner-members"))
                    .TenantOwnerMembersPage,
                }),
              },
              {
                path: "/system/accounts",
                lazy: async () => ({
                  Component: (await import("./app/system/accounts")).default,
                }),
              },
              {
                path: "/system/operating-platforms",
                lazy: async () => ({
                  Component: (await import("./app/system/operating-platforms"))
                    .default,
                }),
              },
              {
                path: "/system/tenants",
                lazy: async () => ({
                  Component: (await import("./app/tenants/page")).default,
                }),
              },
              {
                path: "/system/audit-governance",
                lazy: async () => ({
                  Component: (await import("./app/audit-governance/page"))
                    .default,
                }),
              },
              {
                path: "/system/closure-requests",
                lazy: async () => ({
                  Component: (await import("./app/principals/closure-requests"))
                    .default,
                }),
              },
              {
                path: "/operating-platforms/:platformID",
                lazy: async () => ({
                  Component: (await import("./app/system/layout"))
                    .AdministrationWorkspaceHome,
                }),
              },
              {
                path: "/operating-platforms/:platformID/roles",
                lazy: async () => ({
                  Component: (await import("./app/roles/page")).default,
                }),
              },
              {
                path: "/operating-platforms/:platformID/menus",
                lazy: async () => ({
                  Component: (await import("./app/menus/page")).default,
                }),
              },
              {
                path: "/operating-platforms/:platformID/departments",
                lazy: async () => ({
                  Component: (await import("./app/departments/page")).default,
                }),
              },
              {
                path: "/operating-platforms/:platformID/groups",
                lazy: async () => ({
                  Component: (await import("./app/groups/page")).default,
                }),
              },
              {
                path: "/operating-platforms/:platformID/members",
                lazy: async () => ({
                  Component: (await import("./app/system/platform-members"))
                    .default,
                }),
              },
            ],
          },
          {
            index: true,
            loader: workspaceHomeLoader,
          },
          {
            path: "/me",
            lazy: async () => ({
              Component: (await import("./app/account/layout")).default,
            }),
            children: [
              { index: true, element: <Navigate to="profile" replace /> },
              {
                path: "tokens",
                lazy: async () => ({
                  Component: (await import("./app/account/tokens")).default,
                }),
              },
              {
                path: "files",
                lazy: async () => ({
                  Component: (await import("./app/account/files")).default,
                }),
              },
              {
                path: "memberships",
                lazy: async () => ({
                  Component: (await import("./app/account/memberships"))
                    .default,
                }),
              },
              {
                path: "identities",
                lazy: async () => ({
                  Component: (await import("./app/account/identities")).default,
                }),
              },
              {
                path: "closure",
                lazy: async () => ({
                  Component: (await import("./app/account/closure")).default,
                }),
              },
              {
                path: "applications",
                lazy: async () => ({
                  Component: (await import("./app/account/applications"))
                    .default,
                }),
              },
              ...["profile", "security", "mfa"].map((path) => ({
                path,
                lazy: async () => ({
                  Component: (await import("./app/security/page")).default,
                }),
              })),
            ],
          },
          {
            path: "/security",
            element: <Navigate to="/me/security" replace />,
          },
          {
            path: "/login",
            lazy: async () => ({
              Component: (await import("./app/login/page")).default,
            }),
          },
          {
            lazy: async () => ({
              Component: (await import("./app/layout")).default,
            }),
            children: [
              {
                path: "/tenants/:tenantID/files",
                lazy: async () => ({
                  Component: (await import("./app/files/page")).default,
                }),
              },
              {
                path: "/tenants/:tenantID",
                lazy: async () => ({
                  Component: (await import("./app/dashboard/page")).default,
                }),
              },
              {
                path: "/tenants/:tenantID/users",
                lazy: async () => ({
                  Component: (await import("./app/principals/page")).default,
                }),
              },
              {
                path: "/tenants/:tenantID/invitations",
                lazy: async () => ({
                  Component: (await import("./app/invitations/page")).default,
                }),
              },
              {
                path: "/tenants/:tenantID/service-accounts",
                lazy: async () => ({
                  Component: (await import("./app/service-accounts/page"))
                    .default,
                }),
              },
              {
                path: "/tenants/:tenantID/entities",
                lazy: async () => ({
                  Component: (await import("./app/entities/page")).default,
                }),
              },
              {
                path: "/tenants/:tenantID/entities/:entityID/files",
                lazy: async () => ({
                  Component: (await import("./app/files/page")).default,
                }),
              },
              {
                path: "/tenants/:tenantID/entities/:entityID/:view",
                lazy: async () => ({
                  Component: (await import("./app/entities/workspace")).default,
                }),
              },
              {
                path: "/tenants/:tenantID/departments",
                lazy: async () => ({
                  Component: (await import("./app/departments/page")).default,
                }),
              },
              {
                path: "/tenants/:tenantID/groups",
                lazy: async () => ({
                  Component: (await import("./app/groups/page")).default,
                }),
              },
              {
                path: "/tenants/:tenantID/roles",
                lazy: async () => ({
                  Component: (await import("./app/roles/page")).default,
                }),
              },
              {
                path: "/tenants/:tenantID/field-policies",
                lazy: async () => ({
                  Component: (await import("./app/field-policies/page"))
                    .default,
                }),
              },
              {
                path: "/tenants/:tenantID/access-requests",
                lazy: async () => ({
                  Component: (await import("./app/access-requests/page"))
                    .default,
                }),
              },
              {
                path: "/tenants/:tenantID/access-reviews",
                lazy: async () => ({
                  Component: (await import("./app/access-reviews/page"))
                    .default,
                }),
              },
              {
                path: "/tenants/:tenantID/menus",
                lazy: async () => ({
                  Component: (await import("./app/menus/page")).default,
                }),
              },
              {
                path: "/tenants/:tenantID/oauth-clients",
                lazy: async () => ({
                  Component: (await import("./app/oauth-clients/page")).default,
                }),
              },
              {
                path: "/tenants/:tenantID/scim-directories",
                lazy: async () => ({
                  Component: (await import("./app/scim-directories/page"))
                    .default,
                }),
              },
              {
                path: "/tenants/:tenantID/audit/events",
                lazy: async () => ({
                  Component: (await import("./app/audit-events/page")).default,
                }),
              },
              {
                path: "/tenants/:tenantID/audit-governance",
                lazy: async () => ({
                  Component: (await import("./app/audit-governance/page"))
                    .default,
                }),
              },
            ],
          },
        ],
      },
      { path: "*", element: <Navigate to="/" replace /> },
    ],
  },
])
