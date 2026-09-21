import assert from "node:assert/strict"
import { randomBytes } from "node:crypto"
import { mkdirSync } from "node:fs"
import { fileURLToPath } from "node:url"
import { chromium } from "playwright"
import { auditPassword } from "./audit-password.mjs"

const baseURL = (
  process.env.IAM_CONSOLE_URL ?? "http://localhost:8091"
).replace(/\/$/, "")
const screenshots = new URL(
  "../../../../../.local/screenshots/profile/",
  import.meta.url
)
mkdirSync(screenshots, { recursive: true })
const browser = await chromium.launch({
  channel: process.env.IAM_BROWSER_CHANNEL ?? "chrome",
  headless: true,
})
const admin = await browser.newContext({ baseURL })
async function api(context, path, method = "GET", body, tenant = "") {
  const requestPath =
    tenant && !path.startsWith("/login") && !path.startsWith("/platform/")
      ? `/tenants/${tenant}${path}`
      : path
  const response = await context.request.fetch("/api" + requestPath, {
    method,
    data: body,
    headers: { Origin: baseURL },
  })
  const result = await response.json()
  return { status: response.status(), data: result.data }
}
async function ok(...args) {
  const result = await api(...args)
  assert(
    result.status >= 200 && result.status < 300,
    `${args[2] ?? "GET"} ${args[1]}: ${result.status}`
  )
  return result.data
}
async function fits(page) {
  await page.evaluate(() =>
    Promise.all(
      document
        .getAnimations()
        .map((animation) => animation.finished.catch(() => {}))
    )
  )
  assert(
    await page.evaluate(
      () => document.documentElement.scrollWidth <= innerWidth
    ),
    "page overflows horizontally"
  )
  const dialog = page.getByRole("dialog")
  if (await dialog.count()) {
    const box = await dialog.boundingBox()
    const viewport = page.viewportSize()
    assert(
      box.x >= 0 &&
        box.y >= 0 &&
        box.x + box.width <= viewport.width + 1 &&
        box.y + box.height <= viewport.height + 1,
      "dialog must fit the viewport"
    )
  }
}
const errors = []
try {
  await ok(admin, "/login", "POST", {
    login_name: process.env.IAM_BROWSER_LOGIN ?? "admin@example.test",
    password: auditPassword(),
    return_url: baseURL + "/",
  })
  const me = await ok(admin, "/me/session")
  const stamp = Date.now()
  const tenant = await ok(admin, "/tenants", "POST", {
    slug: `profile-audit-${stamp}`,
    name: "Profile acceptance",
    owner_principal_id: me.subject,
  })
  const password = randomBytes(32).toString("base64url")
  const actor = await ok(
    admin,
    "/principals",
    "POST",
    {
      login_name: `profile-actor-${stamp}`,
      password,
      display_name: "Profile operator",
      email: `operator-${stamp}@example.test`,
    },
    tenant.id
  )
  const target = await ok(
    admin,
    "/principals",
    "POST",
    {
      login_name: `profile-target-${stamp}`,
      password,
      display_name: "Profile target",
      email: `private-${stamp}@example.test`,
    },
    tenant.id
  )
  const roles = await ok(admin, "/roles", "GET", undefined, tenant.id)
  const adminRole = roles.find((role) => role.name === "Administrator")
  assert(adminRole)
  await ok(
    admin,
    `/roles/${adminRole.id}/members/${actor.id}`,
    "PUT",
    {},
    tenant.id
  )
  const restricted = await ok(
    admin,
    "/roles",
    "POST",
    { name: "Restricted profile operator", description: "Browser acceptance" },
    tenant.id
  )
  await ok(
    admin,
    `/roles/${restricted.id}/members/${actor.id}`,
    "PUT",
    {},
    tenant.id
  )
  let version = 0
  for (const [name, viewport, mask] of [
    ["desktop", { width: 1440, height: 1000 }, "redact"],
    ["mobile", { width: 390, height: 844 }, "omit"],
  ]) {
    const policy = await ok(
      admin,
      "/field-policies/user_profile/email",
      "PUT",
      {
        version,
        policy: {
          read: {
            default: "allow",
            allow_roles: [],
            deny_roles: [restricted.id],
          },
          write: {
            default: "allow",
            allow_roles: [],
            deny_roles: [restricted.id],
          },
          mask,
        },
      },
      tenant.id
    )
    version = policy.version
    const context = await browser.newContext({ baseURL, viewport })
    await context.addCookies([
      { name: "NEXT_LOCALE", value: "zh-CN", url: baseURL },
    ])
    const page = await context.newPage()
    page.on("pageerror", (error) => errors.push(error.message))
    await page.goto("/login")
    await page.locator('input[name="login_name"]').fill(actor.login_name)
    await page.locator('input[name="password"]').fill(password)
    await page.locator('button[type="submit"]').click()
    await page
      .getByRole("button", { name: "切换工作空间", exact: true })
      .waitFor()
    await page
      .locator('[aria-label="当前工作区"]')
      .getByText(tenant.id, { exact: true })
      .waitFor()
    await page.goto("/users")
    const row = page
      .getByRole("row")
      .filter({ has: page.getByText(target.login_name, { exact: true }) })
    await row.waitFor()
    assert(!(await row.innerText()).includes(target.email))
    assert.equal(await row.getByText("未验证", { exact: true }).count(), 0)
    if (mask === "redact")
      assert((await row.innerText()).includes("[REDACTED]"))
    await row.getByRole("button", { name: "编辑", exact: false }).click()
    const dialog = page.getByRole("dialog")
    await dialog.waitFor()
    assert(await dialog.locator('input[name="email"]').isDisabled())
    assert.equal(await dialog.locator('input[name="email"]').inputValue(), "")
    await fits(page)
    await page.screenshot({
      path: fileURLToPath(new URL(`${name}-editor.png`, screenshots)),
      fullPage: true,
    })
    const patchResponse = page.waitForResponse(
      (response) =>
        response.url().endsWith(`/principals/${target.id}`) &&
        response.request().method() === "PATCH"
    )
    await dialog
      .locator('input[name="display_name"]')
      .fill(`Profile target ${name}`)
    await dialog.getByRole("button", { name: "保存", exact: true }).click()
    const saved = await patchResponse
    assert.equal(saved.status(), 200)
    assert.deepEqual(saved.request().postDataJSON(), {
      display_name: `Profile target ${name}`,
    })
    await dialog.waitFor({ state: "hidden" })
    const raw = await ok(
      admin,
      `/principals/${target.id}`,
      "GET",
      undefined,
      tenant.id
    )
    assert.equal(
      raw.email,
      target.email,
      "saving another field preserves the hidden email"
    )
    assert.equal(
      (
        await api(
          context,
          `/principals/${target.id}`,
          "PATCH",
          { email: null },
          tenant.id
        )
      ).status,
      403
    )
    await fits(page)
    await page.locator("table").evaluate((table) => {
      table.parentElement.scrollLeft = 0
    })
    await page.screenshot({
      path: fileURLToPath(new URL(`${name}-list.png`, screenshots)),
      fullPage: true,
    })
    await page.goto("/account/profile")
    await page.getByRole("heading", { name: "主邮箱", exact: true }).waitFor()
    assert.equal(
      await page
        .getByRole("button", { name: "发送验证邮件", exact: true })
        .count(),
      0
    )
    assert(!(await page.locator("main").innerText()).includes(actor.email))
    await page.goto("/users")
    await row.waitFor()
    await row.getByRole("button", { name: "编辑", exact: false }).click()
    await dialog.waitFor()
    await page.evaluate(() => {
      localStorage.setItem("iam.tenant", "999999")
      window.dispatchEvent(new Event("tenant-change"))
    })
    await dialog.waitFor({ state: "hidden" })
    await page.waitForFunction(() => !document.querySelector("tbody tr"))
    console.log(
      `PASS ${name}: ${mask}, capability-aware editor, partial PATCH, raw preservation, hidden verification and tenant reset`
    )
    await context.close()
  }
  assert.deepEqual(errors, [])
  console.log(`PASS profile browser acceptance; isolated tenant ${tenant.id}`)
} finally {
  await browser.close()
}
