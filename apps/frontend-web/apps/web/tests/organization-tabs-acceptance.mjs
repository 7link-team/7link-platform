import assert from "node:assert/strict"
import { join } from "node:path"

export async function organizationTabsAcceptance({
  admin,
  request,
  operator,
  platformA,
  tenantB,
  statement,
  createAccount,
  login,
  contexts,
  directory,
  pass,
}) {
  const page = admin.page
  const tabs = []
  const errors = []
  const records = []
  const members = (id) =>
    statement(
      "SELECT principal_id, status FROM e2e_iam_tenant_members WHERE tenant_id = ? ORDER BY principal_id"
    ).all(BigInt(id))
  const beforePlatform = members(platformA.id)
  const beforeTenant = members(tenantB.id)
  const viewRequest = async (
    view,
    path,
    method = "GET",
    data,
    status = 200
  ) => {
    const result = await page.evaluate(
      async ({ token, path, method, data }) => {
        const response = await fetch(`/api${path}`, {
          method,
          credentials: "omit",
          headers: {
            Authorization: `Bearer ${token}`,
            "Content-Type": "application/json",
          },
          ...(data === undefined ? {} : { body: JSON.stringify(data) }),
        })
        return {
          status: response.status,
          body: await response.json().catch(() => ({})),
        }
      },
      { token: view.token, path, method, data }
    )
    assert.equal(result.status, status, `${method} ${path}`)
    return result.body.data
  }
  const open = async (kind, owner, mode = "read_only") => {
    await page.goto(
      kind === "platform" ? "/system/operating-platforms" : "/system/tenants"
    )
    await page.getByText("Organization management", { exact: true }).waitFor()
    await page
      .getByRole("row")
      .filter({ has: page.getByText(owner.name, { exact: true }) })
      .getByRole("button", {
        name:
          kind === "platform"
            ? `Enter operating platform ${owner.name}`
            : `Open ${owner.name}`,
        exact: true,
      })
      .click()
    const dialog = page.getByRole("dialog")
    if (mode === "read_write") {
      await dialog.locator("#account-view-mode").click()
      await page.getByRole("option", { name: "Writable", exact: true }).click()
    }
    const parentURL = page.url()
    const parentTenant = await page.evaluate(() =>
      sessionStorage.getItem("iam.tenant")
    )
    const [tab, response] = await Promise.all([
      admin.context.waitForEvent("page"),
      page.waitForResponse(
        (response) =>
          new URL(response.url()).pathname === "/api/account-views" &&
          response.request().method() === "POST"
      ),
      dialog.getByRole("button", { name: "Open view", exact: true }).click(),
    ])
    const view = (await response.json()).data
    assert(response.ok())
    tabs.push(tab)
    records.push(view)
    tab.on("pageerror", (error) => errors.push(error.message))
    await tab.waitForURL((url) =>
      url.pathname.startsWith(
        kind === "platform"
          ? `/platform/${owner.id}`
          : `/admin/${owner.id}`
      )
    )
    await tab.waitForLoadState("networkidle")
    assert.equal(page.url(), parentURL)
    assert.equal(
      await page.evaluate(() => sessionStorage.getItem("iam.tenant")),
      parentTenant
    )
    assert.equal(await tab.evaluate(() => window.opener), null)
    assert.equal(await tab.getByRole("alert").count(), 0)
    assert.equal((await viewRequest(view, "/me")).subject, operator.subject)
    const state = await tab.evaluate(async () =>
      (await import("/src/lib/account-view.ts")).getAccountView()
    )
    assert.equal(state.target_type, kind)
    assert.equal(state.scope_id, owner.id)
    assert.equal(state.principal_id, undefined)
    assert.equal(state.mode, mode)
    assert(!JSON.stringify(state).includes(view.token))
    assert.equal(
      await tab.evaluate(
        (token) =>
          [
            JSON.stringify(localStorage),
            JSON.stringify(sessionStorage),
            location.href,
            window.name,
          ].some((text) => text.includes(token)),
        view.token
      ),
      false
    )
    const audit = statement(
      "SELECT principal_id, detail FROM e2e_iam_audit_events WHERE event_type = 'account_view_created' AND target_id = ? ORDER BY id DESC LIMIT 1"
    ).get(BigInt(view.id))
    const detail = JSON.parse(audit.detail)
    assert.equal(String(audit.principal_id), operator.subject)
    assert.equal(detail.view_target_type, kind)
    assert.equal(detail.view_target_id, owner.id)
    assert.equal(detail.viewed_principal_id, undefined)
    return { tab, view }
  }
  try {
    const restricted = await createAccount("organization-view-restricted")
    await request(`/system/administrators/${restricted.id}`, "PUT", {
      full_administrator: false,
      permissions: ["account_view_create"],
    })
    const restrictedLogin = await login(
      restricted.login_name,
      "ViewAcceptance-aA1!2026"
    )
    contexts.push(restrictedLogin.context)
    for (const [kind, owner] of [
      ["platform", platformA],
      ["tenant", tenantB],
    ]) {
      const denied = await restrictedLogin.context.request.post(
        "/api/account-views",
        {
          data: { target_type: kind, scope_type: kind, scope_id: owner.id },
          headers: { Origin: new URL(page.url()).origin },
        }
      )
      assert(
        [401, 403].includes(denied.status()),
        "view creation authority must not confer organization access"
      )
    }
    pass(
      "restricted visitor with view-creation authority cannot issue a view for organizations without its own membership or protected full system authority"
    )
    await page.setViewportSize({ width: 1440, height: 1000 })
    assert.equal(
      (
        await admin.context.request.get(
          `/api/operating-platforms/${platformA.id}/permissions`
        )
      ).status(),
      403
    )
    const platformRead = await open("platform", platformA)
    const tenantRead = await open("tenant", tenantB)
    for (const [entry, kind, owner, other] of [
      [platformRead, "platform", platformA, tenantB],
      [tenantRead, "tenant", tenantB, platformA],
    ]) {
      const prefix =
        kind === "platform"
          ? `/operating-platforms/${owner.id}`
          : `/tenants/${owner.id}`
      await viewRequest(entry.view, `${prefix}/roles`)
      await viewRequest(
        entry.view,
        `${prefix}/roles`,
        "POST",
        { name: "Readonly must fail" },
        403
      )
      await viewRequest(
        entry.view,
        kind === "platform"
          ? `/tenants/${other.id}/roles`
          : `/operating-platforms/${other.id}/roles`,
        "GET",
        undefined,
        403
      )
      await viewRequest(entry.view, "/system/menus", "GET", undefined, 403)
      await entry.tab.locator(`a[href="${kind === "platform" ? "/platform" : "/admin"}/${owner.id}/roles"]`).first().click()
      await entry.tab.waitForLoadState("networkidle")
      assert.equal(
        await entry.tab
          .getByRole("button", { name: "Create role", exact: true })
          .count(),
        0
      )
    }
    pass(
      "nonmember superadministrator enters exact platform and tenant using dedicated readonly tokens; no cookie bypass, owner impersonation or fabricated identity; typed visitor/object audits and cross-scope/write denials"
    )
    const platformWrite = await open("platform", platformA, "read_write")
    const tenantWrite = await open("tenant", tenantB, "read_write")
    for (const [entry, kind, owner] of [
      [platformWrite, "platform", platformA],
      [tenantWrite, "tenant", tenantB],
    ]) {
      const prefix =
        kind === "platform"
          ? `/operating-platforms/${owner.id}`
          : `/tenants/${owner.id}`
      await entry.tab.locator(`a[href="${kind === "platform" ? "/platform" : "/admin"}/${owner.id}/roles"]`).first().click()
      await entry.tab
        .getByRole("button", { name: "Create role", exact: true })
        .click()
      const name = `Organization ${kind} writable role`
      await entry.tab.locator("#role-name").fill(name)
      await entry.tab.locator('button[type="submit"][form="role-form"]').click()
      await entry.tab.getByRole("dialog").waitFor({ state: "hidden" })
      await entry.tab.getByRole("heading", { name, exact: true }).waitFor()
      const audit = statement(
        "SELECT principal_id, detail FROM e2e_iam_audit_events WHERE event_type = 'role_created' AND tenant_id = ? ORDER BY id DESC LIMIT 1"
      ).get(BigInt(owner.id))
      const detail = JSON.parse(audit.detail)
      assert.equal(String(audit.principal_id), operator.subject)
      assert.equal(detail.view_target_type, kind)
      assert.equal(detail.view_target_id, owner.id)
      assert.equal(detail.account_view_id, entry.view.id)
      assert.equal(detail.viewed_principal_id, undefined)
      await viewRequest(
        entry.view,
        `/principals/${operator.subject}/personal-tokens`,
        "POST",
        { name: "Forbidden conversion" },
        403
      )
    }
    assert.deepEqual(members(platformA.id), beforePlatform)
    assert.deepEqual(members(tenantB.id), beforeTenant)
    for (const width of [1440, 390]) {
      for (const [entry, label] of [
        [platformRead, "platform-read"],
        [platformWrite, "platform-write"],
        [tenantRead, "tenant-read"],
        [tenantWrite, "tenant-write"],
      ]) {
        await entry.tab.setViewportSize({
          width,
          height: width === 390 ? 844 : 1000,
        })
        await entry.tab.screenshot({
          path: join(directory, `organization-tabs-${label}-${width}.png`),
          fullPage: true,
        })
      }
    }
    await Promise.all([
      platformRead.tab.waitForEvent("close"),
      platformRead.tab
        .getByRole("button", { name: "Exit view", exact: true })
        .click(),
    ])
    await viewRequest(
      platformRead.view,
      `/operating-platforms/${platformA.id}/roles`,
      "GET",
      undefined,
      401
    )
    await viewRequest(
      platformWrite.view,
      `/operating-platforms/${platformA.id}/roles`
    )
    await viewRequest(tenantWrite.view, `/tenants/${tenantB.id}/roles`)
    await tenantRead.tab.reload()
    await tenantRead.tab
      .getByRole("heading", { name: "Account view closed", exact: true })
      .waitFor()
    assert.deepEqual(errors, [])
    pass(
      "concurrent readonly/writable platform and tenant views preserve visitor identity; real UI writes have typed audits, memberships unchanged, refresh fails closed and exiting one token preserves siblings; desktop/mobile render"
    )
  } finally {
    for (const record of records) {
      await request(`/account-views/${record.id}`, "DELETE").catch(() => {})
      record.token = ""
    }
    for (const tab of tabs) if (!tab.isClosed()) await tab.close()
  }
}
