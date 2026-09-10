import assert from "node:assert/strict"
import { chromium } from "playwright"

// Isolated browser regression: only HTTP responses are stubbed; the real app,
// routing, storage, requests and user interactions execute in Chromium.
const baseURL = process.env.IAM_CONSOLE_URL ?? "http://127.0.0.1:18094"
const browser = await chromium.launch({
  channel: process.env.IAM_BROWSER_CHANNEL ?? "chrome",
  headless: true,
})
const tenant = (id) => ({
  id,
  name: `Workspace ${id}`,
  slug: id,
  status: "active",
})
const entity = (id, parent_id = "") => ({
  id,
  parent_id,
  name: `Entity ${id}`,
  type: "organization",
  status: "active",
  metadata: {},
})
const consent = {
  client_id: "client/1",
  client_name: "Calendar client",
  tenant_id: "t2",
  scopes: null,
  granted_at: "2026-09-01T00:00:00Z",
  last_used_at: "2026-09-09T00:00:00Z",
}
let completed = 0
async function scenario(name, test, { locale = "en-US", mobile = false } = {}) {
  if (
    process.env.IAM_REGRESSION_FILTER &&
    !name.includes(process.env.IAM_REGRESSION_FILTER)
  )
    return
  const context = await browser.newContext({
    baseURL,
    viewport: mobile
      ? { width: 390, height: 844 }
      : { width: 1440, height: 1000 },
  })
  await context.addCookies([
    { name: "NEXT_LOCALE", value: locale, url: baseURL },
  ])
  const state = {
    subject: "person-1",
    mine: [tenant("t1"), tenant("t2")],
    platform: null,
    entities: [
      entity("root"),
      entity("child", "root"),
      entity("grandchild", "child"),
    ],
    consents: [consent],
    logout: 500,
    revoke: 500,
    tenantsStatus: 200,
    entitiesStatus: 200,
    menusStatus: 200,
    sessionStatus: 200,
    menus: [],
    delayedMenus: [],
    requests: [],
    delayed: [],
  }
  const errors = []
  const page = await context.newPage()
  page.setDefaultTimeout(10000)
  page.on("pageerror", (error) => errors.push(error.message))
  const mutation = {
    "principals-empty": ["principals", "items"],
    "tenants-empty": ["tenants", "tenants"],
    "oauth-clients-empty": ["oauth-clients", "clients"],
  }[process.env.IAM_REGRESSION_MUTATION]
  if (mutation) {
    await page.route(`**/src/app/${mutation[0]}/page.tsx*`, async (route) => {
      const response = await route.fetch()
      const source = await response.text()
      const fixed = `!loading && !loadError && ${mutation[1]}.length === 0`
      assert(
        source.includes(fixed),
        "Mutation targets the real empty-state guard"
      )
      await route.fulfill({
        response,
        body: source.replaceAll(fixed, `${mutation[1]}.length === 0`),
      })
    })
  }
  await page.route("**/api/**", async (route) => {
    const request = route.request()
    const path = new URL(request.url()).pathname.slice(4)
    state.requests.push({
      path,
      method: request.method(),
      tenant: request.headers()["x-tenant-id"],
    })
    const send = (data, status = 200) =>
      route.fulfill({
        status,
        contentType: "application/json",
        body: JSON.stringify({
          code: status < 400 ? 0 : status,
          message: status < 400 ? "ok" : `failure ${status}`,
          data,
        }),
      })
    if (path === state.listPath) {
      const result = () => send(state.listData, state.listStatus)
      if (state.delayList) {
        state.heldList.push(result)
        state.onListDelayed()
        return
      }
      return result()
    }
    if (path === "/authn/sessions/current" && request.method() === "GET") {
      if (state.sessionStatus === "network") return route.abort("failed")
      return send(
        {
          subject: state.subject,
          issuer: "test",
          acr: 1,
          preferred_username: state.subject,
          organization_id: "t1",
        },
        state.sessionStatus
      )
    }
    if (path === "/authn/sessions" && request.method() === "POST") {
      state.sessionStatus = state.afterLoginStatus ?? 200
      return send({ status: "authenticated", return_url: "/after-login" })
    }
    if (
      (path === "/authn/sessions/current" || path === "/authn/sessions") &&
      request.method() === "DELETE"
    ) {
      if (state.logout === "network") return route.abort("failed")
      return send({ logout_url: "/signed-out" }, state.logout)
    }
    if (path === "/authn/sessions")
      return send([
        {
          id: "session-1",
          current: true,
          created_at: "2026-09-01T00:00:00Z",
          last_seen_at: "2026-09-09T00:00:00Z",
          expires_at: "2026-09-12T00:00:00Z",
          absolute_expires_at: "2026-09-12T00:00:00Z",
          user_agent: "Regression browser",
          ip_address: "127.0.0.1",
        },
      ])
    if (path === "/iam/me/tenants") {
      if (state.delayTenants) {
        const items = state.delayTenants
        state.delayed.push(() => send(items))
        state.onDelayed?.()
        return
      }
      return send(state.mine, state.tenantsStatus)
    }
    if (path === "/iam/tenants")
      return send(state.platform, state.platform ? 200 : 403)
    if (path === "/iam/entities")
      return send(state.entities, state.entitiesStatus)
    if (path === "/iam/me/menus") {
      if (state.delayMenus) {
        const menus = state.delayMenus
        state.delayedMenus.push(() => send(menus))
        state.onMenuDelayed?.()
        return
      }
      return send(state.menus, state.menusStatus)
    }
    if (path === "/iam/me/oauth-consents") {
      if (state.invalidConsentResponse)
        return route.fulfill({
          status: 200,
          contentType: "application/json",
          body: "invalid",
        })
      return send(state.consents, state.consentStatus ?? 200)
    }
    if (
      path.startsWith("/iam/me/oauth-consents/") &&
      request.method() === "DELETE"
    ) {
      if (state.revoke === 200) state.consents = []
      return send({ revoked: state.revoke === 200 }, state.revoke)
    }
    if (path === "/iam/principals") return send({ items: [] })
    if (path === "/iam/fields/effective") return send({ fields: {} })
    if (path === "/iam/oauth-clients")
      return send([
        {
          id: "machine",
          tenant_id: "t1",
          name: "Machine client",
          consent_mode: "implicit",
          redirect_uris: null,
          grant_types: ["client_credentials"],
          scopes: null,
          public_client: false,
          status: "active",
        },
      ])
    return send([])
  })
  try {
    await test(page, state)
    assert.deepEqual(errors, [], "No uncaught browser errors")
    completed++
    console.log(`PASS ${name}`)
  } catch (error) {
    console.error(`FAIL ${name}: ${await page.locator("body").innerText()}`)
    throw error
  } finally {
    await context.close()
  }
}
async function visible(page, text) {
  await page.getByText(text, { exact: true }).first().waitFor()
}
async function menuLogout(page) {
  await page.getByRole("button", { name: "Account menu", exact: true }).click()
  await page.getByRole("menuitem", { name: "Sign out", exact: true }).click()
}
async function workspace(page) {
  await page
    .getByRole("button", { name: "Switch workspace", exact: true })
    .click()
}
try {
  for (const route of ["/", "/account/applications", "/login"]) {
    await scenario(
      `session verification 503/network retry preserves route ${route}`,
      async (page, state) => {
        state.sessionStatus = 503
        await page.goto(route)
        await visible(
          page,
          "Your session could not be verified. Check your connection and retry. Your sign-in status is currently unknown."
        )
        assert.equal(new URL(page.url()).pathname, route)
        assert.equal(await page.locator('input[name="password"]').count(), 0)
        assert.equal(
          await page
            .getByRole("button", { name: "Account menu", exact: true })
            .count(),
          0
        )
        state.sessionStatus = "network"
        await page.getByRole("button", { name: "Retry now" }).click()
        await visible(
          page,
          "Your session could not be verified. Check your connection and retry. Your sign-in status is currently unknown."
        )
        assert.equal(new URL(page.url()).pathname, route)
        state.sessionStatus = 200
        await page.getByRole("button", { name: "Retry now" }).click()
        await page
          .getByRole("button", { name: "Account menu", exact: true })
          .waitFor()
      }
    )
  }
  await scenario(
    "session 401 reaches login and failed post-login verification never follows success URL",
    async (page, state) => {
      state.sessionStatus = 401
      await page.goto("/account/applications")
      await page.waitForURL("**/login")
      await page.locator('input[name="login_name"]').fill("person-1")
      await page.locator('input[name="password"]').fill("local-test-password")
      state.afterLoginStatus = 503
      await page.locator('button[type="submit"]').click()
      await visible(
        page,
        "Your session could not be verified. Check your connection and retry. Your sign-in status is currently unknown."
      )
      assert.equal(new URL(page.url()).pathname, "/login")
      state.sessionStatus = 401
      await page.getByRole("button", { name: "Retry now" }).click()
      await page.locator('input[name="password"]').waitFor()
      assert.equal(new URL(page.url()).pathname, "/login")
    }
  )
  const menu = (label) => [
    {
      id: label,
      label,
      path: "/iam/roles",
      sort_order: 0,
      permission_code: "role_view",
    },
  ]
  await scenario(
    "menu refresh rejects older responses in the same tenant",
    async (page, state) => {
      state.menus = menu("Original menu")
      await page.goto("/")
      await visible(page, "Original menu")
      state.delayMenus = menu("Outdated menu")
      const arrived = new Promise((resolve) => {
        state.onMenuDelayed = resolve
      })
      await page.evaluate(() =>
        window.dispatchEvent(new Event("menu-catalog-change"))
      )
      await arrived
      state.delayMenus = null
      state.menus = menu("Latest menu")
      await page.evaluate(() =>
        window.dispatchEvent(new Event("menu-catalog-change"))
      )
      await visible(page, "Latest menu")
      for (const release of state.delayedMenus) await release()
      await page.evaluate(
        () =>
          new Promise((resolve) =>
            requestAnimationFrame(() => requestAnimationFrame(resolve))
          )
      )
      await visible(page, "Latest menu")
      assert.equal(
        await page.getByText("Outdated menu", { exact: true }).count(),
        0
      )
    }
  )
  await scenario(
    "automatic tenant selection hides cached menus while new menus load",
    async (page, state) => {
      state.menus = menu("Tenant one menu")
      await page.goto("/")
      await visible(page, "Tenant one menu")
      state.delayMenus = menu("Tenant two menu")
      const arrived = new Promise((resolve) => {
        state.onMenuDelayed = resolve
      })
      state.mine = [tenant("t2")]
      await page.evaluate(() =>
        window.dispatchEvent(new Event("tenant-catalog-change"))
      )
      await arrived
      await page.waitForFunction(
        () => localStorage.getItem("iam.tenant") === "t2"
      )
      assert.equal(
        await page.getByText("Tenant one menu", { exact: true }).count(),
        0
      )
      for (const release of state.delayedMenus) await release()
      await visible(page, "Tenant two menu")
    }
  )
  await scenario(
    "menu response from previous tenant cannot replace current menu",
    async (page, state) => {
      state.menus = menu("Tenant one menu")
      await page.goto("/")
      await visible(page, "Tenant one menu")
      state.delayMenus = menu("Late tenant one menu")
      const arrived = new Promise((resolve) => {
        state.onMenuDelayed = resolve
      })
      await page.evaluate(() =>
        window.dispatchEvent(new Event("menu-catalog-change"))
      )
      await arrived
      state.delayMenus = null
      state.menus = menu("Tenant two menu")
      state.mine = [tenant("t2")]
      await page.evaluate(() =>
        window.dispatchEvent(new Event("tenant-catalog-change"))
      )
      await visible(page, "Tenant two menu")
      for (const release of state.delayedMenus) await release()
      await page.evaluate(
        () =>
          new Promise((resolve) =>
            requestAnimationFrame(() => requestAnimationFrame(resolve))
          )
      )
      await visible(page, "Tenant two menu")
      assert.equal(
        await page.getByText("Late tenant one menu", { exact: true }).count(),
        0
      )
    }
  )
  await scenario(
    "logout 500/network preserves session and retries to server URL",
    async (page, state) => {
      await page.goto("/account/applications")
      await visible(page, "Calendar client")
      await menuLogout(page)
      await visible(
        page,
        "Sign out could not be confirmed. Your session may still be active. Retry before leaving this device."
      )
      assert.equal(new URL(page.url()).pathname, "/account/applications")
      await page.reload()
      await visible(page, "Calendar client")
      state.logout = "network"
      await menuLogout(page)
      await page.getByRole("button", { name: "Retry now" }).waitFor()
      state.logout = 200
      await page.getByRole("button", { name: "Retry now" }).click()
      await page.waitForURL("**/signed-out")
      assert.equal(
        await page.evaluate(() => localStorage.getItem("iam.tenant")),
        ""
      )
    }
  )
  await scenario("logout all failure and success URL", async (page, state) => {
    await page.goto("/account/security")
    await page.getByRole("button", { name: "Sign out all devices" }).click()
    await visible(page, "failure 500")
    assert.equal(new URL(page.url()).pathname, "/account/security")
    state.logout = 200
    await page.getByRole("button", { name: "Sign out all devices" }).click()
    await page.waitForURL("**/signed-out")
    assert.equal(
      await page.evaluate(() => localStorage.getItem("iam.tenant")),
      ""
    )
  })
  await scenario(
    "member tenant discovery, validated selection, account switch",
    async (page, state) => {
      await page.goto("/")
      await workspace(page)
      await page.getByRole("button", { name: /Workspace t2/ }).waitFor()
      await page.getByRole("button", { name: /Workspace t2/ }).click()
      await page.waitForFunction(
        () => localStorage.getItem("iam.tenant") === "t2"
      )
      assert(
        state.requests
          .filter((r) => r.path === "/iam/me/tenants")
          .every((r) => r.tenant === undefined)
      )
      state.subject = "person-2"
      state.mine = [tenant("t3")]
      await page.reload()
      await page.waitForFunction(
        () => localStorage.getItem("iam.tenant") === "t3"
      )
      await workspace(page)
      await page.getByRole("button", { name: /Workspace t3/ }).waitFor()
      assert.equal(
        await page.getByText("Workspace t2", { exact: true }).count(),
        0
      )
      await page.evaluate(() => localStorage.setItem("iam.tenant", "expired"))
      await page.reload()
      await page.waitForFunction(
        () => localStorage.getItem("iam.tenant") === "t3"
      )
    }
  )
  await scenario(
    "platform administrator keeps non-membership tenant access",
    async (page, state) => {
      state.mine = []
      state.platform = [tenant("platform-only")]
      await page.goto("/")
      await page.waitForFunction(
        () => localStorage.getItem("iam.tenant") === "platform-only"
      )
      await workspace(page)
      await page
        .getByRole("button", { name: /Workspace platform-only/ })
        .waitFor()
    }
  )
  await scenario(
    "tenant failure, retry, empty directory and late response",
    async (page, state) => {
      state.tenantsStatus = 500
      await page.goto("/")
      await visible(page, "failure 500")
      await workspace(page)
      state.tenantsStatus = 200
      state.mine = []
      await page.getByRole("button", { name: "Retry now" }).click()
      await visible(
        page,
        "No active workspaces are available for this account."
      )
      state.delayTenants = [tenant("outdated")]
      const delayedArrived = new Promise((resolve) => {
        state.onDelayed = resolve
      })
      await Promise.all([
        delayedArrived,
        page.evaluate(() =>
          window.dispatchEvent(new Event("tenant-catalog-change"))
        ),
      ])
      assert(state.delayed.length > 0)
      const delayed = [...state.delayed]
      state.delayTenants = null
      state.mine = [tenant("current")]
      await page.evaluate(() =>
        window.dispatchEvent(new Event("tenant-catalog-change"))
      )
      await page.waitForFunction(
        () => localStorage.getItem("iam.tenant") === "current"
      )
      // Delayed response resolves after a more recent directory request.
      for (const release of delayed) await release()
      await page.evaluate(
        () =>
          new Promise((resolve) =>
            requestAnimationFrame(() => requestAnimationFrame(resolve))
          )
      )
      assert.equal(
        await page.evaluate(() => localStorage.getItem("iam.tenant")),
        "current"
      )
    }
  )
  for (const mobile of [false, true]) {
    await scenario(
      `child settings retain parent and exclude descendants (${mobile ? "mobile" : "desktop"})`,
      async (page) => {
        await page.goto("/iam/entities/child/manage")
        const rows = page.locator("tbody tr")
        await rows.first().waitFor()
        assert.equal(await rows.count(), 1)
        assert((await rows.innerText()).includes("Entity child"))
        assert((await rows.innerText()).includes("Entity root"))
        assert(!(await rows.innerText()).includes("Entity grandchild"))
        assert.equal(
          await page
            .getByText("No entities in this tenant", { exact: true })
            .count(),
          0
        )
      },
      { mobile }
    )
  }
  await scenario(
    "entity/menu failures stay explicit and do not masquerade as empty",
    async (page, state) => {
      state.entitiesStatus = 403
      state.menusStatus = 500
      await page.goto("/iam/entities")
      await visible(page, "failure 403")
      await visible(page, "failure 500")
      assert.equal(
        await page
          .getByText("No entities in this tenant", { exact: true })
          .count(),
        0
      )
      assert.equal(
        await page
          .getByRole("button", { name: "Create entity", exact: true })
          .count(),
        0
      )
    }
  )
  for (const [locale, title, revoke, confirm, empty] of [
    [
      "en-US",
      "Authorized applications",
      "Revoke Calendar client",
      "Confirm",
      "You have not authorized any applications.",
    ],
    [
      "zh-CN",
      "已授权应用",
      "撤销 Calendar client",
      "确认",
      "您尚未授权任何应用。",
    ],
    [
      "ms-MY",
      "Aplikasi yang dibenarkan",
      "Batalkan Calendar client",
      "Sahkan",
      "Anda belum membenarkan sebarang aplikasi.",
    ],
  ]) {
    await scenario(
      `consent load/revoke failure/retry/readback ${locale}`,
      async (page, state) => {
        await page.goto("/account/applications")
        await page.getByRole("heading", { name: title, exact: true }).waitFor()
        await visible(page, "Calendar client")
        await page.getByRole("button", { name: revoke, exact: true }).click()
        await page.getByRole("button", { name: confirm, exact: true }).click()
        await page.getByRole("dialog").getByRole("alert").waitFor()
        state.revoke = 200
        await page.getByRole("button", { name: confirm, exact: true }).click()
        await visible(page, empty)
        assert(
          state.requests
            .filter((r) => r.path.includes("oauth-consents"))
            .every((r) => r.tenant === undefined)
        )
        assert(
          state.requests.some(
            (r) =>
              r.path === "/iam/me/oauth-consents/client%2F1" &&
              r.method === "DELETE"
          )
        )
      },
      { locale, mobile: locale !== "en-US" }
    )
  }
  await scenario("consent 403 is not an empty state", async (page, state) => {
    state.consentStatus = 403
    await page.goto("/account/applications")
    await visible(page, "failure 403")
    assert.equal(
      await page
        .getByText("You have not authorized any applications.", { exact: true })
        .count(),
      0
    )
    state.consentStatus = 200
    await page.getByRole("button", { name: "Retry now" }).click()
    await visible(page, "Calendar client")
  })
  await scenario(
    "nullable OAuth array fields render and open editor",
    async (page) => {
      await page.goto("/iam/oauth-clients")
      await visible(page, "Machine client")
      await page
        .getByRole("button", { name: "Edit Machine client", exact: true })
        .click()
      await page.getByRole("dialog").waitFor()
    }
  )
  await scenario(
    "language switch changes subsequent client error feedback",
    async (page, state) => {
      state.invalidConsentResponse = true
      await page.goto("/account/applications")
      await visible(page, "The service returned an invalid response.")
      await page.getByRole("button", { name: "Language", exact: true }).click()
      await page.getByRole("button", { name: /ZH 简体中文/ }).click()
      await page
        .getByRole("heading", { name: "已授权应用", exact: true })
        .waitFor()
      await page.getByRole("button", { name: "立即重试", exact: true }).click()
      await visible(page, "服务返回了无效响应。")
    }
  )
  for (const fixture of [
    {
      name: "principals",
      path: "/iam/users",
      api: "/iam/principals",
      empty: "No local accounts",
      emptyData: { items: [] },
      data: {
        items: [
          {
            id: "account-1",
            login_name: "visible-account",
            status: "active",
            updated_at: "2026-09-01T00:00:00Z",
          },
        ],
      },
      row: "visible-account",
    },
    {
      name: "tenants",
      path: "/iam/tenants",
      api: "/iam/tenants",
      empty: "No tenants",
      emptyData: [],
      data: [tenant("visible-tenant")],
      row: "Workspace visible-tenant",
    },
    {
      name: "oauth-clients",
      path: "/iam/oauth-clients",
      api: "/iam/oauth-clients",
      empty: "No OAuth clients registered",
      emptyData: [],
      data: [
        {
          id: "visible-client",
          name: "Visible client",
          status: "active",
          grant_types: ["client_credentials"],
          scopes: [],
          redirect_uris: [],
          public_client: false,
        },
      ],
      row: "Visible client",
    },
  ])
    for (const mobile of [false, true]) {
      await scenario(
        `list states ${fixture.name} ${mobile ? "mobile" : "desktop"}`,
        async (page, state) => {
          await page.goto("/account/applications")
          await page
            .getByRole("heading", {
              name: "Authorized applications",
              exact: true,
            })
            .waitFor()
          state.listPath = fixture.api
          state.listData = fixture.emptyData
          state.listStatus = 403
          state.delayList = true
          state.heldList = []
          const requested = new Promise((resolve) => {
            state.onListDelayed = resolve
          })
          await page.goto(fixture.path)
          await requested
          await visible(page, "Loading")
          assert.equal(
            await page.getByText(fixture.empty, { exact: true }).count(),
            0
          )
          assert(state.heldList.length, "List request is held while loading")
          state.delayList = false
          await Promise.all(state.heldList.map((finish) => finish()))
          await visible(page, "failure 403")
          assert.equal(
            await page.getByText(fixture.empty, { exact: true }).count(),
            0
          )
          assert.equal(
            await page.getByText("0 clients", { exact: true }).count(),
            0
          )
          state.listStatus = 200
          await page
            .getByRole("button", { name: "Retry now", exact: true })
            .click()
          await page
            .getByText(fixture.empty, { exact: true })
            .filter({ visible: true })
            .first()
            .waitFor()
          assert.equal(await page.getByRole("alert").count(), 0)
          state.listData = fixture.data
          await page.reload()
          await page
            .getByText(fixture.row, { exact: true })
            .filter({ visible: true })
            .first()
            .waitFor()
          assert.equal(
            await page.getByText(fixture.empty, { exact: true }).count(),
            0
          )
        },
        { mobile }
      )
    }
  console.log(`PASS ${completed} browser regression scenarios`)
} finally {
  await browser.close()
}
