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
  permissions: ReadonlySet<string> | null = new Set(["entity_view"]),
  scopedFileEntityID = ""
): string {
  return renderToStaticMarkup(
    <MemoryRouter initialEntries={[path]}>
      <IntlAppProvider initialLocale="zh-CN" initialMessages={{ iam: zh }}>
        <AppSidebar
          tenantID="456"
          permissions={permissions}
          entityID={entityID}
          scopedFileEntityID={scopedFileEntityID}
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
  it("renders access requests in the configured branch without an extra tenant wrapper or duplicate", () => {
    const html = renderSidebar([
      {
        id: "branch",
        label: "Configured access",
        permission_code: "",
        sort_order: 0,
        children: [
          {
            id: "request",
            label: "Configured requests",
            path: "/access-requests",
            permission_code: "access_request_view",
            sort_order: 0,
          },
        ],
      },
    ])
    expect(html).toContain("Configured requests")
    expect(html.match(/href="\/tenants\/456\/access-requests"/g)?.length).toBe(
      1
    )
    expect(html).not.toContain(">当前租户</span>")
  })
  it("does not expose entity management navigation without its tenant permission", () => {
    const html = renderSidebar(
      [],
      false,
      false,
      "/tenants/456/entities/123/overview",
      "123",
      new Set(["todo_view"])
    )
    expect(html).not.toContain('href="/tenants/456/entities/123/')
    expect(renderSidebar([], false, false, "/", "123", null)).not.toContain(
      'href="/tenants/456/entities/123/'
    )
  })
  it("uses entity routes without exposing tenant-wide administration in entity view", () => {
    const html = renderSidebar(
      [
        {
          id: "users",
          label: "Accounts",
          path: "/users",
          permission_code: "user_view",
          sort_order: 1,
        },
      ],
      true,
      true,
      "/tenants/456/entities/123/roles",
      "123"
    )
    expect(html).toContain('href="/tenants/456/entities/123/overview"')
    expect(html).toContain('href="/tenants/456/entities/123/roles"')
    expect(html).not.toContain('href="/tenants/456/entities/123/resources"')
    expect(html).toContain("访问控制")
    expect(html).toContain('href="/tenants/456/entities/123/manage"')
    expect(html).not.toContain('href="/tenants/456/users"')
    expect(html).not.toContain('href="/system/tenants"')
    expect(html).not.toContain('href="/tenants/456/field-policies"')
    expect(html).not.toContain('href="/tenants/456/access-requests"')
  })
  it("exposes entity files only with the file view permission", () => {
    expect(
      renderSidebar(
        [],
        false,
        false,
        "/tenants/456/entities/123/overview",
        "123",
        new Set(["file_view"])
      )
    ).toContain('href="/tenants/456/entities/123/files"')
    expect(
      renderSidebar(
        [],
        false,
        false,
        "/tenants/456/entities/123/overview",
        "123",
        new Set(["entity_view"])
      )
    ).not.toContain('href="/tenants/456/entities/123/files"')
  })
  it("exposes tenant files only with the file view permission", () => {
    expect(
      renderSidebar([], false, false, "/", "", new Set(["file_view"]))
    ).toContain('href="/tenants/456/files"')
    expect(renderSidebar([], false, false, "/")).not.toContain(
      'href="/tenants/456/files"'
    )
  })
  it("links scoped accounts to entity files without exposing tenant files", () => {
    const html = renderSidebar(
      [],
      false,
      false,
      "/tenants/456",
      "",
      new Set(["entity_view"]),
      "123"
    )
    expect(html).toContain('href="/tenants/456/entities/123/files"')
    expect(html).not.toContain('href="/tenants/456/files"')
  })
  it("does not duplicate a tenant file menu supplied by the server", () => {
    const html = renderSidebar(
      [
        {
          id: "files",
          label: "Files",
          path: "/files",
          permission_code: "file_view",
          sort_order: 1,
        },
      ],
      false,
      false,
      "/",
      "",
      new Set(["file_view"])
    )
    expect(html.match(/href="\/tenants\/456\/files"/g)).toHaveLength(1)
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
    expect(renderSidebar([])).not.toContain(
      'href="/tenants/456/field-policies"'
    )
    expect(renderSidebar([], true)).not.toContain(
      'href="/tenants/456/field-policies"'
    )
    expect(renderSidebar([], false, true)).toContain(
      'href="/tenants/456/field-policies"'
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
    expect(html).toContain('href="/tenants/456/access-requests"')
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
            path: "/users",
            permission_code: "user_view",
            sort_order: 10,
          },
          {
            id: "service-accounts",
            label: "Service accounts",
            path: "/service-accounts",
            permission_code: "service_account_view",
            sort_order: 12,
          },
          {
            id: "departments",
            label: "Departments",
            path: "/departments",
            permission_code: "dept_view",
            sort_order: 15,
          },
          {
            id: "entities",
            label: "Entities",
            path: "/entities",
            permission_code: "entity_view",
            sort_order: 12,
          },
          {
            id: "groups",
            label: "Groups",
            path: "/groups",
            permission_code: "group_view",
            sort_order: 19,
          },
          {
            id: "access-reviews",
            label: "Access reviews",
            path: "/access-reviews",
            permission_code: "access_review_view",
            sort_order: 21,
          },
          {
            id: "scim-directories",
            label: "SCIM directories",
            path: "/scim-directories",
            permission_code: "tenant_administer",
            sort_order: 45,
          },
        ],
      },
    ])
    expect(html).toContain("Users")
    expect(html).not.toContain("角色权限")
    expect(html).toContain('href="/tenants/456/users"')
    expect(html).toContain('href="/tenants/456/service-accounts"')
    expect(html).toContain('href="/tenants/456/entities"')
    expect(html).toContain('href="/tenants/456/departments"')
    expect(html).not.toContain('href="/tenants/456/positions"')
    expect(html).toContain('href="/tenants/456/groups"')
    expect(html).toContain('href="/tenants/456/access-reviews"')
    expect(html).toContain('href="/tenants/456/scim-directories"')
  })

  it("keeps platform administration out of the tenant sidebar even for administrators", () => {
    expect(renderSidebar([])).not.toContain("租户管理")
    expect(renderSidebar([])).not.toContain("平台审计")
    const html = renderSidebar([], true)
    expect(html).not.toContain('href="/system/tenants"')
    expect(html).not.toContain('href="/system/audit-governance"')
  })

  it("shows only platform navigation on platform pages, with independent permission gates", () => {
    const html = renderSidebar(
      [
        {
          id: "users",
          label: "Users",
          path: "/users",
          permission_code: "user_view",
          sort_order: 0,
        },
      ],
      true,
      true,
      "/system/tenants",
      "",
      new Set(["platform_administer"])
    )
    expect(html).toContain("租户管理")
    expect(html).toContain('href="/system/tenants"')
    expect(html).toContain('href="/system/audit-governance"')
    expect(html).not.toContain('href="/tenants/456/users"')
    expect(html).not.toContain('href="/tenants/456/field-policies"')
    expect(html).not.toContain('href="/tenants/456/access-requests"')
    expect(
      renderSidebar([], true, false, "/system/tenants", "", new Set())
    ).not.toContain('href="/system/audit-governance"')
    expect(renderSidebar([], false, false, "/tenants")).not.toContain(
      'href="/system/tenants"'
    )
  })

  it("filters platform routes from custom tenant menus, keeping tenant children as directories", () => {
    const html = renderSidebar(
      [
        {
          id: "p",
          label: "Mixed parent",
          path: "/system/audit-governance",
          sort_order: 0,
          permission_code: "",
          children: [
            {
              id: "u",
              label: "Users",
              path: "/users",
              sort_order: 0,
              permission_code: "user_view",
            },
          ],
        },
        {
          id: "t",
          label: "Platform tenants",
          path: "/system/tenants?view=x",
          sort_order: 1,
          permission_code: "",
        },
      ],
      true
    )
    expect(html).toContain('href="/tenants/456/users"')
    expect(html).not.toContain('href="/system/audit-governance"')
    expect(html).not.toContain("Platform tenants")
  })

  it("selects platform audit navigation only in the platform view", () => {
    const tenant = renderSidebar(
      [],
      true,
      false,
      "/tenants/456/audit-governance"
    )
    const platform = renderSidebar(
      [],
      true,
      false,
      "/system/audit-governance",
      "",
      new Set(["platform_administer"])
    )
    expect(tenant).not.toContain('aria-current="page"')
    expect(platform).toContain('aria-current="page"')
  })
})
