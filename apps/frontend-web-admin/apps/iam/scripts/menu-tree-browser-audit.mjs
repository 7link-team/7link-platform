import assert from "node:assert/strict"
import { readFileSync, mkdirSync, writeFileSync } from "node:fs"
import { join } from "node:path"
import { chromium } from "playwright"

const webURL = process.env.IAM_WEB_URL ?? "http://127.0.0.1:8091"
const directory =
  process.env.IAM_UAT_DIRECTORY ??
  join(import.meta.dirname, "../../../../backend-api/.temp/dev")
const fixture = JSON.parse(
  readFileSync(join(directory, "demo-accounts.json"), "utf8")
)
const output = join(directory, "browser", "menu-tree")
mkdirSync(output, { recursive: true })
const report = {
  status: "RUNNING",
  mockedBusinessResponses: 0,
  operations: [],
  roles: [],
  errors: [],
}
const save = () =>
  writeFileSync(
    join(output, "report.json"),
    JSON.stringify(report, null, 2) + "\n"
  )
const browser = await chromium.launch({ channel: "chrome" })
const contexts = []
const created = []
let admin
let originalMenus = []
async function client(page, method, ...args) {
  return page.evaluate(
    async ({ method, args }) => {
      const { iamApi } = await import("/src/lib/iam-api.ts")
      return iamApi[method](...args)
    },
    { method, args }
  )
}
async function login(account, width = 1440, locale = "en-US") {
  const context = await browser.newContext({
    baseURL: webURL,
    viewport: { width, height: 960 },
  })
  contexts.push(context)
  await context.addCookies([
    { name: "NEXT_LOCALE", value: locale, url: webURL },
  ])
  const page = await context.newPage()
  page.on("pageerror", (error) => report.errors.push(error.message))
  await page.goto("/login")
  await page.locator('form button[role="combobox"]').first().click()
  await page
    .getByRole("option", {
      name: `${account.role} · ${account.loginName}`,
      exact: true,
    })
    .click()
  await page.locator('form button[type="submit"]').click()
  await page.waitForURL((url) => url.pathname !== "/login")
  await page.waitForLoadState("networkidle")
  assert.equal(await page.locator("html").getAttribute("lang"), locale)
  assert.equal(
    new URL(page.url()).pathname,
    ["super-admin", "system-admin"].includes(account.id)
      ? "/system/tenants"
      : `/tenants/${fixture.tenantID}`,
    `${account.id} default workspace`
  )
  return { context, page }
}
const menuPath = `/tenants/${fixture.tenantID}/menus`
const row = (page, id) => page.locator(`[data-menu-row-id="${id}"]`)
const tree = (page, id) => page.locator(`[data-menu-tree-id="${id}"]`)
async function parentIs(page, id, parent) {
  await assertEventually(
    async () =>
      ((await client(page, "menus")).find((menu) => menu.id === id)
        ?.parent_id || "") === parent
  )
}
async function assertEventually(check) {
  for (let i = 0; i < 100; i++) {
    if (await check()) return
    await new Promise((resolve) => setTimeout(resolve, 100))
  }
  assert.fail("menu state did not reach its expected committed value")
}
async function create(page, label, parent = "", route = "") {
  const menu = await client(page, "createMenu", {
    label,
    parent_id: parent,
    route,
    permission_code: route ? "dept_view" : "",
    sort_order: 80000 + created.length,
    status: "active",
  })
  created.push(menu.id)
  return menu
}
try {
  admin = await login(
    fixture.accounts.find((account) => account.id === "super-admin")
  )
  const page = admin.page
  originalMenus = await client(page, "menus")
  const stamp = Date.now()
  const a = await create(page, `Tree A ${stamp}`)
  const b = await create(page, `Tree B ${stamp}`)
  const branch = await create(page, `Branch ${stamp}`, a.id)
  const leaf = await create(
    page,
    `Leaf ${stamp}`,
    branch.id,
    `/departments?tree=${stamp}`
  )
  const movable = await create(page, `Movable ${stamp}`, a.id)
  await create(page, `B child ${stamp}`, b.id)
  await page.goto(menuPath)
  await row(page, leaf.id).waitFor()
  assert.equal(await tree(page, leaf.id).count(), 0, "left tree omits leaves")
  assert.equal(
    await tree(page, movable.id).count(),
    0,
    "movable leaf stays in right list"
  )
  assert(await tree(page, branch.id).isVisible())
  assert.equal(
    await page.locator("[data-menu-row-id]").count(),
    (await client(page, "menus")).length
  )
  await row(page, branch.id)
    .getByRole("button", { name: `Collapse ${branch.label}`, exact: true })
    .click()
  assert.equal(await row(page, leaf.id).count(), 0)
  assert(await row(page, branch.id).isVisible())
  await row(page, branch.id)
    .getByRole("button", { name: `Expand ${branch.label}`, exact: true })
    .click()
  await row(page, leaf.id).waitFor()
  await tree(page, a.id)
    .getByRole("button", { name: a.label, exact: true })
    .click()
  assert.equal(await page.locator("[data-menu-row-id]").count(), 2)
  await row(page, branch.id)
    .getByRole("button", { name: `Expand ${branch.label}`, exact: true })
    .click()
  await row(page, leaf.id).waitFor()
  assert.equal(await page.locator("[data-menu-row-id]").count(), 3)
  assert.equal(
    await tree(page, a.id).getAttribute("aria-selected"),
    "true",
    "right expansion preserves left directory selection"
  )
  await row(page, branch.id)
    .getByRole("button", { name: `Collapse ${branch.label}`, exact: true })
    .click()
  assert.equal(await row(page, leaf.id).count(), 0)
  await tree(page, branch.id)
    .getByRole("button", { name: branch.label, exact: true })
    .click()
  assert.equal(await page.locator("[data-menu-row-id]").count(), 1)
  assert(await row(page, leaf.id).isVisible())
  await page.locator("[data-menu-root]").click()
  await row(page, movable.id)
    .getByRole("button", { name: `Move ${movable.label} up`, exact: true })
    .click()
  await parentIs(page, movable.id, branch.id)
  await row(page, movable.id)
    .getByRole("button", { name: `Move ${movable.label} down`, exact: true })
    .click()
  await parentIs(page, movable.id, a.id)
  await row(page, branch.id)
    .getByRole("button", { name: `Move ${branch.label} down`, exact: true })
    .click()
  await row(page, branch.id)
    .getByRole("button", { name: `Move ${branch.label} down`, exact: true })
    .click()
  await parentIs(page, branch.id, "")
  await row(page, branch.id)
    .getByRole("button", { name: `Move ${branch.label} down`, exact: true })
    .click()
  await parentIs(page, branch.id, b.id)
  await row(page, branch.id)
    .getByRole("button", { name: `Move ${branch.label} up`, exact: true })
    .click()
  await parentIs(page, branch.id, "")
  await parentIs(page, leaf.id, branch.id)
  report.operations.push(
    "root lists all descendants; branch initially lists direct children; right rows expand/collapse in place without changing left selection; up/down enter branches and exit parents for leaves and whole subtrees"
  )

  await row(page, branch.id).dragTo(tree(page, a.id))
  await parentIs(page, branch.id, a.id)
  await row(page, movable.id).dragTo(tree(page, b.id), {
    targetPosition: { x: 80, y: 2 },
  })
  await parentIs(page, movable.id, "")
  const ordered = (await client(page, "menus")).filter(
    (menu) => !menu.parent_id
  )
  assert.equal(
    ordered.findIndex((menu) => menu.id === movable.id) + 1,
    ordered.findIndex((menu) => menu.id === b.id)
  )
  await row(page, movable.id).dragTo(row(page, leaf.id))
  await parentIs(page, movable.id, leaf.id)
  await tree(page, leaf.id).waitFor()
  const rejected = await admin.context.request.patch(
    `/api/menus/${branch.id}`,
    {
      headers: { Origin: webURL },
      data: { parent_id: leaf.id, placement: { kind: "last" } },
    }
  )
  assert.equal(rejected.status(), 422)
  await row(page, branch.id).dragTo(tree(page, leaf.id))
  await parentIs(page, branch.id, a.id)
  await row(page, branch.id).dragTo(page.locator("[data-menu-root]"))
  await parentIs(page, branch.id, "")
  await page.reload()
  await row(page, movable.id).waitFor()
  await parentIs(page, movable.id, leaf.id)
  await parentIs(page, leaf.id, branch.id)
  report.operations.push(
    "left tree shows branches only; right-list leaf drop creates a branch; real mouse drag inside/before/root reparents leaves and subtrees; descendant cycle denied; reload preserves committed tree and order"
  )

  await tree(page, b.id)
    .getByRole("button", { name: b.label, exact: true })
    .click()
  await page.getByRole("button", { name: "Create menu", exact: true }).click()
  const childLabel = `Created in branch ${stamp}`
  await page.locator("#menu-label").fill(childLabel)
  await page.locator('button[form="menu-form"][type="submit"]').click()
  await page.locator("#menu-form").waitFor({ state: "hidden" })
  const uiChild = (await client(page, "menus")).find(
    (menu) => menu.label === childLabel
  )
  assert(uiChild)
  created.push(uiChild.id)
  assert.equal(uiChild.parent_id, b.id)
  report.operations.push(
    "new menu inherits selected branch through actual form"
  )

  // A retained child keeps this destination a branch during button round trips.
  await create(page, `Leaf sibling ${stamp}`, leaf.id)
  for (const account of fixture.accounts) {
    for (const width of [1440, 390]) {
      const { page: rolePage, context } = await login(account, width)
      const result = { role: account.id, width, status: "RUNNING", views: [] }
      report.roles.push(result)
      const openNavigation = async (accountView = false) => {
        if (width === 390)
          await rolePage
            .getByRole("button", {
              name: accountView
                ? "Open account navigation"
                : "Open main navigation",
              exact: true,
            })
            .click()
        const group = rolePage
          .locator("[data-navigation-group]")
          .filter({ visible: true })
          .first()
        await group.waitFor()
        const button = group.getByRole("button").first()
        assert.equal(await button.getAttribute("aria-expanded"), "true")
        assert(
          (await group.locator("[data-navigation-children] a").count()) > 0
        )
        await button.click()
        assert.equal(
          await group.locator("[data-navigation-children]").count(),
          0
        )
        await button.click()
        await rolePage.screenshot({
          path: join(
            output,
            `${account.id}-${width}-${result.views.length}.png`
          ),
        })
      }
      await rolePage.goto(`/tenants/${fixture.tenantID}`)
      await rolePage.waitForLoadState("networkidle")
      await openNavigation()
      const navigation = rolePage.getByRole("navigation", {
        name: "Main navigation",
        exact: true,
      })
      assert.equal(
        await navigation
          .getByRole("button", { name: "Current tenant", exact: true })
          .count(),
        0
      )
      const effective = await client(
        rolePage,
        "effectiveMenus",
        fixture.tenantID
      )
      const checkConfigured = async (nodes, parent = null) => {
        for (const menu of nodes) {
          const node = navigation.locator(`[data-sidebar-menu-id="${menu.id}"]`)
          if (
            menu.path &&
            !menu.path.startsWith("/platform") &&
            !["/", "/security"].includes(menu.path)
          ) {
            assert.equal(
              await node
                .locator(`a[href="/tenants/${fixture.tenantID}${menu.path}"]`)
                .count(),
              1
            )
            if (parent)
              assert.equal(
                await navigation
                  .locator(
                    `[data-sidebar-menu-id="${parent}"] [data-sidebar-menu-id="${menu.id}"]`
                  )
                  .count(),
                1
              )
          }
          await checkConfigured(menu.children ?? [], menu.id)
        }
      }
      await checkConfigured(effective)
      const canManage = ["super-admin", "tenant-admin"].includes(account.id)
      assert.equal(
        await rolePage
          .locator(`aside a[href="${menuPath}"]`)
          .filter({ visible: true })
          .count(),
        canManage ? 1 : 0,
        `${account.id} menu management link visibility`
      )
      result.views.push(
        "tenant group collapses/expands with authorized child links"
      )
      if (width === 390) await rolePage.keyboard.press("Escape")
      await rolePage
        .getByRole("button", { name: "Switch workspace", exact: true })
        .click()
      const switcher = rolePage.locator("[data-workspace-switcher]")
      const platformButton = switcher.getByRole("button", {
        name: "Platform administration",
        exact: true,
      })
      assert.equal(
        await platformButton.count(),
        ["super-admin", "system-admin"].includes(account.id) ? 1 : 0
      )
      await rolePage.keyboard.press("Escape")
      await rolePage.goto(menuPath)
      await rolePage.waitForLoadState("networkidle")
      if (canManage) {
        await row(rolePage, movable.id).waitFor()
        await row(rolePage, movable.id)
          .getByRole("button", {
            name: `Move ${movable.label} up`,
            exact: true,
          })
          .click()
        await parentIs(rolePage, movable.id, branch.id)
        await row(rolePage, movable.id)
          .getByRole("button", {
            name: `Move ${movable.label} down`,
            exact: true,
          })
          .click()
        await parentIs(rolePage, movable.id, leaf.id)
        await rolePage.reload()
        await row(rolePage, movable.id).waitFor()
        assert(
          await rolePage
            .getByRole("tree", { name: "Menu tree", exact: true })
            .isVisible()
        )
        assert(
          await rolePage.evaluate(
            () => document.documentElement.scrollWidth <= window.innerWidth
          ),
          `${account.id} menu manager overflow`
        )
        await rolePage.screenshot({
          path: join(output, `${account.id}-${width}-menu-manager.png`),
        })
        result.views.push("menu tree + cross-level buttons + persisted reload")
        assert.equal(
          await rolePage
            .getByRole("region", { name: "System entries", exact: true })
            .count(),
          0
        )
        assert.equal(
          await rolePage.locator("[data-system-navigation-path]").count(),
          0
        )
      } else {
        assert.equal(await rolePage.getByRole("tree").count(), 0)
        const response = await context.request.patch(
          `/api/menus/${branch.id}`,
          {
            headers: { Origin: webURL },
            data: { parent_id: "", placement: { kind: "last" } },
          }
        )
        assert.equal(response.status(), 403)
        result.views.push("management tree absent; real mutation forbidden")
      }
      if (["super-admin", "system-admin"].includes(account.id)) {
        await rolePage
          .getByRole("button", { name: "Switch workspace", exact: true })
          .click()
        await rolePage
          .locator("[data-workspace-switcher]")
          .getByRole("button", { name: "Platform administration", exact: true })
          .click()
        await rolePage.waitForURL("**/system/tenants")
        await rolePage.waitForLoadState("networkidle")
        assert.equal(new URL(rolePage.url()).pathname, "/system/tenants")
        await openNavigation()
        const platformNav = rolePage.getByRole("navigation", {
          name: "Main navigation",
          exact: true,
        })
        assert.equal(
          await platformNav
            .locator('a[href="/system/closure-requests"]')
            .count(),
          account.id === "super-admin" ? 1 : 0
        )
        if (account.id === "super-admin") {
          for (const target of [
            {
              path: "/system/audit-governance",
              api: "/system/audit/archive-status",
            },
            {
              path: "/system/closure-requests",
              api: "/system/closure-requests",
            },
          ]) {
            const response = rolePage.waitForResponse(
              (response) =>
                new URL(response.url()).pathname.endsWith(target.api) &&
                response.request().method() === "GET"
            )
            await rolePage
              .getByRole("navigation", { name: "Main navigation", exact: true })
              .locator(`a[href="${target.path}"]`)
              .click()
            await rolePage.waitForURL(`**${target.path}`)
            assert.equal((await response).status(), 200)
            await rolePage.waitForLoadState("networkidle")
            if (target.path === "/system/audit-governance") {
              await rolePage
                .getByText("Retention policy", { exact: true })
                .waitFor()
            } else {
              await rolePage
                .locator("main")
                .getByRole("status")
                .waitFor({ state: "hidden" })
            }
            assert.equal(
              await rolePage.locator("main").getByRole("alert").count(),
              0
            )
            await rolePage
              .locator(`aside a[href="${target.path}"][aria-current="page"]`)
              .first()
              .waitFor({ state: "attached" })
            if (width === 390)
              await rolePage
                .getByRole("dialog", { name: "Main navigation", exact: true })
                .waitFor({ state: "hidden" })
            await rolePage.screenshot({
              animations: "disabled",
              path: join(
                output,
                `${account.id}-${width}-${target.path.split("/").at(-1)}.png`
              ),
            })
            if (width === 390)
              await rolePage
                .getByRole("button", {
                  name: "Open main navigation",
                  exact: true,
                })
                .click()
          }
          result.views.push(
            "platform audit and closure pages reached from sidebar with real HTTP200"
          )
        } else {
          assert.equal(
            (
              await context.request.get("/api/system/closure-requests")
            ).status(),
            403
          )
        }
        if (width === 390) await rolePage.keyboard.press("Escape")
        await rolePage
          .getByRole("button", { name: "Switch workspace", exact: true })
          .click()
        await rolePage
          .locator("[data-workspace-switcher]")
          .getByRole("button", { name: "Tenant view", exact: true })
          .waitFor()
        assert.equal(
          await rolePage
            .locator("[data-workspace-switcher]")
            .getByRole("status")
            .count(),
          0,
          "platform switcher has no unresolved tenant entity loading"
        )
        await rolePage
          .locator("[data-workspace-switcher]")
          .getByRole("button", { name: "Tenant view", exact: true })
          .click()
        await rolePage.waitForURL(`**/tenants/${fixture.tenantID}`)
        await rolePage.waitForLoadState("networkidle")
        assert.equal(
          new URL(rolePage.url()).pathname,
          `/tenants/${fixture.tenantID}`
        )
        result.views.push(
          "real switcher navigates platform and back to selected tenant"
        )
        result.views.push("platform view has authorized two-level navigation")
      }
      if (account.id === "entity-account") {
        await rolePage.goto(
          `/tenants/${fixture.tenantID}/entities/${fixture.entityID}/files`
        )
        await rolePage.waitForLoadState("networkidle")
        await openNavigation()
        result.views.push("entity view has scoped two-level navigation")
      }
      await rolePage.goto("/me/profile")
      await rolePage.waitForLoadState("networkidle")
      await openNavigation(true)
      result.views.push("personal account view has two-level navigation")
      assert(
        await rolePage.evaluate(
          () => document.documentElement.scrollWidth <= window.innerWidth
        ),
        `${account.id} ${width} viewport overflow`
      )
      result.status = "PASS"
      save()
      console.log(`PASS ${account.id} ${width}: ${result.views.join("; ")}`)
      await context.close()
    }
  }
  for (const account of fixture.accounts) {
    for (const width of [1440, 390]) {
      const { page, context } = await login(account, width, "zh-CN")
      const result = {
        role: account.id,
        width,
        locale: "zh-CN",
        status: "RUNNING",
        views: [],
      }
      report.roles.push(result)
      await page.goto(`/tenants/${fixture.tenantID}`)
      await page.waitForLoadState("networkidle")
      if (width === 390)
        await page
          .getByRole("button", { name: "打开主导航", exact: true })
          .click()
      const navigation = page.getByRole("navigation", {
        name: "主导航",
        exact: true,
      })
      await navigation.waitFor()
      const text = await navigation.innerText()
      assert(
        !/\?{2,}|\uFFFD/.test(text),
        `${account.id} Chinese navigation corrupted`
      )
      assert(text.includes("系统入口"))
      assert.equal(
        await navigation
          .getByRole("button", { name: "当前租户", exact: true })
          .count(),
        0
      )
      if (account.id === "system-admin") assert(text.includes("组织与成员"))
      const canManage = ["super-admin", "tenant-admin"].includes(account.id)
      assert.equal(
        await navigation.locator(`a[href="${menuPath}"]`).count(),
        canManage ? 1 : 0,
        `${account.id} Chinese menu management link visibility`
      )
      await page.screenshot({
        path: join(output, `${account.id}-${width}-zh-CN-navigation.png`),
      })
      result.views.push(
        "Chinese tenant navigation has valid labels and permission-correct links"
      )
      if (width === 390) await page.keyboard.press("Escape")
      await page.goto(menuPath)
      await page.waitForLoadState("networkidle")
      if (canManage) {
        await page
          .getByRole("tree", { name: "菜单目录树", exact: true })
          .waitFor()
        assert(!/\?{2,}|\uFFFD/.test(await page.getByRole("tree").innerText()))
        result.views.push("Chinese menu management tree renders")
      } else {
        assert.equal(await page.getByRole("tree").count(), 0)
        assert(
          (await page.locator("main").innerText()).includes(
            "当前账号无权执行此操作"
          )
        )
        result.views.push(
          "direct protected URL shows localized denial without management controls"
        )
      }
      await page.screenshot({
        path: join(output, `${account.id}-${width}-zh-CN-menu-manager.png`),
      })
      assert(
        await page.evaluate(
          () => document.documentElement.scrollWidth <= window.innerWidth
        )
      )
      result.status = "PASS"
      save()
      console.log(
        `PASS ${account.id} ${width} zh-CN: ${result.views.join("; ")}`
      )
      await context.close()
    }
  }
  assert.deepEqual(report.errors, [])
  report.status = "PASS"
} catch (error) {
  report.status = "FAIL"
  report.failure = String(error.stack ?? error)
  if (admin) {
    report.actualMenus = await client(admin.page, "menus").catch(() => [])
    await admin.page
      .screenshot({ path: join(output, "failure.png") })
      .catch(() => {})
  }
  throw error
} finally {
  if (admin) {
    const current = await client(admin.page, "menus").catch(() => [])
    for (const original of originalMenus) {
      const actual = current.find((menu) => menu.id === original.id)
      if (
        actual &&
        ((actual.parent_id || "") !== (original.parent_id || "") ||
          actual.sort_order !== original.sort_order)
      )
        await client(admin.page, "updateMenu", original.id, {
          parent_id: original.parent_id || "",
          sort_order: original.sort_order,
        })
    }
    // Delete only this run's fixture, deepest nodes first, using the actual API.
    const pending = new Set(created)
    while (pending.size) {
      const menus = await client(admin.page, "menus").catch(() => [])
      const removable = menus.filter(
        (menu) =>
          pending.has(menu.id) &&
          !menus.some((child) => child.parent_id === menu.id)
      )
      if (!removable.length) break
      for (const menu of removable) {
        await client(admin.page, "deleteMenu", menu.id)
        pending.delete(menu.id)
      }
    }
    if (pending.size)
      report.errors.push(`fixture cleanup incomplete: ${pending.size} nodes`)
  }
  save()
  for (const context of contexts) await context.close().catch(() => {})
  await browser.close()
  console.log(`Report: ${join(output, "report.json")}`)
  if (report.errors.length) {
    report.status = "FAIL"
    save()
    throw new Error(report.errors.join("; "))
  }
}
