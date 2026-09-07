import assert from "node:assert/strict"
import { mkdirSync } from "node:fs"
import { chromium } from "playwright"
import { auditPassword } from "./audit-password.mjs"

const tenant = process.env.IAM_BROWSER_TENANT
if (!tenant)
  throw new Error(
    "IAM_BROWSER_TENANT is required; use a dedicated acceptance tenant"
  )
const password = auditPassword()
const login = process.env.IAM_BROWSER_LOGIN ?? "admin@example.test"
const baseURL = (
  process.env.IAM_CONSOLE_URL ?? "http://localhost:8091"
).replace(/\/$/, "")
const screenshots = new URL(
  "../../../../../.local/screenshots/field-policies/",
  import.meta.url
)
mkdirSync(screenshots, { recursive: true })
const browser = await chromium.launch({
  channel: process.env.IAM_BROWSER_CHANNEL ?? "chrome",
  headless: true,
})
const context = await browser.newContext({
  viewport: { width: 1440, height: 1000 },
})
let page = await context.newPage()
const exceptions = []
page.on("pageerror", (error) => exceptions.push(error.message))
async function api(path, method = "GET", body, headers = {}) {
  return page.evaluate(
    async ({ path, method, body, headers, tenant }) => {
      const response = await fetch("/api" + path, {
        method,
        credentials: "include",
        headers: {
          "Content-Type": "application/json",
          "X-Tenant-Id": tenant,
          ...headers,
        },
        body: body === undefined ? undefined : JSON.stringify(body),
      })
      const data = await response.json().catch(() => null)
      return {
        status: response.status,
        data: data?.data,
        error: data?.detail ?? data?.message,
      }
    },
    { path, method, body, headers, tenant }
  )
}
async function ok(path, method, body, headers) {
  const response = await api(path, method, body, headers)
  assert(
    response.status >= 200 && response.status < 300,
    `${method ?? "GET"} ${path}: ${response.status} ${response.error}`
  )
  return response.data
}
async function select(id, label) {
  await page.locator(id).click()
  await page.getByRole("option", { name: label, exact: true }).click()
}
async function editor() {
  await page
    .getByRole("button", { name: "编辑 description", exact: true })
    .click()
  await page.getByRole("dialog").waitFor()
}
async function save() {
  await page.getByRole("button", { name: "保存", exact: true }).click()
  await page.getByRole("dialog").waitFor({ state: "hidden" })
  await page
    .getByRole("button", { name: "编辑 description", exact: true })
    .waitFor()
}
async function fits() {
  assert(
    await page.evaluate(
      () => document.documentElement.scrollWidth <= innerWidth
    ),
    "page overflows horizontally"
  )
  const dialog = page.getByRole("dialog")
  if (await dialog.count()) {
    const box = await dialog.boundingBox()
    const view = page.viewportSize()
    assert(
      box.x >= 0 && box.x + box.width <= view.width + 1,
      "dialog overflows viewport"
    )
    assert(
      box.y >= 0 && box.y + box.height <= view.height + 1,
      "dialog overflows vertically"
    )
    for (const checkbox of await dialog.getByRole("checkbox").all()) {
      const bounds = await checkbox.boundingBox()
      assert(
        bounds.width >= 16 && bounds.height >= 16,
        "checkbox must retain usable dimensions"
      )
    }
  }
}
try {
  await page.goto(baseURL + "/login")
  await page.evaluate(
    ({ tenant }) => localStorage.setItem("iam.tenant", tenant),
    { tenant }
  )
  await ok("/authn/sessions", "POST", {
    login_name: login,
    password,
    return_url: baseURL + "/",
  })
  const me = await ok("/authn/sessions/current")
  const existing = (await ok("/iam/field-policies")).find(
    (p) => p.resource === "todo" && p.field === "description"
  )
  assert(
    !existing?.version,
    "Use a dedicated acceptance tenant without a custom todo.description policy; existing policies are not removed by setup"
  )
  const roles = await ok("/iam/roles")
  const adminRole = roles.find((role) => role.name === "Administrator")
  assert(adminRole)
  const entity = await ok("/iam/entities", "POST", {
    name: `Field audit ${Date.now()}`,
    type: "store",
  })
  const task = await ok(
    "/todos",
    "POST",
    {
      title: "Field policy browser audit",
      description: "private browser fixture",
    },
    { "X-Entity-Id": entity.id }
  )
  await page.goto(baseURL + "/iam/field-policies")
  await page
    .getByRole("button", { name: "编辑 description", exact: true })
    .waitFor()
  assert.equal(
    await page.locator("tr[data-field]").count(),
    (await ok("/iam/field-catalog")).length
  )
  assert.equal(
    await page.getByRole("link", { name: "字段权限", exact: true }).count(),
    1
  )
  assert.equal(
    await page.getByRole("button", { name: "编辑 id", exact: true }).count(),
    0
  )
  await fits()
  await page.screenshot({
    path: new URL("desktop-list.png", screenshots).pathname.replace(
      /^\/(?=[A-Za-z]:)/,
      ""
    ),
    fullPage: true,
  })
  await editor()
  assert.equal(
    await page.getByRole("button", { name: "恢复默认", exact: true }).count(),
    0,
    "version-zero defaults cannot be deleted"
  )
  await select("#field-read", "默认拒绝")
  await select("#field-write", "默认拒绝")
  await select("#field-mask", "固定脱敏")
  await page
    .getByRole("checkbox", { name: "Administrator 读取允许", exact: true })
    .check()
  await page
    .getByRole("checkbox", { name: "Administrator 读取拒绝", exact: true })
    .check()
  await page
    .getByRole("checkbox", { name: "Administrator 写入允许", exact: true })
    .check()
  await page
    .getByRole("checkbox", { name: "Administrator 写入拒绝", exact: true })
    .check()
  await page.screenshot({
    path: new URL("desktop-editor.png", screenshots).pathname.replace(
      /^\/(?=[A-Za-z]:)/,
      ""
    ),
    fullPage: true,
  })
  await save()
  let stored = (await ok("/iam/field-policies")).find(
    (p) => p.field === "description"
  )
  assert.equal(stored.policy.mask, "redact")
  assert.deepEqual(stored.policy.read.allow_roles, [adminRole.id])
  assert.deepEqual(stored.policy.read.deny_roles, [adminRole.id])
  const effective = await ok(
    `/iam/fields/effective?resource=todo&entity_id=${entity.id}`
  )
  assert.equal(effective.fields.description.read, "redact")
  assert.equal(effective.fields.description.update, false)
  const masked = await ok("/todos/" + task.id, "GET", undefined, {
    "X-Entity-Id": entity.id,
  })
  assert.notEqual(masked.description, "private browser fixture")
  assert.equal(
    (
      await api(
        "/todos",
        "POST",
        { title: "Rejected", description: "blocked" },
        { "X-Entity-Id": entity.id }
      )
    ).status,
    403
  )
  console.log(
    "PASS UI save, role allow/deny persistence, no administrator bypass, actual todo masking/write rejection"
  )

  await editor()
  const concurrent = await ok("/iam/field-policies/todo/description", "PUT", {
    version: stored.version,
    policy: { ...stored.policy, mask: "omit" },
  })
  await page.getByRole("button", { name: "保存", exact: true }).click()
  await page
    .getByText("策略版本冲突，当前修改未保存。", { exact: true })
    .waitFor()
  assert.equal(
    (await ok("/iam/field-policies")).find((p) => p.field === "description")
      .version,
    concurrent.version
  )
  await page.getByRole("button", { name: "重新加载", exact: true }).click()
  await page.getByRole("dialog").waitFor({ state: "hidden" })
  await editor()
  assert.equal(await page.locator("#field-mask").innerText(), "隐藏")
  await page.getByRole("button", { name: "恢复默认", exact: true }).click()
  await page.getByRole("button", { name: "确认恢复", exact: true }).click()
  await page.getByRole("dialog").waitFor({ state: "hidden" })
  assert.equal(
    (await ok("/iam/field-policies")).find((p) => p.field === "description")
      .version,
    0
  )
  assert.equal(
    (
      await ok("/todos/" + task.id, "GET", undefined, {
        "X-Entity-Id": entity.id,
      })
    ).description,
    "private browser fixture"
  )
  console.log(
    "PASS real competing write produces 409; explicit reload; reset restores actual data access"
  )

  await page
    .getByRole("button", { name: "编辑 created_at", exact: true })
    .click()
  assert(await page.locator("#field-write").isDisabled())
  await page.getByRole("button", { name: "取消", exact: true }).click()
  await page.setViewportSize({ width: 390, height: 844 })
  await fits()
  await page.screenshot({
    path: new URL("mobile-list.png", screenshots).pathname.replace(
      /^\/(?=[A-Za-z]:)/,
      ""
    ),
    fullPage: true,
  })
  await editor()
  await fits()
  await page.screenshot({
    path: new URL("mobile-editor.png", screenshots).pathname.replace(
      /^\/(?=[A-Za-z]:)/,
      ""
    ),
    fullPage: true,
  })
  await page.getByRole("button", { name: "取消", exact: true }).click()
  console.log(
    "PASS fixed/read-only fields, 1440 desktop and 390 mobile framing"
  )

  const tenantB = await ok("/iam/tenants", "POST", {
    slug: `field-switch-${Date.now()}`,
    name: "Field switch tenant",
    owner_principal_id: me.subject,
  })
  await editor()
  await page.evaluate(
    ({ tenantB }) => {
      localStorage.setItem("iam.tenant", tenantB)
      window.dispatchEvent(new Event("tenant-change"))
    },
    { tenantB: tenantB.id }
  )
  await page.getByRole("dialog").waitFor({ state: "hidden" })
  await page
    .getByRole("button", { name: "编辑 description", exact: true })
    .waitFor()
  assert(
    (
      await api("/iam/field-policies", "GET", undefined, {
        "X-Tenant-Id": tenantB.id,
      })
    ).data.every((p) => p.version === 0)
  )
  console.log("PASS tenant switch discards the old tenant editor")

  const viewerLogin = `field-viewer-${Date.now()}@example.test`
  const viewer = await ok("/iam/principals", "POST", {
    login_name: viewerLogin,
    password,
    display_name: "Field policy viewer",
    email: viewerLogin,
  })
  const viewerRole = await ok("/iam/roles", "POST", {
    name: `Field viewer ${Date.now()}`,
  })
  await ok(
    `/iam/roles/${viewerRole.id}/permissions/field_policy_view`,
    "PUT",
    {}
  )
  await ok(`/iam/roles/${viewerRole.id}/members/${viewer.id}`, "PUT", {})
  const adminPage = page
  const viewerContext = await browser.newContext({
    viewport: { width: 1280, height: 900 },
  })
  page = await viewerContext.newPage()
  page.on("pageerror", (error) => exceptions.push(error.message))
  await page.goto(baseURL + "/login")
  await page.evaluate(
    (tenant) => localStorage.setItem("iam.tenant", tenant),
    tenant
  )
  await ok("/authn/sessions", "POST", {
    login_name: viewerLogin,
    password,
    return_url: baseURL + "/",
  })
  await page.goto(baseURL + "/iam/field-policies")
  await editor()
  const forbiddenWrite = page.waitForResponse(
    (response) =>
      response.url().endsWith("/iam/field-policies/todo/description") &&
      response.request().method() === "PUT"
  )
  await page.getByRole("button", { name: "保存", exact: true }).click()
  assert.equal((await forbiddenWrite).status(), 403)
  assert(await page.getByRole("dialog").isVisible())
  assert.equal(
    (await ok("/iam/field-policies")).find((p) => p.field === "description")
      .version,
    0
  )
  await page.getByRole("button", { name: "取消", exact: true }).click()
  assert.equal(
    (
      await api("/iam/field-policies", "GET", undefined, {
        "X-Tenant-Id": tenantB.id,
      })
    ).status,
    403
  )
  console.log(
    "PASS non-platform read-only viewer cannot save or read another tenant's policies"
  )

  const viewerPage = page
  page = adminPage
  await ok(
    `/iam/roles/${viewerRole.id}/permissions/field_policy_update`,
    "PUT",
    {}
  )
  page = viewerPage
  await editor()
  await page.locator("#field-role-id").fill(viewerRole.id)
  await page.getByRole("button", { name: "添加角色 ID", exact: true }).click()
  await page
    .getByRole("checkbox", { name: `${viewerRole.id} 读取拒绝`, exact: true })
    .check()
  await save()
  assert.deepEqual(
    (await ok("/iam/field-policies")).find((p) => p.field === "description")
      .policy.read.deny_roles,
    [viewerRole.id]
  )
  await editor()
  await page.getByRole("button", { name: "恢复默认", exact: true }).click()
  await page.getByRole("button", { name: "确认恢复", exact: true }).click()
  await page.getByRole("dialog").waitFor({ state: "hidden" })
  console.log(
    "PASS non-platform field manager can edit by role ID without role-catalog permission"
  )

  page = adminPage
  await ok(
    `/iam/roles/${viewerRole.id}/permissions/field_policy_view`,
    "DELETE"
  )
  page = viewerPage
  await page.reload()
  await page.getByText("字段策略不可用", { exact: true }).waitFor()
  assert.equal(
    await page.getByRole("link", { name: "字段权限", exact: true }).count(),
    0
  )
  assert.equal(await page.locator("tr[data-field]").count(), 0)
  console.log(
    "PASS revoked catalog access clears navigation and does not render stale policies"
  )
  assert.deepEqual(exceptions, [])
  console.log("PASS no browser runtime exceptions")
} finally {
  await browser.close()
}
