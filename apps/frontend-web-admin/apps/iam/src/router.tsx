import { createBrowserRouter, Navigate, Outlet, redirect } from "react-router"
import { AuthProvider } from "./components/auth-provider"
import { PendingRoute } from "./components/pending-route"
import { RouteError } from "./components/route-error"
import { getTenant, iamApi, setTenant } from "./lib/iam-api"
import { workspacePath } from "./lib/navigation"

async function workspaceHomeLoader() {
  try {
    const tenants = (await iamApi.myTenants()).filter(
      (tenant) => tenant.status === "active"
    )
    const selected = getTenant()
    const tenantID = tenants.some((tenant) => tenant.id === selected)
      ? selected
      : (tenants[0]?.id ?? "")
    if (tenantID) setTenant(tenantID)
    return redirect(workspacePath(tenantID))
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
                path: "/platform/closure-requests",
                lazy: async () => ({
                  Component: (await import("./app/principals/closure-requests"))
                    .default,
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
                path: "/platform/tenants",
                lazy: async () => ({
                  Component: (await import("./app/tenants/page")).default,
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
                path: "/tenants/:tenantID/positions",
                lazy: async () => ({
                  Component: (await import("./app/positions/page")).default,
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
              {
                path: "/platform/audit-governance",
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
