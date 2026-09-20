import assert from "node:assert/strict"
import { mkdirSync } from "node:fs"
import { fileURLToPath } from "node:url"
import { chromium } from "playwright"
import { auditPassword } from "./audit-password.mjs"

const tenant = process.env.IAM_BROWSER_TENANT
if (!tenant)
  throw new Error("IAM_BROWSER_TENANT must be a dedicated acceptance tenant")
const password = auditPassword()
const login = process.env.IAM_BROWSER_LOGIN ?? "admin@example.test"
const baseURL = (
  process.env.IAM_CONSOLE_URL ?? "http://localhost:8091"
).replace(/\/$/, "")
const screenshots = new URL(
  "../../../../../.local/screenshots/scim-actors/",
  import.meta.url
)
mkdirSync(screenshots, { recursive: true })
const browser = await chromium.launch({
  channel: process.env.IAM_BROWSER_CHANNEL ?? "chrome",
  headless: true,
})
const context = await browser.newContext({
  viewport: { width: 1440, height: 1000 },
  reducedMotion: "reduce",
})
await context.addCookies([
  { name: "NEXT_LOCALE", value: "zh-CN", url: baseURL },
])
const page = await context.newPage()
const exceptions = []
page.on("pageerror", (error) => exceptions.push(error.message))
async function api(path, method = "GET", body) {
  return page.evaluate(
    async ({ path, method, body, tenant }) => {
      const response = await fetch("/api" + path, {
        method,
        credentials: "include",
        headers: { "Content-Type": "application/json" },
        body: body === undefined ? undefined : JSON.stringify(body),
      })
      const result = await response.json()
      if (!response.ok)
        throw new Error(
          `${method} ${path}: ${response.status} ${result.message}`
        )
      return result.data
    },
    { path, method, body, tenant }
  )
}
async function save() {
  await page
    .getByRole("dialog")
    .getByRole("button", { name: "保存", exact: true })
    .click()
  await page.getByRole("dialog").waitFor({ state: "hidden" })
}
async function edit(name) {
  await page
    .getByRole("button", { name: `编辑 ${name}`, exact: true })
    .filter({ visible: true })
    .click()
  await page.getByRole("dialog").waitFor()
}
async function capture(name) {
  await page.evaluate(async () => {
    await Promise.all(
      document
        .getAnimations()
        .map((animation) => animation.finished.catch(() => {}))
    )
  })
  const dialog = page.getByRole("dialog")
  const box = await dialog.boundingBox()
  const viewport = page.viewportSize()
  assert(box.x >= 0 && box.x + box.width <= viewport.width + 1)
  assert(box.y >= 0 && box.y + box.height <= viewport.height + 1)
  assert(
    await page.evaluate(
      () => document.documentElement.scrollWidth <= innerWidth
    )
  )
  await page.screenshot({
    path: fileURLToPath(new URL(name + ".png", screenshots)),
    fullPage: true,
    animations: "disabled",
  })
}
try {
  await page.goto(baseURL + "/login")
  await api("/sessions", "POST", {
    login_name: login,
    password,
    return_url: baseURL + "/",
  })
  const session = await api("/sessions/current")
  await page.evaluate(
    ({ tenant, subject }) => {
      localStorage.setItem("iam.tenant", tenant)
      localStorage.setItem("iam.tenant:subject", subject)
    },
    { tenant, subject: session.subject }
  )
  const suffix = Date.now()
  const first = await api("/service-accounts", "POST", {
    login_name: `scim-ui-first-${suffix}`,
    display_name: "SCIM UI First",
  })
  const second = await api("/service-accounts", "POST", {
    login_name: `scim-ui-second-${suffix}`,
    display_name: "SCIM UI Second",
  })
  const name = `SCIM UI ${suffix}`
  await page.goto(baseURL + "/scim-directories")
  await page.getByRole("button", { name: "创建目录", exact: true }).click()
  await page.locator("#scim-directory-name").fill(name)
  await page.locator("#scim-directory-service-account").fill(first.id)
  await capture("desktop-editor")
  await save()
  let directory = (await api("/scim/directories")).find(
    (item) => item.name === name
  )
  assert.equal(directory.service_account_id, first.id)
  await edit(name)
  assert.equal(
    await page.locator("#scim-directory-service-account").inputValue(),
    first.id
  )
  await page.locator("#scim-directory-name").fill(name + " renamed")
  await save()
  directory = (await api("/scim/directories")).find(
    (item) => item.id === directory.id
  )
  assert.equal(
    directory.service_account_id,
    first.id,
    "ordinary edits preserve the actor"
  )
  await edit(directory.name)
  await page.locator("#scim-directory-service-account").fill(second.id)
  await save()
  directory = (await api("/scim/directories")).find(
    (item) => item.id === directory.id
  )
  assert.equal(directory.service_account_id, second.id)

  await edit(directory.name)
  directory = await api("/scim/directories/" + directory.id, "PUT", {
    name: directory.name,
    status: "disabled",
    version: directory.version,
    service_account_id: first.id,
  })
  await page.locator("#scim-directory-name").fill(name + " stale")
  await page
    .getByRole("dialog")
    .getByRole("button", { name: "保存", exact: true })
    .click()
  await page
    .getByRole("dialog")
    .getByRole("button", { name: "重新加载", exact: true })
    .click()
  await page.waitForFunction(
    (id) =>
      document.querySelector("#scim-directory-service-account")?.value === id,
    first.id
  )
  await page.setViewportSize({ width: 390, height: 844 })
  await capture("mobile-editor")
  await page.locator("#scim-directory-service-account").fill("")
  await save()
  directory = (await api("/scim/directories")).find(
    (item) => item.id === directory.id
  )
  assert.equal(
    directory.service_account_id,
    undefined,
    "explicit clearing persists"
  )

  await edit(directory.name)
  const me = await api("/sessions/current")
  const otherTenant = await api("/tenants", "POST", {
    slug: `scim-ui-other-${suffix}`,
    name: "SCIM UI Other",
    owner_principal_id: me.subject,
  })
  await page.evaluate((id) => {
    localStorage.setItem("iam.tenant", id)
    window.dispatchEvent(new Event("tenant-change"))
  }, otherTenant.id)
  await page.getByRole("dialog").waitFor({ state: "hidden" })
  assert.equal(
    await page
      .getByRole("button", { name: `编辑 ${directory.name}`, exact: true })
      .count(),
    0
  )
  assert.deepEqual(exceptions, [])
  console.log(
    "PASS: SCIM actor create, preserve, rebind, conflict reload, clear, tenant switch, desktop/mobile"
  )
} finally {
  await browser.close()
}
