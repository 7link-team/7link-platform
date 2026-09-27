import assert from "node:assert/strict"
import { randomBytes } from "node:crypto"
import { mkdirSync } from "node:fs"
import { fileURLToPath } from "node:url"
import { chromium } from "playwright"
import { auditPassword } from "../scripts/audit-password.mjs"

const tenant = process.env.IAM_BROWSER_TENANT
if (!tenant)
  throw new Error("IAM_BROWSER_TENANT must be a dedicated acceptance tenant")
const baseURL = (
  process.env.IAM_CONSOLE_URL ?? "http://localhost:8091"
).replace(/\/$/, "")
const browser = await chromium.launch({
  channel: process.env.IAM_BROWSER_CHANNEL ?? "chrome",
  headless: true,
})
const screenshots = new URL(
  "../../../../../.local/screenshots/session-tenant/",
  import.meta.url
)
mkdirSync(screenshots, { recursive: true })
const admin = await browser.newContext({ baseURL })
async function api(context, path, method = "GET", body, scope = "") {
  const requestPath = scope ? `/tenants/${scope}${path}` : path
  const response = await context.request.fetch("/api" + requestPath, {
    method,
    headers: { Origin: baseURL },
    data: body,
  })
  const result = await response.json()
  return { status: response.status(), data: result.data }
}
async function ok(context, path, method, body, scope) {
  const result = await api(context, path, method, body, scope)
  assert(
    result.status >= 200 && result.status < 300,
    `${method ?? "GET"} ${path}: ${result.status}`
  )
  return result.data
}
async function expectTenantWorkspace(page, tenantID) {
  await page
    .getByRole("button", { name: "切换工作空间", exact: true })
    .waitFor()
  await page
    .locator('[aria-label="当前工作区"]')
    .getByText(tenantID, { exact: true })
    .waitFor()
}
const errors = []
try {
  await ok(admin, "/login", "POST", {
    login_name: process.env.IAM_BROWSER_LOGIN ?? "admin@example.test",
    password: auditPassword(),
    return_url: baseURL + "/",
  })
  const stamp = Date.now()
  const memberPassword = randomBytes(32).toString("base64url")
  const actor = await ok(
    admin,
    "/principals",
    "POST",
    {
      login_name: `session-member-${stamp}`,
      display_name: "Session acceptance member",
      password: memberPassword,
      email: `session-member-${stamp}@example.test`,
    },
    tenant
  )
  const roles = await ok(admin, "/roles", "GET", undefined, tenant)
  const role = roles.find((item) => item.name === "Administrator")
  assert(role)
  await ok(admin, `/roles/${role.id}/members/${actor.id}`, "PUT", {}, tenant)
  let lastContext
  for (const [name, viewport] of [
    ["desktop", { width: 1440, height: 1000 }],
    ["mobile", { width: 390, height: 844 }],
  ]) {
    const context = await browser.newContext({ baseURL, viewport })
    await context.addCookies([
      { name: "NEXT_LOCALE", value: "zh-CN", url: baseURL },
    ])
    lastContext = context
    const page = await context.newPage()
    page.on("pageerror", (error) => errors.push(error.message))
    let unscopedSession = false
    let derivedMenu = false
    page.on("request", (request) => {
      if (request.url().endsWith("/api/me")) {
        assert(!new URL(request.url()).pathname.includes("/tenants/"))
        unscopedSession = true
      }
      if (
        request
          .url()
          .includes(`/api/tenants/${tenant}/principals/${actor.id}/menus`)
      )
        derivedMenu = true
    })
    assert.deepEqual((await context.storageState()).origins, [])
    await page.goto("/user/login")
    await page.locator('input[name="login_name"]').fill(actor.login_name)
    await page.locator('input[name="password"]').fill(memberPassword)
    const menusLoaded = page.waitForResponse(
      (response) =>
        response
          .url()
          .includes(`/api/tenants/${tenant}/principals/${actor.id}/menus`) &&
        response.status() === 200
    )
    await page.locator('button[type="submit"]').click()
    await expectTenantWorkspace(page, tenant)
    assert(unscopedSession)
    await menusLoaded
    assert(derivedMenu)
    await page.locator("main article").nth(3).waitFor()
    assert.equal((await ok(context, "/me")).organization_id, tenant)
    assert.equal(
      (await api(context, "/tenants")).status,
      403,
      "member must not be a platform administrator"
    )
    assert(
      await page.evaluate(
        () => document.documentElement.scrollWidth <= innerWidth
      )
    )
    await page.screenshot({
      path: fileURLToPath(new URL(`${name}.png`, screenshots)),
      fullPage: true,
    })
  }
  const owner = await ok(admin, "/me")
  const second = await ok(admin, "/tenants", "POST", {
    slug: `session-second-${stamp}`,
    name: "Session second tenant",
    owner_principal_id: owner.subject,
  })
  await ok(
    admin,
    "/members",
    "POST",
    {
      subject: actor.id,
      display_name: actor.display_name,
      email: actor.email,
      status: "active",
    },
    second.id
  )
  assert.equal((await ok(lastContext, "/me")).organization_id, undefined)
  const ambiguous = await browser.newContext({
    baseURL,
    viewport: { width: 1440, height: 1000 },
  })
  await ambiguous.addCookies(await lastContext.cookies())
  const page = await ambiguous.newPage()
  page.on("pageerror", (error) => errors.push(error.message))
  await page.goto("/")
  const switcher = page.getByRole("button", {
    name: "切换工作空间",
    exact: true,
  })
  await switcher.waitFor()
  await page
    .locator('[aria-label="当前工作区"]')
    .getByText("未选择", { exact: true })
    .waitFor()
  await switcher.click()
  const tenantInput = page.getByRole("dialog").locator("#workspace-tenant-id")
  await tenantInput.fill(tenant)
  await tenantInput.press("Enter")
  await expectTenantWorkspace(page, tenant)
  await page.reload()
  await expectTenantWorkspace(page, tenant)
  assert.equal(
    (await ok(ambiguous, "/me", "GET", undefined, second.id)).organization_id,
    second.id
  )
  assert.equal((await ok(ambiguous, "/me")).organization_id, undefined)
  await ok(
    admin,
    `/members/${actor.id}`,
    "PATCH",
    { status: "disabled" },
    tenant
  )
  assert.equal(
    (await api(ambiguous, "/me", "GET", undefined, tenant)).status,
    403
  )
  assert.equal((await ok(ambiguous, "/me")).organization_id, second.id)
  assert.deepEqual(errors, [])
  console.log(
    "PASS: unconfigured tenant member login, session adoption, desktop/mobile, ambiguous membership, explicit selection, revocation"
  )
} finally {
  await browser.close()
}
