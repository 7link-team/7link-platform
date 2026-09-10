import assert from "node:assert/strict"
import { readFileSync, mkdirSync, writeFileSync } from "node:fs"
import { randomBytes, createHash } from "node:crypto"
import { chromium } from "playwright"

// Real local IAM requests throughout, including the browser's reauthentication.
const directory = new URL(
  "../../../../backend-api/.temp/iam-uat/",
  import.meta.url
)
const settings = JSON.parse(readFileSync(new URL("state.json", directory)))
const baseURL = settings.webURL
assert.equal(new URL(baseURL).hostname, "127.0.0.1")
const output = new URL("browser/personal-lifecycle/", directory)
mkdirSync(output, { recursive: true, mode: 0o700 })
const report = {
  status: "RUNNING",
  binarySHA256: createHash("sha256")
    .update(readFileSync(new URL("api", directory)))
    .digest("hex"),
  results: [],
  cleanup: [],
}
const save = () =>
  writeFileSync(
    new URL("report.json", output),
    JSON.stringify(report, null, 2),
    { mode: 0o600 }
  )
const browser = await chromium.launch({
  channel: process.env.IAM_BROWSER_CHANNEL ?? "chrome",
  headless: true,
})
const admin = await browser.newContext({ baseURL })
const fixtures = []
report.fixtures = fixtures
save()
const password = randomBytes(32).toString("base64url") + "aA1!"
async function call(context, path, method = "GET", data, tenant) {
  return context.request.fetch("/api" + path, {
    method,
    ...(data === undefined ? {} : { data }),
    headers: { Origin: baseURL, ...(tenant ? { "X-Tenant-Id": tenant } : {}) },
  })
}
async function ok(context, path, method = "GET", data, tenant) {
  const response = await call(context, path, method, data, tenant)
  assert(response.ok(), `${method} ${path}: ${response.status()}`)
  return (await response.json()).data
}
async function login(context, name, secret) {
  await ok(context, "/authn/sessions", "POST", {
    login_name: name,
    password: secret,
    return_url: baseURL + "/",
  })
  return ok(context, "/authn/sessions/current")
}
async function verified(page, messages, responsePath) {
  const dialog = page.getByRole("dialog")
  await dialog.locator("#reauth-password").fill(password)
  const response = page.waitForResponse(
    (r) =>
      new URL(r.url()).pathname === "/api" + responsePath &&
      r.request().method() === "POST"
  )
  const [verifiedResponse] = await Promise.all([
    response,
    dialog
      .getByRole("button", { name: messages.continue, exact: true })
      .click(),
  ])
  assert.equal(verifiedResponse.status(), 200)
  await dialog.waitFor({ state: "hidden" })
}
try {
  const owner = await login(
    admin,
    "admin@example.test",
    readFileSync(new URL("admin-password", directory), "utf8").trim()
  )
  for (const locale of ["en-US", "zh-CN", "ms-MY"])
    for (const width of [1440, 390]) {
      const name = `closure-${Date.now()}-${width}`
      const tenant = await ok(admin, "/iam/tenants", "POST", {
        name,
        slug: name,
        owner_principal_id: owner.subject,
      })
      const fixture = { tenant: tenant.id }
      fixtures.push(fixture)
      save()
      const principal = await ok(
        admin,
        "/iam/principals",
        "POST",
        { login_name: name, display_name: name, password },
        tenant.id
      )
      fixture.principal = principal.id
      save()
      const context = await browser.newContext({
        baseURL,
        viewport: { width, height: 900 },
      })
      const reviewer = await browser.newContext({
        baseURL,
        viewport: { width, height: 900 },
      })
      const messages = JSON.parse(
        readFileSync(
          new URL(`../src/messages/${locale}.json`, import.meta.url),
          "utf8"
        )
      )
      try {
        await login(context, name, password)
        await login(
          reviewer,
          "admin@example.test",
          readFileSync(new URL("admin-password", directory), "utf8").trim()
        )
        for (const ctx of [context, reviewer])
          await ctx.addCookies([
            { name: "NEXT_LOCALE", value: locale, url: baseURL },
          ])
        const page = await context.newPage()
        page.setDefaultTimeout(15000)
        const errors = []
        page.on("pageerror", (e) => errors.push(e.message))
        await page.goto("/account/memberships")
        await page.getByRole("heading", { name, exact: true }).waitFor()
        await page
          .getByRole("button", { name: messages.leaveTenant, exact: true })
          .click()
        await verified(page, messages, `/iam/me/memberships/${tenant.id}/leave`)
        await page.getByText(messages.noMemberships, { exact: true }).waitFor()
        assert.equal(
          (await ok(context, "/authn/sessions/current")).subject,
          principal.id,
          "leaving preserves the global session"
        )
        const membership = await ok(context, "/iam/me/memberships")
        assert.equal(membership.items.length, 0)
        await page.goto("/account/closure")
        await page
          .getByRole("button", {
            name: messages.submitClosureRequest,
            exact: true,
          })
          .click()
        await verified(page, messages, "/iam/me/closure-request")
        await page
          .getByRole("heading", {
            name: messages.closureStatus_pending,
            exact: true,
          })
          .waitFor()
        assert.equal(
          (await ok(context, "/iam/me/closure-request")).status,
          "pending"
        )
        await page
          .getByRole("button", {
            name: messages.cancelClosureRequest,
            exact: true,
          })
          .click()
        await page
          .getByRole("heading", {
            name: messages.closureStatus_canceled,
            exact: true,
          })
          .waitFor()
        assert.equal(
          (await ok(context, "/iam/me/closure-request")).status,
          "canceled"
        )
        await page
          .getByRole("button", {
            name: messages.submitClosureRequest,
            exact: true,
          })
          .click()
        await verified(page, messages, "/iam/me/closure-request")
        const review = await reviewer.newPage()
        review.on("pageerror", (e) => errors.push(e.message))
        review.setDefaultTimeout(15000)
        await review.goto("/iam/closure-requests")
        const row = review.getByRole("listitem").filter({
          has: review.getByRole("heading", {
            name: principal.id,
            exact: true,
          }),
        })
        await row
          .getByRole("button", {
            name: messages.processClosureRequest,
            exact: true,
          })
          .click()
        const processing = review.waitForResponse(
          (r) =>
            new URL(r.url()).pathname.endsWith(
              `/principals/${principal.id}/closure-request/processing`
            ) && r.request().method() === "POST"
        )
        const [processedResponse] = await Promise.all([
          processing,
          review
            .getByRole("dialog")
            .getByRole("button", {
              name: messages.processClosureRequest,
              exact: true,
            })
            .click(),
        ])
        assert.equal(
          processedResponse.status(),
          200,
          "zero-tenant account can be processed"
        )
        assert.equal(
          (await call(context, "/authn/sessions/current")).status(),
          401,
          "processing revokes old session"
        )
        const requests = await ok(
          reviewer,
          "/platform/iam/closure-requests?status=processing"
        )
        assert(
          requests.items.some(
            (item) =>
              item.principal_id === principal.id && item.status === "processing"
          )
        )
        await review.waitForFunction(
          () => document.querySelector("select")?.value === "processing"
        )
        const [lifecycle] = await Promise.all([
          review.waitForResponse(
            (r) =>
              new URL(r.url()).pathname ===
                `/api/platform/iam/principals/${principal.id}/lifecycle` &&
              r.request().method() === "GET"
          ),
          row
            .getByRole("button", {
              name: messages.retentionErasure,
              exact: true,
            })
            .click(),
        ])
        assert.equal(
          lifecycle.status(),
          200,
          "zero-tenant lifecycle details remain accessible"
        )
        const details = (await lifecycle.json()).data
        assert.equal(details.suspended, true)
        assert.equal(
          details.anonymized,
          false,
          "processing does not silently erase the account"
        )
        await review
          .getByRole("dialog")
          .getByRole("definition")
          .filter({ hasText: name })
          .waitFor()
        assert.deepEqual(errors, [])
        for (const p of [page, review])
          assert(
            await p.evaluate(
              () => document.documentElement.scrollWidth <= innerWidth
            ),
            "no horizontal overflow"
          )
        const screenshot = `${locale}-${width}.png`
        await review.screenshot({
          path: new URL(screenshot, output).pathname,
          fullPage: true,
          animations: "disabled",
        })
        report.results.push({ locale, width, status: "PASS", screenshot })
        save()
      } finally {
        await context.close()
        await reviewer.close()
      }
    }
  assert.equal(report.results.length, 6)
  report.status = "PASS"
} catch (error) {
  report.status = "FAIL"
  report.error = error.message
  process.exitCode = 1
} finally {
  for (const fixture of fixtures) {
    try {
      if (fixture.principal)
        await ok(
          admin,
          `/iam/principals/${fixture.principal}/suspension`,
          "PUT",
          undefined,
          fixture.tenant
        )
      const tenants = await ok(admin, "/iam/tenants")
      const tenant = tenants.find((item) => item.id === fixture.tenant)
      assert(tenant)
      await ok(
        admin,
        `/iam/tenants/${tenant.id}?version=${tenant.version}`,
        "DELETE"
      )
      report.cleanup.push({ tenant: fixture.tenant, status: "PASS" })
    } catch (error) {
      report.cleanup.push({
        tenant: fixture.tenant,
        status: "FAIL",
        error: error.message,
      })
      report.status = "FAIL"
      process.exitCode = 1
    }
  }
  save()
  await admin.close()
  await browser.close()
}
console.log(
  JSON.stringify({
    status: report.status,
    cases: report.results.length,
    error: report.error,
  })
)
