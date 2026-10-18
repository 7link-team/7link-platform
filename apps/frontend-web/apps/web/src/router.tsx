import { createBrowserRouter, Navigate, Outlet } from "react-router"
import { AuthProvider } from "./components/auth-provider"
import { PendingRoute } from "./components/pending-route"
import { RouteError } from "./components/route-error"
import { authEntrances, entrancePath } from "./lib/auth-entrance"
import {
  legacyRouteLoader,
  workspaceHomeLoader,
  tenantWorkspaceLoader,
} from "./route-loaders"

export const router = createBrowserRouter([
  {
    element: <Outlet />,
    hydrateFallbackElement: <PendingRoute />,
    errorElement: <RouteError />,
    children: [
      ...(["tenant", "user"] as const).flatMap((entry) => [
        {
          path: entrancePath(entry, "register"),
          lazy: async () => ({
            Component: (await import("./app/register/page")).default,
          }),
        },
        {
          path: entrancePath(entry, "recover"),
          lazy: async () => ({
            Component: (await import("./app/recover/page")).default,
          }),
        },
        {
          path: entrancePath(entry, "verify-email"),
          lazy: async () => ({
            Component: (await import("./app/verify-email/page")).default,
          }),
        },
      ]),
      ...[
        "/login",
        "/login/:entry",
        "/register",
        "/register/:entry",
        "/recover",
        "/recover/:entry",
        "/verify-email",
        "/accept-invitation",
        "/workspaces/:entry",
        "/me",
        "/me/*",
        "/tenants",
        "/tenants/*",
        "/operating-platforms",
        "/operating-platforms/*",
      ].map((path) => ({ path, loader: legacyRouteLoader })),
      {
        path: "/admin/accept-invitation",
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
          ...authEntrances.map((entry) => ({
            path: entrancePath(entry, "login"),
            lazy: async () => ({
              Component: (await import("./app/login/page")).default,
            }),
          })),
          ...authEntrances.map((entry) => ({
            path: entrancePath(entry, "workspaces"),
            lazy: async () => ({
              Component: (await import("./app/login/workspaces")).default,
            }),
          })),
          ...(["tenant", "platform"] as const).map((entry) => ({
            path: entry === "tenant" ? "/admin" : "/platform",
            element: (
              <Navigate to={entrancePath(entry, "workspaces")} replace />
            ),
          })),
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
                handle: { layout: "workspace" },
                lazy: async () => ({
                  Component: (await import("./app/menus/page")).SystemMenusPage,
                }),
              },
              {
                handle: { layout: "workspace" },
                path: "/system/audit/events",
                lazy: async () => ({
                  Component: (await import("./app/audit-events/page"))
                    .SystemAuditEventsPage,
                }),
              },
              {
                handle: { layout: "workspace" },
                path: "/system/departments",
                lazy: async () => ({
                  Component: (await import("./app/departments/page"))
                    .SystemDepartmentsPage,
                }),
              },
              {
                path: "/system/roles",
                handle: { layout: "workspace" },
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
                handle: { layout: "workspace" },
                path: "/system/accounts/system",
                lazy: async () => ({
                  Component: (await import("./app/system/accounts"))
                    .SystemMemberAccountsPage,
                }),
              },
              {
                handle: { layout: "workspace" },
                path: "/system/accounts/platform",
                lazy: async () => ({
                  Component: (await import("./app/system/accounts"))
                    .PlatformAccountsPage,
                }),
              },
              {
                handle: { layout: "workspace" },
                path: "/system/accounts/tenant",
                lazy: async () => ({
                  Component: (await import("./app/system/accounts"))
                    .TenantAccountsPage,
                }),
              },
              {
                handle: { layout: "workspace" },
                path: "/system/accounts/platform/:ownerID",
                lazy: async () => ({
                  Component: (await import("./app/system/owner-members"))
                    .PlatformOwnerMembersPage,
                }),
              },
              {
                handle: { layout: "workspace" },
                path: "/system/accounts/tenant/:ownerID",
                lazy: async () => ({
                  Component: (await import("./app/system/owner-members"))
                    .TenantOwnerMembersPage,
                }),
              },
              {
                handle: { layout: "workspace" },
                path: "/system/accounts",
                lazy: async () => ({
                  Component: (await import("./app/system/accounts")).default,
                }),
              },
              {
                handle: { layout: "workspace" },
                path: "/system/operating-platforms",
                lazy: async () => ({
                  Component: (await import("./app/system/operating-platforms"))
                    .default,
                }),
              },
              {
                handle: { layout: "workspace" },
                path: "/system/tenants",
                lazy: async () => ({
                  Component: (await import("./app/tenants/page")).default,
                }),
              },
              {
                handle: { layout: "workspace" },
                path: "/system/audit-governance",
                lazy: async () => ({
                  Component: (await import("./app/audit-governance/page"))
                    .default,
                }),
              },
              {
                handle: { layout: "workspace" },
                path: "/system/closure-requests",
                lazy: async () => ({
                  Component: (await import("./app/principals/closure-requests"))
                    .default,
                }),
              },
              {
                path: "/platform/:platformID",
                lazy: async () => ({
                  Component: (await import("./app/system/layout"))
                    .AdministrationWorkspaceHome,
                }),
              },
              {
                path: "/platform/:platformID/roles",
                handle: { layout: "workspace" },
                lazy: async () => ({
                  Component: (await import("./app/roles/page")).default,
                }),
              },
              {
                path: "/platform/:platformID/menus",
                handle: { layout: "workspace" },
                lazy: async () => ({
                  Component: (await import("./app/menus/page")).default,
                }),
              },
              {
                handle: { layout: "workspace" },
                path: "/platform/:platformID/departments",
                lazy: async () => ({
                  Component: (await import("./app/departments/page")).default,
                }),
              },
              {
                handle: { layout: "workspace" },
                path: "/platform/:platformID/groups",
                lazy: async () => ({
                  Component: (await import("./app/groups/page")).default,
                }),
              },
              {
                handle: { layout: "workspace" },
                path: "/platform/:platformID/members",
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
            path: "/user",
            lazy: async () => ({
              Component: (await import("./app/account/layout")).default,
            }),
            children: [
              { index: true, element: <Navigate to="files" replace /> },
              {
                handle: { layout: "workspace" },
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
                handle: { layout: "workspace" },
                path: "memberships",
                lazy: async () => ({
                  Component: (await import("./app/account/memberships"))
                    .default,
                }),
              },
              {
                handle: { layout: "workspace" },
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
                handle: { layout: "workspace" },
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
            element: <Navigate to="/user/security" replace />,
          },
          {
            lazy: async () => ({
              Component: (await import("./app/layout")).default,
            }),
            children: [
              {
                path: "/admin/:tenantID/files",
                lazy: async () => ({
                  Component: (await import("./app/files/page")).default,
                }),
              },
              {
                path: "/admin/:tenantID",
                loader: tenantWorkspaceLoader,
                lazy: async () => ({
                  Component: (await import("./app/dashboard/page")).default,
                }),
              },
              {
                handle: { layout: "workspace" },
                path: "/admin/:tenantID/users",
                lazy: async () => ({
                  Component: (await import("./app/principals/page")).default,
                }),
              },
              {
                handle: { layout: "workspace" },
                path: "/admin/:tenantID/invitations",
                lazy: async () => ({
                  Component: (await import("./app/invitations/page")).default,
                }),
              },
              {
                handle: { layout: "workspace" },
                path: "/admin/:tenantID/service-accounts",
                lazy: async () => ({
                  Component: (await import("./app/service-accounts/page"))
                    .default,
                }),
              },
              {
                handle: { layout: "workspace" },
                path: "/admin/:tenantID/entities",
                lazy: async () => ({
                  Component: (await import("./app/entities/page")).default,
                }),
              },
              {
                path: "/admin/:tenantID/entities/:entityID/files",
                lazy: async () => ({
                  Component: (await import("./app/files/page")).default,
                }),
              },
              // The register of departments, reached from inside an entity. It is
              // the same page as the tenant's, which the service narrows to the
              // departments the caller's grant names; it lives here as well so
              // that reaching it does not leave the entity perspective, the way
              // the files view above does not.
              {
                handle: { layout: "workspace" },
                path: "/admin/:tenantID/entities/:entityID/departments",
                lazy: async () => ({
                  Component: (await import("./app/departments/page")).default,
                }),
              },
              {
                handle: { layout: "workspace" },
                path: "/admin/:tenantID/entities/:entityID/:view",
                lazy: async () => ({
                  Component: (await import("./app/entities/workspace")).default,
                }),
              },
              {
                handle: { layout: "workspace" },
                path: "/admin/:tenantID/departments",
                lazy: async () => ({
                  Component: (await import("./app/departments/page")).default,
                }),
              },
              {
                handle: { layout: "workspace" },
                path: "/admin/:tenantID/groups",
                lazy: async () => ({
                  Component: (await import("./app/groups/page")).default,
                }),
              },
              {
                path: "/admin/:tenantID/roles",
                handle: { layout: "workspace" },
                lazy: async () => ({
                  Component: (await import("./app/roles/page")).default,
                }),
              },
              {
                handle: { layout: "workspace" },
                path: "/admin/:tenantID/field-policies",
                lazy: async () => ({
                  Component: (await import("./app/field-policies/page"))
                    .default,
                }),
              },
              {
                handle: { layout: "workspace" },
                path: "/admin/:tenantID/access-requests",
                lazy: async () => ({
                  Component: (await import("./app/access-requests/page"))
                    .default,
                }),
              },
              {
                handle: { layout: "workspace" },
                path: "/admin/:tenantID/access-reviews",
                lazy: async () => ({
                  Component: (await import("./app/access-reviews/page"))
                    .default,
                }),
              },
              {
                path: "/admin/:tenantID/menus",
                handle: { layout: "workspace" },
                lazy: async () => ({
                  Component: (await import("./app/menus/page")).default,
                }),
              },
              {
                handle: { layout: "workspace" },
                path: "/admin/:tenantID/oauth-clients",
                lazy: async () => ({
                  Component: (await import("./app/oauth-clients/page")).default,
                }),
              },
              {
                path: "/admin/:tenantID/device",
                lazy: async () => ({
                  Component: (await import("./app/device/page")).default,
                }),
              },
              {
                handle: { layout: "workspace" },
                path: "/admin/:tenantID/scim-directories",
                lazy: async () => ({
                  Component: (await import("./app/scim-directories/page"))
                    .default,
                }),
              },
              {
                handle: { layout: "workspace" },
                path: "/admin/:tenantID/audit/events",
                lazy: async () => ({
                  Component: (await import("./app/audit-events/page")).default,
                }),
              },
              {
                handle: { layout: "workspace" },
                path: "/admin/:tenantID/audit-governance",
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
