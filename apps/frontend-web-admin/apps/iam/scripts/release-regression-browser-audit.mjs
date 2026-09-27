import assert from "node:assert/strict"
import { readFileSync } from "node:fs"
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
    permissions: [],
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
    "departments-empty": ["departments", "departments"],
    "groups-empty": ["groups", "groups"],
    "menus-empty": ["menus", "menus"],
    "invitations-empty": ["invitations", "items", "loaded"],
    "service-accounts-empty": ["service-accounts", "accounts"],
    "scim-directories-empty": ["scim-directories", "directories"],
    "audit-events-empty": ["audit-events", "events"],
    "access-reviews-empty": ["access-reviews", null],
  }[process.env.IAM_REGRESSION_MUTATION]
  if (mutation) {
    await page.route(`**/src/app/${mutation[0]}/page.tsx*`, async (route) => {
      const response = await route.fetch()
      const source = await response.text()
      const fixed = mutation[1]
        ? `${mutation[2] ?? "!loading"} && !loadError && ${mutation[1]}.length === 0`
        : "!loadError && /* @__PURE__ */ _jsxDEV(CampaignList"
      assert(
        source.includes(fixed),
        "Mutation targets the real empty-state guard"
      )
      await route.fulfill({
        response,
        body: source.replaceAll(
          fixed,
          mutation[1]
            ? `${mutation[1]}.length === 0`
            : "/* @__PURE__ */ _jsxDEV(CampaignList"
        ),
      })
    })
  }
  await page.route("**/api/**", async (route) => {
    const request = route.request()
    assert.equal(
      request.headers()["x-tenant-id"],
      undefined,
      "Tenant context belongs in the public URI"
    )
    const resourcePath = new URL(request.url()).pathname.slice(4)
    const tenantID = /^\/tenants\/([^/]+)\//.exec(resourcePath)?.[1]
    const path = tenantID
      ? resourcePath.replace(/^\/tenants\/[^/]+/, "")
      : resourcePath
    state.requests.push({
      path: resourcePath,
      method: request.method(),
      tenant: tenantID,
    })
    const send = (data, status = 200) =>
      route.fulfill({
        status,
        contentType: "application/json",
        body: JSON.stringify({
          code: status < 400 ? 0 : status,
          message:
            status < 400 ? "ok" : (state.failureMessage ?? `failure ${status}`),
          data,
        }),
      })
    if (resourcePath === state.listPath) {
      const result = () => send(state.listData, state.listStatus)
      if (state.delayList) {
        state.heldList.push(result)
        state.onListDelayed()
        return
      }
      return result()
    }
    if (
      path === `/principals/${state.subject}/permissions` ||
      new RegExp(
        `^/entities/[^/]+/principals/${state.subject}/permissions$`
      ).test(path)
    )
      return send(state.permissions)
    if (state.roleScopeCase) {
      if (path === "/roles")
        return send([{ id: "scope-role", name: "Scope role", description: "" }])
      if (path === "/departments")
        return send([
          {
            id: "scope-department",
            name: "Scope department",
            status: "active",
          },
        ])
      if (path === "/roles/scope-role/data-scope") {
        if (request.method() === "PUT") {
          state.roleScope = { role_id: "scope-role", ...request.postDataJSON() }
          return send(state.roleScope)
        }
        const snapshot = state.roleScope
        if (state.delayRoleScope) {
          state.releaseRoleScope = () => send(snapshot)
          state.onRoleScopeDelayed()
          return
        }
        return send(snapshot)
      }
    }
    if (path === `/principals/${state.subject}/personal-token-permissions`)
      return send([
        { code: "user_view", resource: "user", verb: "view", scope: "tenant" },
      ])
    if (path === `/principals/${state.subject}/reauthentication/methods`)
      return send({
        methods: state.federated ? [] : ["password"],
        mfa_required: !state.federated,
      })
    if (path === `/principals/${state.subject}/identity-links`)
      return send(
        state.links ??
          (state.federated
            ? [
                {
                  id: "link-1",
                  provider_id: "provider-1",
                  provider_name: "Test identity provider",
                  tenant_id: "t1",
                },
              ]
            : [])
      )
    if (path === `/principals/${state.subject}/identity-link-providers`)
      return send(
        state.providers ??
          (state.federated
            ? [
                {
                  id: "provider-1",
                  name: "Test identity provider",
                  tenant_id: "t1",
                },
              ]
            : [])
      )
    if (path === `/principals/${state.subject}/identity-link-transactions`) {
      if (state.identityFlow) {
        assert(state.proofVerified)
        assert.equal(state.reauthPurpose, "identity_link")
        state.links = [
          {
            id: "link-1",
            provider_id: "provider-1",
            provider_name: "Test identity provider",
            tenant_id: "t1",
            external_subject: "External person",
          },
        ]
        return send({ authorization_url: baseURL + "/me/identities" })
      }
      state.proofVerified = true
      return send({ authorization_url: baseURL + "/me/tokens" })
    }
    if (path === `/principals/${state.subject}/personal-token-preparations`) {
      state.tokenPreparation = request.postDataJSON()
      return send({
        request: state.tokenPreparation,
        target: "bound-request-target",
      })
    }
    if (path === `/principals/${state.subject}/reauthentication/challenges`) {
      state.proofVerified = false
      state.reauthPurpose = request.postDataJSON().purpose
      state.reauthTarget = request.postDataJSON().target
      return send({
        proof: "fixture-proof",
        method: state.federated ? "federation" : "password",
        expires_at: "2027-01-01T00:00:00Z",
      })
    }
    if (
      path === `/principals/${state.subject}/reauthentication/verifications`
    ) {
      const body = request.postDataJSON()
      state.proofVerified =
        body.password === "correct-password" && body.code === "123456"
      return send(
        { verified: state.proofVerified },
        state.proofVerified ? 200 : 401
      )
    }
    if (
      path === `/principals/${state.subject}/personal-tokens` &&
      request.method() === "POST"
    ) {
      assert(state.proofVerified, "Token creation requires verified proof")
      assert.equal(state.reauthPurpose, "personal_token_create")
      const token = {
        ...state.tokenPreparation,
        id: "token-1",
        created_at: "2026-09-10T00:00:00Z",
      }
      state.personalTokens = [token]
      return send({ ...token, token: "fixture-one-time-secret" })
    }
    if (path === `/principals/${state.subject}/personal-tokens`)
      return send(state.personalTokens ?? [])
    if (
      path === `/principals/${state.subject}/identity-links/link-1` &&
      request.method() === "DELETE"
    ) {
      assert(state.proofVerified)
      assert.equal(state.reauthPurpose, "identity_unlink")
      assert.equal(state.reauthTarget, "link-1")
      if (state.actionConflict) return send(null, 409)
      state.links = []
      return send({ deleted: true })
    }
    if (path === `/principals/${state.subject}/memberships`)
      return send({
        items: state.memberships ?? [],
        total: state.memberships?.length ?? 0,
      })
    if (
      path === `/principals/${state.subject}/memberships/t1` &&
      request.method() === "DELETE"
    ) {
      assert(state.proofVerified)
      assert.equal(state.reauthPurpose, "tenant_leave")
      assert.equal(state.reauthTarget, "t1")
      if (state.actionConflict) return send(null, 409)
      state.memberships = state.memberships.filter(
        (item) => item.tenant_id !== "t1"
      )
      state.mine = state.mine.filter((item) => item.id !== "t1")
      return send({ status: "disabled" })
    }
    if (path === `/principals/${state.subject}/closure-request`) {
      state.closure ??= {
        principal_id: state.subject,
        status: "none",
        version: 0,
      }
      if (request.method() !== "GET") {
        assert.equal(request.postDataJSON().version, state.closure.version)
        if (state.actionConflict) return send(null, 409)
        if (request.method() === "POST") {
          assert(state.proofVerified)
          assert.equal(state.reauthPurpose, "principal_close")
          assert.equal(state.reauthTarget, state.subject)
        }
        state.closure = {
          ...state.closure,
          status: request.method() === "POST" ? "pending" : "canceled",
          version: state.closure.version + 1,
        }
      }
      return send(state.closure)
    }
    if (path === "/system/closure-requests") {
      const status = new URL(request.url()).searchParams.get("status")
      const items = (state.closureQueue ?? []).filter(
        (item) => item.status === status
      )
      return send({ items, total: items.length })
    }
    if (
      path.startsWith("/system/principals/") &&
      path.endsWith("/closure-request") &&
      request.method() === "PATCH"
    ) {
      const id = path.split("/")[3]
      const item = state.closureQueue.find((entry) => entry.principal_id === id)
      assert.equal(request.postDataJSON().version, item.version)
      if (state.actionConflict) return send(null, 409)
      const status = request.postDataJSON().status
      assert(["processing", "rejected"].includes(status))
      item.status = status
      item.version++
      return send(item)
    }
    if (
      path === `/principals/${state.subject}/personal-tokens/token-1` &&
      request.method() === "DELETE"
    ) {
      state.personalTokens[0].revoked_at = "2026-09-10T00:01:00Z"
      return send({ revoked: true })
    }
    if (path === "/me" && request.method() === "GET") {
      if (state.accountIdentityCase) {
        const profile = {
          subject: state.subject,
          issuer: "test",
          acr: 1,
          organization_id: "t1",
          ...state.accountProfile,
        }
        if (state.delayAccountProfile) {
          state.releaseAccountProfile = () => send(profile)
          state.onAccountProfileDelayed()
          return
        }
        if (state.accountProfileStatus === "network")
          return route.abort("failed")
        return send(profile, state.accountProfileStatus ?? 200)
      }
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
    if (path === "/login" && request.method() === "POST") {
      state.sessionStatus = state.afterLoginStatus ?? 200
      return send({ status: "authenticated", return_url: "/after-login" })
    }
    if (
      (path === "/sessions/current" ||
        path === `/principals/${state.subject}/sessions`) &&
      request.method() === "DELETE"
    ) {
      if (state.logout === "network") return route.abort("failed")
      return send({ logout_url: "/signed-out" }, state.logout)
    }
    if (path === `/principals/${state.subject}/sessions`)
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
    if (path === `/principals/${state.subject}/tenants`) {
      if (state.delayTenants) {
        const items = state.delayTenants
        state.delayed.push(() => send(items))
        state.onDelayed?.()
        return
      }
      return send(state.mine, state.tenantsStatus)
    }
    if (path === "/tenants")
      return send(state.platform, state.platform ? 200 : 403)
    if (path === "/entities") return send(state.entities, state.entitiesStatus)
    if (path === `/principals/${state.subject}/menus`) {
      if (state.delayMenus) {
        const menus = state.delayMenus
        state.delayedMenus.push(() => send(menus))
        state.onMenuDelayed?.()
        return
      }
      return send(state.menus, state.menusStatus)
    }
    if (path === `/principals/${state.subject}/oauth-consents`) {
      if (state.invalidConsentResponse)
        return route.fulfill({
          status: 200,
          contentType: "application/json",
          body: "invalid",
        })
      return send(state.consents, state.consentStatus ?? 200)
    }
    if (
      path.startsWith(`/principals/${state.subject}/oauth-consents/`) &&
      request.method() === "DELETE"
    ) {
      if (state.revoke === 200) state.consents = []
      return send({ revoked: state.revoke === 200 }, state.revoke)
    }
    if (path === "/principals") return send({ items: [] })
    if (path === "/effective-fields") return send({ fields: {} })
    if (path === "/audit-integrity")
      return send({ valid: true, verified_events: 0, head_sequence: 0 })
    if (path === "/oauth-clients")
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
    console.error(
      "Browser errors:",
      errors,
      "Recent requests:",
      state.requests.slice(-10)
    )
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
  await scenario(
    "account identity uses the global session without tenant addressing",
    async (page, state) => {
      state.accountIdentityCase = true
      state.accountProfile = {
        preferred_username: "Administrator name",
        email: "",
      }
      const dashboard = page.waitForResponse((response) =>
        response.url().endsWith(`/principals/${state.subject}/sessions`)
      )
      await page.goto("/tenants/t1")
      await page.waitForURL("**/tenants/t1")
      await dashboard
      const account = page.getByRole("button", {
        name: "Account menu",
        exact: true,
      })
      await page.waitForFunction(() =>
        document
          .querySelector('button[aria-label="Account menu"]')
          ?.textContent.includes("Administrator name")
      )
      assert((await account.innerText()).includes("Administrator name"))
      const sessions = state.requests.filter(
        (request) => request.path === "/me"
      )
      assert(
        sessions.some((request) => !request.tenant),
        "Authentication remains global"
      )
      assert(
        sessions.every((request) => !request.tenant),
        "Authentication and display both use the global session"
      )
      await account.click()
      await page
        .getByRole("menuitem", { name: "My account", exact: true })
        .waitFor()
      assert.equal(new URL(page.url()).pathname, "/tenants/t1")
    }
  )
  await scenario(
    "account identity ignores late global profile refreshes after workspace changes",
    async (page, state) => {
      state.accountIdentityCase = true
      state.accountProfile = { preferred_username: "Old profile name" }
      await page.goto("/tenants/t1")
      await page.waitForURL("**/tenants/t1")
      await page.waitForFunction(() =>
        document
          .querySelector('button[aria-label="Account menu"]')
          ?.textContent.includes("Old profile name")
      )
      state.delayAccountProfile = true
      const delayed = new Promise((resolve) => {
        state.onAccountProfileDelayed = resolve
      })
      await workspace(page)
      await page.getByRole("button", { name: /Workspace t2/ }).click()
      await delayed
      await page.waitForURL("**/tenants/t2")
      await page.keyboard.press("Escape")
      await page.getByRole("dialog").waitFor({ state: "hidden" })
      state.delayAccountProfile = false
      state.accountProfile = { preferred_username: "Current profile name" }
      await workspace(page)
      await page.getByRole("button", { name: /Workspace t1/ }).click()
      await page.keyboard.press("Escape")
      await page.waitForFunction(() =>
        document
          .querySelector('button[aria-label="Account menu"]')
          ?.textContent.includes("Current profile name")
      )
      await state.releaseAccountProfile()
      await page.waitForTimeout(200)
      const account = page.getByRole("button", {
        name: "Account menu",
        exact: true,
      })
      assert((await account.innerText()).includes("Current profile name"))
      assert(!(await account.innerText()).includes("Old profile name"))
    }
  )
  await scenario(
    "account identity clears the previous global profile name when fields are omitted",
    async (page, state) => {
      state.accountIdentityCase = true
      state.accountProfile = { preferred_username: "Previous profile name" }
      await page.goto("/tenants/t1")
      await page.waitForFunction(() =>
        document
          .querySelector('button[aria-label="Account menu"]')
          ?.textContent.includes("Previous profile name")
      )
      const updated = page.waitForResponse((response) =>
        response.url().endsWith("/me")
      )
      state.accountProfile = {}
      await workspace(page)
      await page.getByRole("button", { name: /Workspace t2/ }).click()
      await updated
      await page.waitForFunction(() =>
        document
          .querySelector('button[aria-label="Account menu"]')
          ?.textContent.includes("person-1")
      )
      await page
        .getByRole("button", { name: "Account menu", exact: true })
        .click()
      assert(
        !(await page.getByRole("menu").innerText()).includes(
          "Previous profile name"
        )
      )
    }
  )
  for (const status of [403, 503, "network"])
    await scenario(
      `account identity failure ${status} preserves global login`,
      async (page, state) => {
        state.accountIdentityCase = true
        await page.goto("/tenants/t1")
        await page
          .getByRole("button", { name: "Account menu", exact: true })
          .waitFor()
        state.accountProfileStatus = status
        const requested = page.waitForRequest((request) =>
          request.url().endsWith("/me")
        )
        await workspace(page)
        await page.getByRole("button", { name: /Workspace t2/ }).click()
        await requested
        const account = page.getByRole("button", {
          name: "Account menu",
          exact: true,
        })
        await account.click()
        await page
          .getByRole("menuitem", { name: "My account", exact: true })
          .waitFor()
        assert((await page.getByRole("menu").innerText()).includes("person-1"))
        assert.equal(new URL(page.url()).pathname, "/tenants/t2")
      }
    )
  await scenario(
    "account identity without workspaces stays unscoped",
    async (page, state) => {
      state.accountIdentityCase = true
      state.mine = []
      await page.goto("/")
      await page.waitForURL("**/me/profile")
      await page
        .getByRole("heading", { name: "Basic information", exact: true })
        .waitFor()
      await page
        .getByRole("button", { name: "Account menu", exact: true })
        .click()
      assert((await page.getByRole("menu").innerText()).includes("person-1"))
      assert(
        state.requests
          .filter((request) => request.path === "/me")
          .every((request) => !request.tenant)
      )
    }
  )
  await scenario(
    "account identity mobile uses permitted email for blank username",
    async (page, state) => {
      state.accountIdentityCase = true
      state.accountProfile = {
        preferred_username: "  ",
        email: "person@example.test",
      }
      await page.goto("/tenants/t1")
      await page.waitForFunction(() =>
        document
          .querySelector('button[aria-label="Account menu"]')
          ?.textContent.includes("person@example.test")
      )
      await page
        .getByRole("button", { name: "Account menu", exact: true })
        .click()
      assert(
        (await page.getByRole("menu").innerText()).includes(
          "person@example.test"
        )
      )
      assert(
        await page.evaluate(
          () => document.documentElement.scrollWidth <= innerWidth
        )
      )
    },
    { mobile: true }
  )
  await scenario(
    "account identity rejects a profile for a different subject",
    async (page, state) => {
      state.accountIdentityCase = true
      await page.goto("/tenants/t1")
      await page
        .getByRole("button", { name: "Account menu", exact: true })
        .waitFor()
      state.accountProfile = {
        subject: "other-person",
        preferred_username: "Other person's name",
      }
      const received = page.waitForResponse((response) =>
        response.url().endsWith("/me")
      )
      await workspace(page)
      await page.getByRole("button", { name: /Workspace t2/ }).click()
      await received
      await page
        .getByRole("button", { name: "Account menu", exact: true })
        .click()
      const text = await page.getByRole("menu").innerText()
      assert(text.includes("person-1"))
      assert(!text.includes("Other person's name"))
    }
  )
  await scenario(
    "account identity remains readable in the personal account header",
    async (page, state) => {
      state.accountIdentityCase = true
      state.accountProfile = { preferred_username: "Account owner" }
      await page.goto("/tenants/t1")
      await page.waitForFunction(() =>
        document
          .querySelector('button[aria-label="Account menu"]')
          ?.textContent.includes("Account owner")
      )
      await page
        .getByRole("button", { name: "Account menu", exact: true })
        .click()
      await page
        .getByRole("menuitem", { name: "My account", exact: true })
        .click()
      await page.waitForURL("**/me/profile")
      await page.waitForFunction(() =>
        document
          .querySelector('button[aria-label="Account menu"]')
          ?.textContent.includes("Account owner")
      )
    }
  )
  for (const route of ["/tenants/t1", "/me/applications", "/login"]) {
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
      await page.goto("/me/applications")
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
      path: "/tenants/t1/roles",
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
    "workspace navigation hides cached menus while new menus load",
    async (page, state) => {
      state.menus = menu("Tenant one menu")
      await page.goto("/")
      await visible(page, "Tenant one menu")
      state.delayMenus = menu("Tenant two menu")
      const arrived = new Promise((resolve) => {
        state.onMenuDelayed = resolve
      })
      await workspace(page)
      await page.getByRole("button", { name: /Workspace t2/ }).click()
      await page.keyboard.press("Escape")
      await arrived
      await page.waitForURL("**/tenants/t2")
      await page.waitForFunction(
        () => sessionStorage.getItem("iam.tenant") === "t2"
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
      await workspace(page)
      await page.getByRole("button", { name: /Workspace t2/ }).click()
      await page.keyboard.press("Escape")
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
      await page.goto("/me/applications")
      await visible(page, "Calendar client")
      await menuLogout(page)
      await visible(
        page,
        "Sign out could not be confirmed. Your session may still be active. Retry before leaving this device."
      )
      assert.equal(new URL(page.url()).pathname, "/me/applications")
      await page.reload()
      await visible(page, "Calendar client")
      state.logout = "network"
      await menuLogout(page)
      await page.getByRole("button", { name: "Retry now" }).waitFor()
      state.logout = 200
      await page.getByRole("button", { name: "Retry now" }).click()
      await page.waitForURL("**/signed-out")
      assert.equal(
        await page.evaluate(() => sessionStorage.getItem("iam.tenant")),
        ""
      )
    }
  )
  await scenario("logout all failure and success URL", async (page, state) => {
    await page.goto("/me/security")
    await page.getByRole("button", { name: "Sign out all devices" }).click()
    await visible(page, "failure 500")
    assert.equal(new URL(page.url()).pathname, "/me/security")
    state.logout = 200
    await page.getByRole("button", { name: "Sign out all devices" }).click()
    await page.waitForURL("**/signed-out")
    assert.equal(
      await page.evaluate(() => sessionStorage.getItem("iam.tenant")),
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
        () => sessionStorage.getItem("iam.tenant") === "t2"
      )
      assert(
        state.requests
          .filter((r) => r.path === `/principals/${state.subject}/tenants`)
          .every((r) => r.tenant === undefined)
      )
      state.subject = "person-2"
      state.mine = [tenant("t3")]
      await page.reload()
      await page.waitForFunction(
        () => sessionStorage.getItem("iam.tenant") === "t3"
      )
      await workspace(page)
      await page.getByRole("button", { name: /Workspace t3/ }).waitFor()
      assert.equal(
        await page.getByText("Workspace t2", { exact: true }).count(),
        0
      )
      await page.evaluate(() => sessionStorage.setItem("iam.tenant", "expired"))
      await page.reload()
      await page.waitForFunction(
        () => sessionStorage.getItem("iam.tenant") === "t3"
      )
    }
  )
  await scenario(
    "platform administrator keeps non-membership tenant access",
    async (page, state) => {
      state.mine = []
      state.platform = [tenant("platform-only")]
      await page.goto("/system/tenants")
      await page.waitForFunction(
        () => sessionStorage.getItem("iam.tenant") === "platform-only"
      )
      await workspace(page)
      await page
        .getByRole("dialog")
        .getByRole("button", { name: /Workspace platform-only/ })
        .waitFor()
    }
  )
  await scenario(
    "tenant failure, retry, empty directory and late response",
    async (page, state) => {
      state.tenantsStatus = 500
      await page.goto("/tenants/t1")
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
        () => sessionStorage.getItem("iam.tenant") === "current"
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
        await page.evaluate(() => sessionStorage.getItem("iam.tenant")),
        "current"
      )
    }
  )
  for (const mobile of [false, true]) {
    await scenario(
      `child settings retain parent and exclude descendants (${mobile ? "mobile" : "desktop"})`,
      async (page, state) => {
        state.permissions = ["entity_view", "entity_update"]
        await page.goto("/tenants/t1/entities/child/manage")
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
      await page.goto("/tenants/t1/entities")
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
        await page.goto("/me/applications")
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
              r.path ===
                `/principals/${state.subject}/oauth-consents/client%2F1` &&
              r.method === "DELETE"
          )
        )
      },
      { locale, mobile: locale !== "en-US" }
    )
  }
  await scenario("consent 403 is not an empty state", async (page, state) => {
    state.consentStatus = 403
    await page.goto("/me/applications")
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
  for (const saveDraft of [false, true])
    await scenario(
      `late role scope read cannot overwrite an ${saveDraft ? "edited and saved" : "unsaved"} scope`,
      async (page, state) => {
        state.roleScopeCase = true
        state.roleScope = {
          role_id: "scope-role",
          scope: "all",
          department_ids: [],
        }
        state.permissions = ["role_view", "role_update", "dept_view"]
        await page.goto("/tenants/t1/roles?role=scope-role&view=scope")
        const scope = page.locator("#role-data-scope")
        await scope.waitFor()
        state.delayRoleScope = true
        const delayed = new Promise((resolve) => {
          state.onRoleScopeDelayed = resolve
        })
        await page.evaluate(() =>
          window.dispatchEvent(new Event("menu-catalog-change"))
        )
        await delayed
        await scope.click()
        await page
          .getByRole("option", { name: "Selected departments", exact: true })
          .click()
        const checkbox = page.locator(
          '[data-scope-department-id="scope-department"]'
        )
        await checkbox.click()
        if (saveDraft) {
          const saved = page.waitForResponse(
            (response) =>
              response.url().endsWith("/scope-role/data-scope") &&
              response.request().method() === "PUT"
          )
          await page.locator("[data-save-data-scope]").click()
          await saved
          assert.equal(state.roleScope.scope, "selected_departments")
        }
        const stale = page.waitForResponse(
          (response) =>
            response.url().endsWith("/scope-role/data-scope") &&
            response.request().method() === "GET"
        )
        await state.releaseRoleScope()
        await stale
        await page.waitForTimeout(200)
        assert.equal(await scope.innerText(), "Selected departments")
        assert.equal(await checkbox.getAttribute("aria-checked"), "true")
      }
    )
  await scenario(
    "role scope refresh initializes an untouched draft",
    async (page, state) => {
      state.roleScopeCase = true
      state.roleScope = {
        role_id: "scope-role",
        scope: "all",
        department_ids: [],
      }
      state.permissions = ["role_view", "role_update", "dept_view"]
      await page.goto("/tenants/t1/roles?role=scope-role&view=scope")
      const scope = page.locator("#role-data-scope")
      await scope.waitFor()
      state.roleScope = {
        role_id: "scope-role",
        scope: "self",
        department_ids: [],
      }
      await page.evaluate(() =>
        window.dispatchEvent(new Event("menu-catalog-change"))
      )
      await page.waitForFunction(
        () =>
          document.querySelector("#role-data-scope")?.textContent === "Own data"
      )
      assert.equal(await scope.innerText(), "Own data")
    }
  )
  await scenario(
    "nullable OAuth array fields render and open editor",
    async (page, state) => {
      state.permissions = ["oauth_client_update"]
      await page.goto("/tenants/t1/oauth-clients")
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
      await page.goto("/me/applications")
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
    ...[
      ["departments", "No departments in this tenant"],
      ["groups", "No user groups in this tenant"],
      ["menus", "No menu nodes"],
      ["invitations", "No member invitations"],
      ["access-reviews", "No access reviews"],
      ["scim-directories", "No SCIM directories created"],
    ].map(([name, empty]) => ({
      name,
      path: `/tenants/t1/${name}`,
      api: `/tenants/t1/${
        {
          groups: "principal-groups",
          invitations: "membership-invitations",
          "access-reviews": "role-access-reviews",
        }[name] ?? name
      }`,
      empty,
      emptyData: [],
      data: [
        {
          id: "visible-item",
          name: "Visible item",
          label: "Visible item",
          email: "visible@example.test",
          code: "visible-item",
          status:
            name === "invitations"
              ? "pending"
              : name === "access-reviews"
                ? "open"
                : "active",
          type: "static",
          sort_order: 0,
          version: 1,
          role_ids: [],
          items: [],
          total: 0,
          pending: 0,
          kept: 0,
          revoked: 0,
          created_at: "2026-09-01T00:00:00Z",
          updated_at: "2026-09-01T00:00:00Z",
          due_at: "2027-09-01T00:00:00Z",
          expires_at: "2027-09-01T00:00:00Z",
        },
      ],
      row: name === "invitations" ? "visible@example.test" : "Visible item",
    })),
    {
      name: "service-accounts",
      path: "/tenants/t1/service-accounts",
      api: "/tenants/t1/service-accounts",
      empty: "No service accounts created",
      emptyData: { items: [], total: 0 },
      data: {
        items: [
          {
            id: "machine",
            login_name: "visible-machine",
            display_name: "Visible machine",
            status: "active",
            version: 1,
          },
        ],
        total: 1,
      },
      row: "Visible machine",
    },
    {
      name: "audit-events",
      path: "/tenants/t1/audit/events",
      api: "/tenants/t1/audit/events",
      empty: "No matching audit events",
      emptyData: [],
      data: [
        {
          id: "event-1",
          event_type: "visible_event",
          outcome: "success",
          sequence: 1,
          detail: {},
          created_at: "2026-09-01T00:00:00Z",
        },
      ],
      row: "visible_event",
    },
    {
      name: "principals",
      path: "/tenants/t1/users",
      api: "/tenants/t1/principals",
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
      path: "/system/tenants",
      api: "/tenants",
      empty: "No tenants",
      emptyData: [],
      data: [tenant("visible-tenant")],
      row: "Workspace visible-tenant",
    },
    {
      name: "oauth-clients",
      path: "/tenants/t1/oauth-clients",
      api: "/tenants/t1/oauth-clients",
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
          await page.goto("/me/applications")
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
          await Promise.race([
            requested,
            page.waitForTimeout(10_000).then(() => {
              throw new Error(`List request not observed: ${fixture.api}`)
            }),
          ])
          if (fixture.name === "access-reviews")
            await page
              .getByLabel("Loading access reviews", { exact: true })
              .waitFor()
          else await visible(page, "Loading")
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
          for (const zero of [
            "0 accounts",
            "0 directories",
            "0 root nodes · 0 menus",
            "0-0 / 0",
          ])
            assert.equal(await page.getByText(zero, { exact: true }).count(), 0)
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
            .getByText(fixture.row, { exact: false })
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
  for (const locale of ["en-US", "zh-CN", "ms-MY"])
    for (const mobile of [false, true]) {
      const messages = JSON.parse(
        readFileSync(
          new URL(`../src/messages/${locale}.json`, import.meta.url),
          "utf8"
        )
      )
      await scenario(
        `personal token verification create revoke ${locale} ${mobile ? "mobile" : "desktop"}`,
        async (page, state) => {
          await page.goto("/me/tokens")
          await page
            .getByRole("heading", {
              name: messages.personalTokens,
              exact: true,
            })
            .waitFor()
          await visible(page, messages.noPersonalTokens)
          await page
            .getByRole("button", {
              name: messages.createPersonalToken,
              exact: true,
            })
            .click()
          assert.equal(
            await page.locator("#token-expiry").inputValue(),
            "",
            "User must choose the expiry"
          )
          await page.locator("#token-name").fill("Acceptance token")
          await page.locator("#token-tenant").selectOption("t1")
          await page.locator("#token-expiry").fill("2027-01-01T12:00")
          await page.getByRole("checkbox").check()
          await page
            .getByRole("dialog")
            .getByRole("button", { name: messages.continue, exact: true })
            .click()
          await page.locator("#reauth-password").fill("wrong-password")
          await page.locator("#reauth-code").fill("123456")
          await page
            .getByRole("dialog")
            .getByRole("button", { name: messages.continue, exact: true })
            .click()
          await visible(page, "failure 401")
          assert.equal(state.personalTokens?.length ?? 0, 0)
          await page.locator("#reauth-password").fill("correct-password")
          await page
            .getByRole("dialog")
            .getByRole("button", { name: messages.continue, exact: true })
            .click()
          await page.getByTestId("personal-token-secret").waitFor()
          assert.equal(
            await page.getByTestId("personal-token-secret").innerText(),
            "fixture-one-time-secret"
          )
          assert.equal(
            await page.evaluate(() =>
              JSON.stringify({ ...localStorage, ...sessionStorage }).includes(
                "fixture-one-time-secret"
              )
            ),
            false
          )
          await page
            .getByRole("dialog")
            .getByRole("button", { name: messages.close, exact: true })
            .first()
            .click()
          await page.reload()
          await visible(page, "Acceptance token")
          assert.equal(
            await page.getByTestId("personal-token-secret").count(),
            0
          )
          await page
            .getByRole("button", { name: messages.revoke, exact: true })
            .click()
          await page
            .getByRole("dialog")
            .getByRole("button", { name: messages.revoke, exact: true })
            .click()
          await page
            .getByRole("button", { name: messages.revoked, exact: true })
            .waitFor()
          for (const request of state.requests.filter((item) =>
            item.path.startsWith(`/principals/${state.subject}/`)
          )) {
            assert.equal(request.tenant, undefined)
          }
          assert(
            await page.evaluate(
              () => document.documentElement.scrollWidth <= innerWidth
            )
          )
        },
        { locale, mobile }
      )
    }
  await scenario(
    "personal token federated continuation keeps proof out of URL",
    async (page, state) => {
      state.federated = true
      await page.goto("/me/tokens")
      await page
        .getByRole("button", { name: "Create personal token", exact: true })
        .click()
      await page.locator("#token-name").fill("Federated token")
      await page.locator("#token-tenant").selectOption("t1")
      await page.locator("#token-expiry").fill("2027-01-01T12:00")
      await page.getByRole("checkbox").check()
      await page
        .getByRole("dialog")
        .getByRole("button", { name: "Continue", exact: true })
        .click()
      await page.locator("#reauth-method").waitFor()
      await page
        .getByRole("dialog")
        .getByRole("button", { name: "Continue", exact: true })
        .click()
      await page.getByTestId("personal-token-secret").waitFor()
      assert.equal(new URL(page.url()).search, "")
      assert.equal(
        await page.evaluate(() =>
          sessionStorage.getItem("iam.reauthentication")
        ),
        null
      )
    }
  )
  for (const locale of ["en-US", "zh-CN", "ms-MY"])
    for (const mobile of [false, true]) {
      const messages = JSON.parse(
        readFileSync(
          new URL(`../src/messages/${locale}.json`, import.meta.url),
          "utf8"
        )
      )
      await scenario(
        `personal lifecycle conflicts and readback ${locale} ${mobile ? "mobile" : "desktop"}`,
        async (page, state) => {
          const button = (key) =>
            page.getByRole("button", { name: messages[key], exact: true })
          const verify = async () => {
            await page.locator("#reauth-password").fill("correct-password")
            await page.locator("#reauth-code").fill("123456")
            await page
              .getByRole("dialog")
              .getByRole("button", { name: messages.continue, exact: true })
              .click()
          }
          state.memberships = [
            { tenant_id: "t1", tenant_name: "Workspace t1" },
            { tenant_id: "t2", tenant_name: "Workspace t2" },
          ]
          await page.goto("/me/memberships")
          await visible(page, "Workspace t1")
          state.actionConflict = true
          await button("leaveTenant").first().click()
          await verify()
          await visible(page, "failure 409")
          assert.equal(state.memberships.length, 2)
          state.actionConflict = false
          await verify()
          await page.getByRole("dialog").waitFor({ state: "hidden" })
          await visible(page, "Workspace t2")
          assert.equal(
            await page
              .getByRole("heading", { name: "Workspace t1", exact: true })
              .count(),
            0
          )
          await page.reload()
          await visible(page, "Workspace t2")
          assert.equal(state.sessionStatus, 200)

          await page.goto("/me/closure")
          await button("submitClosureRequest").click()
          await verify()
          await button("cancelClosureRequest").waitFor()
          assert.equal(state.closure.status, "pending")
          state.actionConflict = true
          await button("cancelClosureRequest").click()
          await visible(page, "failure 409")
          assert.equal(state.closure.status, "pending")
          state.actionConflict = false
          await button("cancelClosureRequest").click()
          await button("submitClosureRequest").waitFor()
          await page.reload()
          await visible(page, messages.closureStatus_canceled)

          state.identityFlow = true
          state.links = []
          state.providers = [
            {
              id: "provider-1",
              name: "Test identity provider",
              tenant_id: "t1",
              provider_type: "oidc",
            },
          ]
          await page.goto("/me/identities")
          await page.locator("#identity-provider").selectOption("provider-1")
          await button("linkIdentity").click()
          await verify()
          await button("unlinkIdentity").waitFor()
          await visible(page, "External person")
          assert.equal(new URL(page.url()).search, "")
          state.actionConflict = true
          await button("unlinkIdentity").click()
          await verify()
          await visible(page, "failure 409")
          assert.equal(state.links.length, 1)
          state.actionConflict = false
          await verify()
          await visible(page, messages.noLinkedIdentities)
          await page.reload()
          await visible(page, messages.noLinkedIdentities)

          state.closureQueue = [
            { principal_id: "requester-one", status: "pending", version: 1 },
            { principal_id: "requester-two", status: "pending", version: 3 },
          ]
          await page.goto("/system/closure-requests")
          await visible(page, "requester-one")
          await button("rejectClosureRequest").first().click()
          state.actionConflict = true
          await page
            .getByRole("dialog")
            .getByRole("button", {
              name: messages.rejectClosureRequest,
              exact: true,
            })
            .click()
          await visible(page, "failure 409")
          assert.equal(state.closureQueue[0].status, "pending")
          state.actionConflict = false
          await page
            .getByRole("dialog")
            .getByRole("button", {
              name: messages.rejectClosureRequest,
              exact: true,
            })
            .click()
          await page.getByRole("dialog").waitFor({ state: "hidden" })
          await button("processClosureRequest").click()
          assert(
            (await page.getByRole("dialog").innerText()).includes(
              "requester-two"
            )
          )
          await page
            .getByRole("dialog")
            .getByRole("button", {
              name: messages.processClosureRequest,
              exact: true,
            })
            .click()
          await page.getByRole("dialog").waitFor({ state: "hidden" })
          await visible(page, "requester-two")
          assert.equal(state.closureQueue[1].status, "processing")
          assert.equal(await button("processClosureRequest").count(), 0)
          await button("retentionErasure").waitFor()
          assert(
            await page.evaluate(
              () => document.documentElement.scrollWidth <= innerWidth
            )
          )
          for (const request of state.requests.filter(
            (item) =>
              item.path.startsWith("/system/") ||
              item.path.startsWith(`/principals/${state.subject}/`)
          )) {
            assert.equal(request.tenant, undefined)
          }
        },
        { locale, mobile }
      )
    }
  for (const locale of ["en-US", "zh-CN", "ms-MY"]) {
    const messages = JSON.parse(
      readFileSync(
        new URL(`../src/messages/${locale}.json`, import.meta.url),
        "utf8"
      )
    )
    await scenario(
      `nested error localization ${locale}`,
      async (page, state) => {
        state.failureMessage = "forbidden"
        state.entitiesStatus = 403
        await page.goto("/tenants/t1/entities")
        await page
          .getByRole("alert")
          .filter({ hasText: messages.forbidden })
          .first()
          .waitFor()
        assert.equal(
          await page
            .getByRole("alert")
            .getByText("forbidden", { exact: true })
            .count(),
          0
        )
        state.consentStatus = 403
        await page.goto("/me/applications")
        await page
          .getByRole("alert")
          .filter({ hasText: messages.forbidden })
          .first()
          .waitFor()
        state.entitiesStatus = 200
        state.menusStatus = 403
        await page.goto("/")
        await page
          .getByRole("alert")
          .filter({ hasText: messages.forbidden })
          .first()
          .waitFor()
      },
      { locale }
    )
  }
  console.log(`PASS ${completed} browser regression scenarios`)
} finally {
  await browser.close()
}
