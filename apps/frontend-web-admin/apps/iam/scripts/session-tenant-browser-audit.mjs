import assert from "node:assert/strict"
import { randomBytes } from "node:crypto"
import { mkdirSync } from "node:fs"
import { fileURLToPath } from "node:url"
import { chromium } from "playwright"
import { auditPassword } from "./audit-password.mjs"

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
  const response = await context.request.fetch("/api" + path, {
    method,
    headers: { Origin: baseURL, ...(scope ? { "X-Tenant-Id": scope } : {}) },
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
const errors = []
try {
  await ok(admin, "/authn/sessions", "POST", {
    login_name: process.env.IAM_BROWSER_LOGIN ?? "admin@example.test",
    password: auditPassword(),
    return_url: baseURL + "/",
  })
  const stamp = Date.now()
  const memberPassword = randomBytes(32).toString("base64url")
  const actor = await ok(
    admin,
    "/iam/principals",
    "POST",
    {
      login_name: `session-member-${stamp}`,
      display_name: "Session acceptance member",
      password: memberPassword,
      email: `session-member-${stamp}@example.test`,
    },
    tenant
  )
  const roles = await ok(admin, "/iam/roles", "GET", undefined, tenant)
  const role = roles.find((item) => item.name === "Administrator")
  assert(role)
  await ok(
    admin,
    `/iam/roles/${role.id}/members/${actor.id}`,
    "PUT",
    {},
    tenant
  )
  let lastContext
  for (const [name, viewport] of [
    ["desktop", { width: 1440, height: 1000 }],
    ["mobile", { width: 390, height: 844 }],
  ]) {
    const context = await browser.newContext({ baseURL, viewport })
    lastContext = context
    const page = await context.newPage()
    page.on("pageerror", (error) => errors.push(error.message))
    let unscopedSession = false
    let derivedMenu = false
    page.on("request", (request) => {
      if (request.url().endsWith("/api/authn/sessions/current")) {
        assert.equal(request.headers()["x-tenant-id"], undefined)
        unscopedSession = true
      }
      if (request.url().includes("/api/iam/me/menus")) {
        if (request.headers()["x-tenant-id"] === tenant) derivedMenu = true
      }
    })
    assert.deepEqual((await context.storageState()).origins, [])
    await page.goto("/login")
    await page.locator('input[name="login_name"]').fill(actor.login_name)
    await page.locator('input[name="password"]').fill(memberPassword)
    const menusLoaded = page.waitForResponse(
      (response) =>
        response.url().includes("/api/iam/me/menus") &&
        response.status() === 200
    )
    await page.locator('button[type="submit"]').click()
    await page.locator("#tenant").waitFor()
    await page.waitForFunction(
      (expected) => document.querySelector("#tenant")?.value === expected,
      tenant
    )
    assert(unscopedSession)
    await menusLoaded
    assert(derivedMenu)
    await page.locator("main article").nth(3).waitFor()
    assert.equal(
      (await ok(context, "/authn/sessions/current")).organization_id,
      tenant
    )
    assert.equal(
      (await api(context, "/iam/tenants")).status,
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
  const owner = await ok(admin, "/authn/sessions/current")
  const second = await ok(admin, "/iam/tenants", "POST", {
    slug: `session-second-${stamp}`,
    name: "Session second tenant",
    owner_principal_id: owner.subject,
  })
  await ok(
    admin,
    "/iam/members",
    "POST",
    {
      subject: actor.id,
      display_name: actor.display_name,
      email: actor.email,
      status: "active",
    },
    second.id
  )
  assert.equal(
    (await ok(lastContext, "/authn/sessions/current")).organization_id,
    undefined
  )
  const ambiguous = await browser.newContext({
    baseURL,
    viewport: { width: 1440, height: 1000 },
  })
  await ambiguous.addCookies(await lastContext.cookies())
  const page = await ambiguous.newPage()
  page.on("pageerror", (error) => errors.push(error.message))
  await page.goto("/")
  await page.locator("#tenant").waitFor()
  assert.equal(
    await page.locator("#tenant").inputValue(),
    "",
    "multiple memberships must not pick the first tenant"
  )
  await page.locator("#tenant").fill(tenant)
  await page.locator("#tenant").press("Enter")
  await page.reload()
  await page.locator("#tenant").waitFor()
  assert.equal(
    await page.locator("#tenant").inputValue(),
    tenant,
    "refresh must preserve explicit selection"
  )
  assert.equal(
    (await ok(ambiguous, "/authn/me", "GET", undefined, second.id))
      .organization_id,
    second.id
  )
  assert.equal(
    (await ok(ambiguous, "/authn/sessions/current")).organization_id,
    undefined
  )
  await ok(
    admin,
    `/iam/members/${actor.id}`,
    "PATCH",
    { status: "disabled" },
    tenant
  )
  assert.equal(
    (await api(ambiguous, "/authn/sessions/current", "GET", undefined, tenant))
      .status,
    403
  )
  assert.equal(
    (await ok(ambiguous, "/authn/sessions/current")).organization_id,
    second.id
  )
  assert.deepEqual(errors, [])
  console.log(
    "PASS: unconfigured tenant member login, session adoption, desktop/mobile, ambiguous membership, explicit selection, revocation"
  )
} finally {
  await browser.close()
}
