// Real isolated API acceptance. The transport fault occurs after a real commit.
// Created projects remain available for manual acceptance; credentials are never logged.
import assert from "node:assert/strict"
import { chromium } from "playwright"

const baseURL = process.env.IAM_CONSOLE_URL ?? "http://127.0.0.1:4317"
assert(["localhost", "127.0.0.1"].includes(new URL(baseURL).hostname))
const browser = await chromium.launch({ channel: "chrome", headless: true })
const errors = []
async function login(accountID = "tenant-admin") {
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
  const account = await page.evaluate(async (id) => {
    const accounts = (
      await import("/src/lib/dev-login-accounts.ts")
    ).devAccountsForEntrance("tenant")
    const found = accounts.find((account) => account.id === id)
    return found ? { role: found.role, tenant: found.tenantID } : null
  }, accountID)
  assert(account?.tenant, "existing development persona is required")
  if (accountID !== "tenant-admin") {
    await page.getByRole("combobox").click()
    await page.getByRole("option").filter({ hasText: account.role }).click()
  }
  await page.getByRole("button", { name: "Sign in", exact: true }).click()
  await page.waitForURL((url) => url.pathname !== "/admin/login")
  // A single workspace redirects automatically; multiple workspaces render links.
  const tenantSwitch = page.getByRole("button", {
    name: "Switch tenant",
    exact: true,
  })
  const tenantLink = page.locator(`a[href="/admin/${account.tenant}"]`)
  await tenantSwitch.or(tenantLink).first().waitFor()
  if (await tenantLink.isVisible()) await tenantLink.click()
  await page
    .getByRole("button", { name: "Switch tenant", exact: true })
    .waitFor()
  return { context, page, tenant: account.tenant }
}
async function checkFit(page) {
  assert(
    await page.evaluate(
      () => document.documentElement.scrollWidth <= innerWidth
    ),
    "page overflow"
  )
  const box = await page.getByRole("dialog").boundingBox()
  const size = page.viewportSize()
  assert(
    box &&
      box.x >= 0 &&
      box.y >= 0 &&
      box.x + box.width <= size.width + 1 &&
      box.y + box.height <= size.height + 1,
    "creation dialog outside viewport"
  )
}
try {
  const { context, page, tenant } = await login()
  const code = `B${Date.now().toString(36).toUpperCase()}`
  const name = `Creation acceptance ${code}`
  const endpoint = `/api/tenants/${tenant}/projects`
  const posts = []
  let failMetadata = true
  let loseCommittedResponse = true
  await page.route(new RegExp(`${endpoint}(?:\\?|$)`), async (route) => {
    const request = route.request()
    if (request.method() === "OPTIONS" && failMetadata) {
      return route.fulfill({
        status: 503,
        json: { code: 1, message: "Acceptance transport fault" },
      })
    }
    if (request.method() !== "POST") return route.continue()
    const response = await route.fetch()
    posts.push({
      key: request.headers()["idempotency-key"],
      body: request.postDataJSON(),
      status: response.status(),
      replay: response.headers()["idempotent-replayed"],
      data: (await response.json()).data,
    })
    if (loseCommittedResponse && response.ok()) {
      loseCommittedResponse = false
      return route.abort("connectionreset")
    }
    return route.fulfill({ response })
  })
  await page
    .getByRole("button", { name: "Create project", exact: true })
    .click()
  const dialog = page.getByRole("dialog")
  const submit = dialog.getByRole("button", {
    name: "Create project",
    exact: true,
  })
  await dialog.getByRole("alert").waitFor()
  assert(await submit.isDisabled(), "unavailable contract allowed creation")
  failMetadata = false
  await dialog.getByRole("button", { name: /Retry/ }).click()
  await page.waitForFunction(
    () =>
      !document.querySelector('button[form="project-create-form"]')?.disabled
  )
  await dialog.getByRole("textbox", { name: "Name", exact: true }).fill(name)
  await dialog
    .getByRole("textbox", { name: "Project code", exact: true })
    .fill("invalid")
  await dialog
    .getByRole("textbox", { name: "Description", exact: true })
    .fill("Created through the existing atomic project command.")
  await submit.click()
  await dialog.locator('input[name="code"][aria-invalid="true"]').waitFor()
  assert.equal(posts.length, 0, "invalid code reached the write command")
  await dialog
    .getByRole("textbox", { name: "Project code", exact: true })
    .fill(code)
  await page.setViewportSize({ width: 375, height: 768 })
  await checkFit(page)
  // Both events arrive while the same submit is in flight.
  await dialog.locator("form").evaluate((form) => {
    form.requestSubmit()
    form.requestSubmit()
  })
  await dialog.getByRole("alert").first().waitFor()
  assert.equal(posts.length, 1, "duplicate submission wrote more than once")
  assert.equal(
    posts[0].status,
    201,
    "fault must follow a real successful commit"
  )
  assert.equal(
    await dialog
      .getByRole("textbox", { name: "Name", exact: true })
      .inputValue(),
    name
  )
  assert.equal(
    await dialog
      .getByRole("textbox", { name: "Project code", exact: true })
      .inputValue(),
    code
  )
  await submit.click()
  await page.waitForURL(/\/entities\/[1-9][0-9]*\/project$/)
  assert.equal(posts.length, 2)
  assert.equal(
    posts[0].key,
    posts[1].key,
    "unchanged retry changed its replay key"
  )
  assert.equal(
    posts[1].replay,
    "true",
    "server did not replay the committed command"
  )
  const created = posts[1].data
  const id = created.project.project_id
  assert.equal(id, created.entity.id)
  assert.equal(
    created.entity.parent_id,
    null,
    "new instance acquired a customer parent"
  )
  assert(created.channel_id, "atomic creation omitted its channel")
  assert(
    created.role_bindings.some(
      (binding) => binding.entity_id === id && binding.effect === "allow"
    )
  )
  await page
    .getByRole("region", { name: "Project details", exact: true })
    .waitFor()
  await page.reload()
  await page
    .getByRole("region", { name: "Project details", exact: true })
    .waitFor()
  await page.setViewportSize({ width: 1366, height: 768 })
  await page
    .getByRole("button", { name: "Switch instance", exact: true })
    .click()
  await page
    .getByRole("region", { name: "Instance scope", exact: true })
    .getByRole("button", { name, exact: true })
    .waitFor()
  await page.keyboard.press("Escape")
  await page
    .getByRole("navigation", { name: "Workspace areas", exact: true })
    .getByRole("link", { name: "Collaboration", exact: true })
    .click()
  await page.waitForURL(new RegExp(`/entities/${id}/work-items`))
  await page
    .getByRole("button", { name: "New work item", exact: true })
    .waitFor()
  await page.locator(`a[href="/admin/${tenant}/entities/${id}/files"]`).click()
  await page.waitForURL(new RegExp(`/entities/${id}/files`))
  await page
    .getByRole("navigation", { name: "File roots", exact: true })
    .getByRole("button", { name, exact: true })
    .waitFor()
  const permissions = await page.evaluate(
    async (args) =>
      (await import("/src/lib/iam-api.ts")).iamApi.myPermissions(
        args.tenant,
        args.id
      ),
    { tenant, id }
  )
  assert(
    permissions.includes("project_read") && permissions.includes("file_view"),
    "creator lacks project/file access"
  )
  await page.goto(`/admin/${tenant}/projects`)
  await page
    .getByRole("button", { name: "Create project", exact: true })
    .click()
  await dialog
    .getByRole("textbox", { name: "Name", exact: true })
    .fill("Duplicate code attempt")
  await dialog
    .getByRole("textbox", { name: "Project code", exact: true })
    .fill(code)
  await submit.click()
  await dialog.getByRole("alert").first().waitFor()
  assert.equal(
    posts.at(-1).status,
    409,
    "duplicate code was not rejected by the real server"
  )
  assert.notEqual(
    posts.at(-1).key,
    posts[1].key,
    "new draft reused an earlier command key"
  )
  assert.equal(
    await dialog
      .getByRole("textbox", { name: "Name", exact: true })
      .inputValue(),
    "Duplicate code attempt"
  )
  await checkFit(page)
  await dialog.getByRole("button", { name: "Cancel", exact: true }).click()
  const list = await context.request.get(`${endpoint}?code=eq:${code}`)
  assert.equal(list.status(), 200)
  assert.equal(
    (await list.json()).meta.page.total,
    1,
    "retry or duplicate attempt created another project"
  )
  const member = await login("entity-account")
  await member.page.goto(`/admin/${member.tenant}/projects`)
  await member.page
    .getByRole("heading", { name: "Projects", exact: true })
    .waitFor()
  assert.equal(
    await member.page
      .getByRole("button", { name: "Create project", exact: true })
      .count(),
    0
  )
  const forbidden = await member.context.request.post(
    `/api/tenants/${member.tenant}/projects`,
    {
      data: { name: "Forbidden", code: `X${code}` },
      headers: { Origin: baseURL, "Idempotency-Key": crypto.randomUUID() },
    }
  )
  assert.equal(
    forbidden.status(),
    403,
    "member bypassed project creation permission"
  )
  assert.deepEqual(errors, [], "browser runtime errors")
  console.log(
    `PASS real project creation ${id}: contract retry, validation, duplicate submit, committed-response loss/replay, root identity, bindings/channel, reload/catalog/files, conflict/input preservation, permission refusal, mobile/desktop`
  )
} finally {
  await browser.close()
}
