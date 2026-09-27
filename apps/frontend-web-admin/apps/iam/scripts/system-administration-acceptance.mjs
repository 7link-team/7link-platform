import assert from "node:assert/strict"
import { readFile } from "node:fs/promises"
import { join } from "node:path"

export async function systemAdministrationAcceptance({
  admin,
  directory,
  request,
  createAccount,
  systemTarget,
  login,
  contexts,
  eventually,
  pass,
  openUI,
  exitUI,
  viewRequest,
  open,
  statement,
  operator,
}) {
  const page = admin.page
  const client = (method, ...args) =>
    page.evaluate(
      async ({ method, args }) => {
        const { iamApi } = await import("/src/lib/iam-api.ts")
        return iamApi[method](...args)
      },
      { method, args }
    )
  const list = () => request("/system/menus")
  const row = (id) => page.locator(`[data-menu-row-id="${id}"]`)
  const capture = async (
    target,
    name,
    navigationLabel = "Open main navigation"
  ) => {
    for (const width of [1440, 390]) {
      await target.setViewportSize({
        width,
        height: width === 390 ? 844 : 1000,
      })
      await target.waitForLoadState("networkidle")
      await target.locator("table").evaluateAll((tables) => {
        for (const table of tables) table.parentElement.scrollLeft = 0
      })
      assert(
        await target.evaluate(
          () => document.documentElement.scrollWidth <= innerWidth
        ),
        `${name} horizontal overflow`
      )
      await target.screenshot({
        path: join(directory, `${name}-${width}.png`),
        fullPage: true,
        animations: "disabled",
      })
      if (width === 390) {
        await target
          .getByRole("button", { name: navigationLabel, exact: true })
          .click()
        await target.getByRole("dialog").waitFor()
        await target.screenshot({
          path: join(directory, `${name}-${width}-navigation.png`),
          fullPage: true,
          animations: "disabled",
        })
        await target.keyboard.press("Escape")
      }
    }
  }
  const original = await list()
  assert.equal(original.length, 11)
  assert.equal(original.filter((menu) => !menu.parent_id).length, 3)
  const organization = original.find((menu) => menu.label === "Organization")
  assert(organization)
  const created = []
  try {
    await page.setViewportSize({ width: 1440, height: 1000 })
    await page.goto("/system/menus")
    await page
      .getByRole("heading", { name: "System menu management", exact: true })
      .waitFor()
    await page.locator("[data-menu-row-id]").first().waitFor()
    assert.equal(await page.locator("[data-menu-row-id]").count(), 11)
    await page.getByRole("button", { name: "Create menu", exact: true }).click()
    await page.locator("#menu-label").fill("System acceptance branch")
    await page.locator('button[type="submit"][form="menu-form"]').click()
    await page.getByRole("dialog").waitFor({ state: "hidden" })
    const branch = (await list()).find(
      (menu) => menu.label === "System acceptance branch"
    )
    assert(branch)
    created.push(branch.id)
    const leaf = await request("/system/menus", "POST", {
      label: "System audit mirror",
      route: "/system/acceptance/audit-mirror",
      parent_id: branch.id,
      permission_code: "system_audit_event_view",
      sort_order: 10,
      status: "active",
    })
    const sibling = await request("/system/menus", "POST", {
      label: "System accounts mirror",
      route: "/system/acceptance/accounts-mirror",
      parent_id: branch.id,
      permission_code: "system_account_view",
      sort_order: 20,
      status: "active",
    })
    created.push(leaf.id, sibling.id)
    await page.reload()
    await row(leaf.id).waitFor()
    await row(leaf.id)
      .getByRole("button", { name: `Move ${leaf.label} down`, exact: true })
      .click()
    await eventually(async () => {
      const children = (await list()).filter(
        (menu) => menu.parent_id === branch.id
      )
      return children[0]?.id === sibling.id && children[1]?.id === leaf.id
    }, "system menu arrow order persisted")
    await row(branch.id).dragTo(
      page.locator(`[data-menu-tree-id="${organization.id}"]`)
    )
    await eventually(
      async () =>
        (await list()).find((menu) => menu.id === branch.id)?.parent_id ===
        organization.id,
      "system menu subtree drag persisted"
    )
    await page.reload()
    await row(leaf.id).waitFor()
    const navigation = await request("/system/navigation")
    const storedBranch = navigation
      .find((menu) => menu.id === organization.id)
      ?.children?.find((menu) => menu.id === branch.id)
    assert.deepEqual(
      storedBranch.children.map((menu) => menu.id),
      [sibling.id, leaf.id]
    )
    await row(leaf.id)
      .getByRole("button", { name: `Edit ${leaf.label}`, exact: true })
      .click()
    await page.locator("#menu-label").fill("Renamed system audit mirror")
    await page.locator('button[type="submit"][form="menu-form"]').click()
    await page
      .getByRole("navigation", { name: "Main navigation", exact: true })
      .getByRole("link", { name: "Renamed system audit mirror", exact: true })
      .waitFor()
    await row(leaf.id)
      .getByRole("button", {
        name: "Edit Renamed system audit mirror",
        exact: true,
      })
      .click()
    await page.locator("#menu-status").click()
    await page.getByRole("option", { name: "Disable", exact: true }).click()
    await page.locator('button[type="submit"][form="menu-form"]').click()
    await eventually(
      async () =>
        (await list()).find((menu) => menu.id === leaf.id)?.status ===
        "disabled",
      "system menu disable persisted"
    )
    await eventually(
      async () =>
        page
          .getByRole("navigation", { name: "Main navigation", exact: true })
          .getByRole("link", {
            name: "Renamed system audit mirror",
            exact: true,
          })
          .count()
          .then((count) => count === 0),
      "disabled system menu disappears from actual sidebar"
    )
    await capture(page, "system-menu-management")
    await page.setViewportSize({ width: 1440, height: 1000 })
    await page.getByRole("button", { name: "Language", exact: true }).click()
    await page.getByRole("button", { name: /简体中文/ }).click()
    await page
      .getByRole("heading", { name: "系统菜单管理", exact: true })
      .waitFor()
    for (const menu of original.filter((item) => item.route)) {
      const label = menu.translations["zh-CN"]
      assert(label, `${menu.label} persisted Chinese translation`)
      assert(
        (await row(menu.id).locator("td").first().textContent()).includes(label)
      )
      await page
        .getByRole("navigation")
        .getByRole("link", { name: label, exact: true })
        .waitFor()
    }
    await capture(page, "system-menu-management-zh", "打开主导航")
    await page.setViewportSize({ width: 1440, height: 1000 })
    await page.getByRole("button", { name: "语言", exact: true }).click()
    await page.getByRole("button", { name: /English/ }).click()
    await page
      .getByRole("heading", { name: "System menu management", exact: true })
      .waitFor()
    pass(
      "system menu manager and actual sidebar share persisted11-node bootstrap; real create, subtree drag, arrows, rename and disable survive reload"
    )

    const menuViewer = await createAccount("system-menu-viewer")
    const auditViewer = await createAccount("system-audit-viewer")
    await request(`/system/administrators/${menuViewer.id}`, "PUT", {
      full_administrator: false,
      permissions: ["system_menu_view"],
    })
    await request(`/system/administrators/${auditViewer.id}`, "PUT", {
      full_administrator: false,
      permissions: ["system_audit_event_view", "system_audit_event_export"],
    })
    for (const [target, path, heading] of [
      [menuViewer, "/system/menus", "System menu management"],
      [auditViewer, "/system/audit/events", "System audit events"],
    ]) {
      const signed = await login(target.login_name, "ViewAcceptance-aA1!2026")
      contexts.push(signed.context)
      await signed.page.waitForURL((url) => url.pathname === path)
      await signed.page
        .getByRole("heading", { name: heading, exact: true })
        .waitFor()
      assert.equal(
        await signed.page
          .getByRole("link", { name: "Global accounts", exact: true })
          .count(),
        0
      )
      assert.equal(
        await signed.page
          .getByRole("button", { name: "Create menu", exact: true })
          .count(),
        0
      )
      await request("/system/principals", "GET", undefined, 403, signed.context)
      await request(
        "/system/menus",
        "POST",
        { label: "Forbidden", status: "active" },
        403,
        signed.context
      )
      await capture(
        signed.page,
        target === menuViewer
          ? "system-restricted-menu"
          : "system-restricted-audit"
      )
      if (target === auditViewer) {
        await signed.page.setViewportSize({ width: 1440, height: 1000 })
        await signed.page.locator("#audit-event-type").fill("menu_created")
        await signed.page
          .getByRole("button", { name: "Search", exact: true })
          .click()
        await signed.page.waitForLoadState("networkidle")
        const filtered = await signed.page.evaluate(async () => {
          const { iamApi } = await import("/src/lib/iam-api.ts")
          return iamApi.systemAuditEvents({
            event_type: "menu_created",
            limit: 50,
          })
        })
        assert(filtered.items.length >= 3)
        assert(
          filtered.items.every(
            (event) =>
              event.tenant_id === null && event.event_type === "menu_created"
          )
        )
        await signed.page
          .getByRole("button", { name: /View details/ })
          .first()
          .click()
        await signed.page.getByRole("dialog").waitFor()
        await signed.page
          .getByRole("dialog")
          .getByText(/menu_created/)
          .waitFor()
        await signed.page.keyboard.press("Escape")
        const downloaded = signed.page.waitForEvent("download")
        await signed.page
          .getByRole("button", { name: "Export current results", exact: true })
          .click()
        const download = await downloaded
        const exported = await readFile(await download.path(), "utf8")
        assert(exported.includes("menu_created"))
        assert(
          (
            await request(
              "/system/audit/integrity-verifications",
              "GET",
              undefined,
              undefined,
              signed.context
            )
          ).valid
        )
      }
      await signed.page.goto("about:blank")
    }
    pass(
      "menu-only and audit-only global grants derive admitted homes; real system audit filter/detail/export/chain works and account/menu mutation escalation stays denied"
    )

    const readOnly = await open(systemTarget, "system", "1")
    await viewRequest(
      readOnly,
      "/system/menus",
      "POST",
      { label: "Rejected", status: "active" },
      403
    )
    await openUI(systemTarget, /System administration/)
    await page.getByRole("link", { name: "Menus", exact: true }).click()
    await page
      .getByRole("heading", { name: "System menu management", exact: true })
      .waitFor()
    assert.equal(
      await page
        .getByRole("button", { name: "Create menu", exact: true })
        .count(),
      0
    )
    await capture(page, "system-menu-read-only-view")
    assert.equal(
      await page
        .getByRole("columnheader", { name: "Actions", exact: true })
        .count(),
      0
    )
    await exitUI()
    await openUI(systemTarget, /System administration/, true)
    await page.getByRole("link", { name: "Menus", exact: true }).click()
    await page.getByRole("button", { name: "Create menu", exact: true }).click()
    await page.locator("#menu-label").fill("Writable system view branch")
    await page.locator('button[type="submit"][form="menu-form"]').click()
    await eventually(
      async () =>
        (await list()).some(
          (menu) => menu.label === "Writable system view branch"
        ),
      "writable system view menu persisted"
    )
    const writableMenu = (await list()).find(
      (menu) => menu.label === "Writable system view branch"
    )
    created.push(writableMenu.id)
    const event = statement(
      "SELECT principal_id, tenant_id, detail FROM e2e_iam_audit_events WHERE event_type = 'menu_created' AND target_id = ?"
    ).get(writableMenu.id)
    assert.equal(String(event.principal_id), operator.subject)
    assert.equal(String(event.tenant_id), "0")
    assert.equal(JSON.parse(event.detail).viewed_principal_id, systemTarget.id)
    await capture(page, "system-menu-writable-view")
    await exitUI()
    pass(
      "system read-only view has no mutation controls and denies HTTP writes; writable menu UI audit records actual operator and target in system partition"
    )
  } finally {
    for (const id of created.reverse())
      await request(`/system/menus/${id}`, "DELETE").catch(() => {})
  }
}
