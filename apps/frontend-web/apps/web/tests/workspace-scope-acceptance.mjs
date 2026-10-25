// Live acceptance against an isolated development backend; no HTTP mocks.
// Read-only by default; --seed-tenant creates an isolated tenant and two instances.
import assert from "node:assert/strict"
import { chromium } from "playwright"

const baseURL = process.env.IAM_CONSOLE_URL ?? "http://127.0.0.1:4317"
assert(["127.0.0.1", "localhost"].includes(new URL(baseURL).hostname))
const browser = await chromium.launch({ channel: "chrome", headless: true })
const errors = []
try {
  const context = await browser.newContext({
    baseURL,
    viewport: { width: 1366, height: 768 },
  })
  await context.addCookies([
    { name: "NEXT_LOCALE", value: "en-US", url: baseURL },
  ])
  const page = await context.newPage()
  page.on("pageerror", (error) => errors.push(error.message))
  await page.goto("/admin/login")
  const preferredTenant = await page.evaluate(
    async () =>
      (await import("/src/lib/dev-login-accounts.ts")).devAccountsForEntrance(
        "tenant"
      )[0]?.tenantID
  )
  // The existing development selector pre-fills the demo account; never read or log its password.
  await page.getByRole("button", { name: "Sign in", exact: true }).click()
  await page.waitForURL((url) => url.pathname !== "/admin/login")
  if (new URL(page.url()).pathname.endsWith("/workspaces")) {
    assert(preferredTenant, "demo account needs an initial tenant")
    await page.locator(`a[href="/admin/${preferredTenant}"]`).click()
  }
  const tenantSwitch = page.getByRole("button", {
    name: "Switch tenant",
    exact: true,
  })
  await tenantSwitch.waitFor()
  const tenant = new URL(page.url()).pathname.match(/^\/admin\/([^/]+)/)?.[1]
  assert(tenant && tenant !== "login")
  if (process.argv.includes("--seed-tenant")) {
    const principal = await page.evaluate(
      async () =>
        (await (await import("/src/lib/iam-api.ts")).iamApi.session()).subject
    )
    const systemContext = await browser.newContext({ baseURL })
    await systemContext.addCookies([
      { name: "NEXT_LOCALE", value: "en-US", url: baseURL },
    ])
    const systemPage = await systemContext.newPage()
    await systemPage.goto("/system/login")
    await systemPage
      .getByRole("button", { name: "Sign in", exact: true })
      .click()
    await systemPage.waitForURL((url) => url.pathname === "/system")
    const seeded = await systemPage.evaluate(async (owner) => {
      const { iamApi } = await import("/src/lib/iam-api.ts")
      return (
        await iamApi.createTenant({
          name: "Workspace scope acceptance",
          slug: `scope-acceptance-${Date.now()}`,
          owner_principal_id: owner,
        })
      ).id
    }, principal)
    await systemContext.close()
    await page.evaluate(async (owner) => {
      const { createIamApi } = await import("/src/lib/iam-api.ts")
      const api = createIamApi(undefined, () => owner)
      for (const name of ["Scope acceptance A", "Scope acceptance B"]) {
        await api.createEntity({
          name,
          type: "company",
          status: "active",
          metadata: {},
        })
      }
    }, seeded)
    console.log(
      `Created isolated acceptance tenant ${seeded} with two instances`
    )
    await page.reload()
    await tenantSwitch.waitFor()
  }
  const instanceSwitch = () =>
    page.getByRole("button", { name: "Switch instance", exact: true })
  const areas = page.getByRole("navigation", {
    name: "Workspace areas",
    exact: true,
  })
  await areas.getByRole("link", { name: "Collaboration", exact: true }).click()
  await page.getByRole("region", { name: "Files", exact: true }).waitFor()
  await instanceSwitch().click()
  const picker = page.getByRole("region", {
    name: "Instance scope",
    exact: true,
  })
  const names = (await picker.getByRole("button").allTextContents()).filter(
    (name) => name !== "Tenant overview"
  )
  assert(
    names.length >= 2,
    "isolated acceptance needs two existing admitted instances"
  )
  await picker.getByRole("button", { name: names[0], exact: true }).click()
  await page.waitForURL(new RegExp(`/admin/${tenant}/entities/[^/]+/files$`))
  const first = page.url()
  const search = page.getByRole("textbox", {
    name: "Search this page",
    exact: true,
  })
  await search.fill("scope-history-marker")
  await page.waitForURL(/q=scope-history-marker/)
  await areas.getByRole("link", { name: "Team", exact: true }).click()
  await page.waitForURL(/\/departments$/)
  await page.goBack()
  await page.waitForURL(/\/files\?q=scope-history-marker$/)
  await page.waitForFunction(
    () =>
      document.querySelector('input[placeholder="Search this page"]')?.value ===
      "scope-history-marker"
  )
  await instanceSwitch().click()
  await picker.getByRole("button", { name: names[1], exact: true }).click()
  await page.waitForURL(
    (url) =>
      url.pathname.endsWith("/files") && !url.search && url.href !== first
  )
  await page.waitForFunction(
    () =>
      document.querySelector('input[placeholder="Search this page"]')?.value ===
      ""
  )
  assert.equal(
    await search.inputValue(),
    "",
    "scope switch transplanted another instance's filters"
  )
  assert.equal(
    await areas.locator('[aria-current="page"]').textContent(),
    "Collaboration"
  )
  await instanceSwitch().click()
  await picker
    .getByRole("button", { name: "Tenant overview", exact: true })
    .click()
  await page.waitForURL(new RegExp(`/admin/${tenant}/files$`))
  assert.equal(
    await areas
      .locator('[aria-disabled="true"]')
      .filter({ hasText: "Conversation" })
      .count(),
    1
  )
  await areas.getByRole("link", { name: "Governance", exact: true }).click()
  await page.waitForURL((url) => !url.pathname.endsWith("/files"))
  await page.waitForFunction(
    () =>
      document.querySelector('button[aria-label="Switch instance"]')?.disabled
  )
  assert(
    await instanceSwitch().isDisabled(),
    "tenant-only resource allowed an incompatible instance switch"
  )
  await areas.getByRole("link", { name: "Workbench", exact: true }).click()
  await instanceSwitch().click()
  await picker.getByRole("button", { name: names[0], exact: true }).click()
  await page.waitForURL(
    /\/entities\/[^/]+\/(project|overview|files|departments)$/
  )
  const instanceBase = new URL(page.url()).pathname.replace(/\/[^/]+$/, "")
  await page.goto(`${instanceBase}/manage`)
  await instanceSwitch().waitFor()
  await instanceSwitch().click()
  await picker
    .getByRole("button", { name: "Tenant overview", exact: true })
    .click()
  await page.waitForURL(new RegExp(`/admin/${tenant}/entities$`))
  const rejection = await context.request.get(
    `/api/tenants/${tenant}/entities/1/project`
  )
  assert(
    [403, 404].includes(rejection.status()),
    `invalid entity boundary returned ${rejection.status()}`
  )
  for (const width of [1366, 375]) {
    await page.setViewportSize({ width, height: 768 })
    await page.goto(`/admin/${tenant}/files`)
    await page.getByRole("region", { name: "Files", exact: true }).waitFor()
    assert(
      await page.evaluate(
        () => document.documentElement.scrollWidth <= innerWidth
      ),
      `document overflow at ${width}`
    )
    if (width === 375) {
      await page
        .getByRole("button", { name: "Open main navigation", exact: true })
        .click()
      const drawer = page.getByRole("dialog")
      await drawer
        .getByRole("button", { name: "Switch instance", exact: true })
        .click()
      await picker
        .getByRole("button", { name: names[0], exact: true })
        .waitFor()
      await page.keyboard.press("Escape")
      await page.keyboard.press("Escape")
    }
  }
  await page.setViewportSize({ width: 1366, height: 768 })
  await page.goto(`/admin/${tenant}/files`)
  await tenantSwitch.click()
  const tenants = page.getByRole("region", {
    name: "Current tenant",
    exact: true,
  })
  const tenantNames = await tenants.getByRole("button").allTextContents()
  if (tenantNames.length >= 2) {
    const originalName = await tenantSwitch.textContent()
    const nextName = tenantNames.find(
      (name) => !name.includes(originalName.trim())
    )
    assert(nextName, "could not identify another tenant")
    await tenants.getByRole("button").nth(tenantNames.indexOf(nextName)).click()
    await page.waitForURL(
      (url) =>
        /^\/admin\/[^/]+\/files$/.test(url.pathname) &&
        !url.pathname.includes(tenant)
    )
    await page.getByRole("region", { name: "Files", exact: true }).waitFor()
    await instanceSwitch().click()
    await picker.getByRole("button").nth(2).waitFor()
    const nextInstances = await picker.getByRole("button").allTextContents()
    const admitted = nextInstances.filter((name) => name !== "Tenant overview")
    assert(admitted.length >= 2, "second tenant needs two admitted instances")
    assert(
      admitted.every((name) => !names.includes(name)),
      "old tenant instances leaked into new catalog"
    )
    await picker.getByRole("button", { name: admitted[0], exact: true }).click()
    await page.waitForURL(
      (url) =>
        /\/entities\/[^/]+\/files$/.test(url.pathname) &&
        !url.pathname.includes(tenant)
    )
    await page
      .getByRole("textbox", { name: "Search this page", exact: true })
      .waitFor()
    console.log(
      "PASS live cross-tenant switching: same file resource, independent instance catalogs, two instances per tenant"
    )
  } else {
    console.log(
      "NOT RUN live cross-tenant switching: only one tenant; use --seed-tenant on isolated data"
    )
  }
  assert.deepEqual(errors, [], "browser runtime errors")
  console.log(
    "PASS live workspace scopes: split selectors, same-resource switching, history filters, tenant-only guard, advanced-route compatibility, API refusal, desktop/mobile fit"
  )
} finally {
  await browser.close()
}
