import { describe, expect, it } from "bun:test"
import { renderToStaticMarkup } from "react-dom/server"
import { MemoryRouter } from "react-router"
import type { EffectiveMenu } from "../lib/iam-api"
import { AppSidebar } from "./app-sidebar"
import { IntlAppProvider } from "@workspace/ui/i18n/intl-provider"
import zh from "../messages/zh-CN.json"

function renderSidebar(
  menus: EffectiveMenu[],
  platformTenantAccess = false,
  fieldPolicyAccess = false,
  path = "/",
  entityID = "",
  permissions: ReadonlySet<string> | null = new Set(["entity_view"])
): string {
  return renderToStaticMarkup(
    <MemoryRouter initialEntries={[path]}>
      <IntlAppProvider initialLocale="zh-CN" initialMessages={{ iam: zh }}>
        <AppSidebar
          permissions={permissions}
          entityID={entityID}
          menus={menus}
          platformTenantAccess={platformTenantAccess}
          platformAuditAccess={permissions?.has("platform_administer") ?? false}
          fieldPolicyAccess={fieldPolicyAccess}
        />
      </IntlAppProvider>
    </MemoryRouter>
  )
}

describe("AppSidebar", () => {
  it("does not expose entity management navigation without its tenant permission", () => {
    const html = renderSidebar(
      [],
      false,
      false,
      "/iam/entities/123/overview",
      "123",
      new Set(["todo_view"])
    )
    expect(html).not.toContain('href="/iam/entities/123/')
    expect(renderSidebar([], false, false, "/", "123", null)).not.toContain(
      'href="/iam/entities/123/'
    )
  })
  it("uses entity routes without exposing tenant-wide administration in entity view", () => {
    const html = renderSidebar(
      [
        {
          id: "users",
          label: "Accounts",
          path: "/iam/users",
          permission_code: "user_view",
          sort_order: 1,
        },
      ],
      true,
      true,
      "/iam/entities/123/roles",
      "123"
    )
    expect(html).toContain('href="/iam/entities/123/overview"')
    expect(html).toContain('href="/iam/entities/123/roles"')
    expect(html).toContain('href="/iam/entities/123/resources"')
    expect(html).toContain('href="/iam/entities/123/manage"')
    expect(html).not.toContain('href="/iam/users"')
    expect(html).not.toContain('href="/iam/tenants"')
    expect(html).not.toContain('href="/iam/field-policies"')
    expect(html).not.toContain('href="/iam/access-requests"')
  })
  it("excludes personal routes even when supplied by the effective menu tree", () => {
    const html = renderSidebar(
      ["/security", "/account", "/account/profile"].map((path, index) => ({
        id: String(index),
        label: "Personal",
        path,
        permission_code: "",
        sort_order: index,
      }))
    )
    expect(html).not.toContain("Personal")
    expect(html).not.toContain('href="/account')
    expect(html).not.toContain('href="/security"')
  })
  it("shows field administration only after its own catalog access is proven", () => {
    expect(renderSidebar([])).not.toContain('href="/iam/field-policies"')
    expect(renderSidebar([], true)).not.toContain('href="/iam/field-policies"')
    expect(renderSidebar([], false, true)).toContain(
      'href="/iam/field-policies"'
    )
  })
  it("keeps self-service navigation without exposing administration", () => {
    const html = renderSidebar([])
    expect(html).toContain("概览")
    expect(html).not.toContain("安全中心")
    expect(html).not.toContain("个人账户")
    expect(html).not.toContain('href="/security"')
    expect(html).not.toContain('href="/account/')
    expect(html).toContain("访问申请")
    expect(html).toContain('href="/iam/access-requests"')
    expect(html).not.toContain("主体与成员")
    expect(html).not.toContain("角色权限")
  })

  it("renders only administration routes present in the effective tree", () => {
    const html = renderSidebar([
      {
        id: "iam",
        label: "IAM",
        permission_code: "",
        sort_order: 0,
        children: [
          {
            id: "users",
            label: "Users",
            path: "/iam/users",
            permission_code: "user_view",
            sort_order: 10,
          },
          {
            id: "service-accounts",
            label: "Service accounts",
            path: "/iam/service-accounts",
            permission_code: "service_account_view",
            sort_order: 12,
          },
          {
            id: "departments",
            label: "Departments",
            path: "/iam/departments",
            permission_code: "dept_view",
            sort_order: 15,
          },
          {
            id: "entities",
            label: "Entities",
            path: "/iam/entities",
            permission_code: "entity_view",
            sort_order: 12,
          },
          {
            id: "positions",
            label: "Positions",
            path: "/iam/positions",
            permission_code: "position_view",
            sort_order: 18,
          },
          {
            id: "groups",
            label: "Groups",
            path: "/iam/groups",
            permission_code: "group_view",
            sort_order: 19,
          },
          {
            id: "access-reviews",
            label: "Access reviews",
            path: "/iam/access-reviews",
            permission_code: "access_review_view",
            sort_order: 21,
          },
          {
            id: "scim-directories",
            label: "SCIM directories",
            path: "/iam/scim-directories",
            permission_code: "tenant_administer",
            sort_order: 45,
          },
        ],
      },
    ])
    expect(html).toContain("Users")
    expect(html).not.toContain("角色权限")
    expect(html).toContain('href="/iam/users"')
    expect(html).toContain('href="/iam/service-accounts"')
    expect(html).toContain('href="/iam/entities"')
    expect(html).toContain('href="/iam/departments"')
    expect(html).toContain('href="/iam/positions"')
    expect(html).toContain('href="/iam/groups"')
    expect(html).toContain('href="/iam/access-reviews"')
    expect(html).toContain('href="/iam/scim-directories"')
  })

  it("keeps platform administration out of the tenant sidebar even for administrators", () => {
    expect(renderSidebar([])).not.toContain("租户管理")
    expect(renderSidebar([])).not.toContain("平台审计")
    const html = renderSidebar([], true)
    expect(html).not.toContain('href="/iam/tenants"')
    expect(html).not.toContain('href="/iam/audit-governance?scope=platform"')
  })

  it("shows only platform navigation on platform pages, with independent permission gates", () => {
    const html = renderSidebar(
      [
        {
          id: "users",
          label: "Users",
          path: "/iam/users",
          permission_code: "user_view",
          sort_order: 0,
        },
      ],
      true,
      true,
      "/iam/tenants",
      "",
      new Set(["platform_administer"])
    )
    expect(html).toContain("租户管理")
    expect(html).toContain('href="/iam/tenants"')
    expect(html).toContain('href="/iam/audit-governance?scope=platform"')
    expect(html).not.toContain('href="/iam/users"')
    expect(html).not.toContain('href="/iam/field-policies"')
    expect(html).not.toContain('href="/iam/access-requests"')
    expect(
      renderSidebar([], true, false, "/iam/tenants", "", new Set())
    ).not.toContain('href="/iam/audit-governance?scope=platform"')
    expect(renderSidebar([], false, false, "/iam/tenants")).not.toContain(
      'href="/iam/tenants"'
    )
  })

  it("filters platform routes from custom tenant menus, keeping tenant children as directories", () => {
    const html = renderSidebar(
      [
        {
          id: "p",
          label: "Mixed parent",
          path: "/iam/audit-governance?scope=platform",
          sort_order: 0,
          permission_code: "",
          children: [
            {
              id: "u",
              label: "Users",
              path: "/iam/users",
              sort_order: 0,
              permission_code: "user_view",
            },
          ],
        },
        {
          id: "t",
          label: "Platform tenants",
          path: "/iam/tenants?view=x",
          sort_order: 1,
          permission_code: "",
        },
      ],
      true
    )
    expect(html).toContain('href="/iam/users"')
    expect(html).not.toContain('href="/iam/audit-governance?scope=platform"')
    expect(html).not.toContain("Platform tenants")
  })

  it("selects platform audit navigation only in the platform view", () => {
    const tenant = renderSidebar([], true, false, "/iam/audit-governance")
    const platform = renderSidebar(
      [],
      true,
      false,
      "/iam/audit-governance?scope=platform",
      "",
      new Set(["platform_administer"])
    )
    expect(tenant).not.toContain('aria-current="page"')
    expect(platform).toContain('aria-current="page"')
  })
})
