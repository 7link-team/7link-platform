import { describe, expect, it } from "bun:test"
import { renderToStaticMarkup } from "react-dom/server"
import { MemoryRouter } from "react-router"
import type { EffectiveMenu } from "../lib/iam-api"
import { AppSidebar } from "./app-sidebar"

function renderSidebar(
  menus: EffectiveMenu[],
  platformTenantAccess = false,
  fieldPolicyAccess = false,
  path = "/"
): string {
  return renderToStaticMarkup(
    <MemoryRouter initialEntries={[path]}>
      <AppSidebar
        menus={menus}
        platformTenantAccess={platformTenantAccess}
        fieldPolicyAccess={fieldPolicyAccess}
      />
    </MemoryRouter>
  )
}

describe("AppSidebar", () => {
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
    expect(html).toContain("安全中心")
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
    expect(html).toContain("主体与成员")
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

  it("shows tenant management only after platform access is proven", () => {
    expect(renderSidebar([])).not.toContain("租户管理")
    expect(renderSidebar([])).not.toContain("平台审计")
    const html = renderSidebar([], true)
    expect(html).toContain("租户管理")
    expect(html).toContain('href="/iam/tenants"')
    expect(html).toContain('href="/iam/audit-governance?scope=platform"')
  })

  it("selects platform audit navigation only in the platform view", () => {
    const tenant = renderSidebar([], true, false, "/iam/audit-governance")
    const platform = renderSidebar(
      [],
      true,
      false,
      "/iam/audit-governance?scope=platform"
    )
    expect(tenant).not.toContain('aria-current="page"')
    expect(platform).toContain('aria-current="page"')
  })
})
