import assert from "node:assert/strict"
import { readFileSync } from "node:fs"
import { randomBytes } from "node:crypto"
import { chromium } from "playwright"

const directory = new URL("../../../../backend-api/.temp/dev/", import.meta.url)
const runtime = JSON.parse(
  readFileSync(new URL("runtime.json", directory), "utf8")
)
const accounts = JSON.parse(
  readFileSync(new URL("demo-accounts.json", directory), "utf8")
).accounts
const browser = await chromium.launch({ channel: "chrome", headless: true })
const errors = []
const contexts = []
async function session(account, width = 1440, prefix = "user") {
  const context = await browser.newContext({
    baseURL: runtime.webURL,
    viewport: { width, height: 900 },
  })
  contexts.push(context)
  await context.addCookies([
    { name: "NEXT_LOCALE", value: "en-US", url: runtime.webURL },
  ])
  const page = await context.newPage()
  page.on("pageerror", (error) => errors.push(error.message))
  await page.goto(`/${prefix}/login`)
  await page.locator("#login-name").fill(account.loginName)
  await page.locator("#password").fill(account.password)
  await page.locator('form button[type="submit"]').click()
  await page.waitForURL(
    (url) =>
      !url.pathname.endsWith("/login") && !url.pathname.endsWith("/workspaces")
  )
  return { context, page }
}
async function call(context, path, method = "GET", data) {
  const response = await context.request.fetch(`/api${path}`, {
    method,
    data,
    headers: { Origin: runtime.webURL },
  })
  assert(response.ok(), `${method} ${path}: ${response.status()}`)
  return (await response.json()).data
}
try {
  const admin = await session(
    accounts.find((account) => account.loginName === "admin@example.test"),
    1440,
    "system"
  )
  for (const [width, action] of [
    [1440, "folder"],
    [390, "upload"],
    [1440, "retry"],
    [1440, "concurrent"],
  ]) {
    if (process.argv.includes("--system-only")) break
    const loginName = `auto-root-${randomBytes(6).toString("hex")}@example.test`
    const password = randomBytes(24).toString("base64url") + "aA1!"
    const principal = await call(
      admin.context,
      `/tenants/${runtime.tenantID}/principals`,
      "POST",
      { login_name: loginName, password, display_name: "Auto root acceptance" }
    )
    const { context, page } = await session({ loginName, password }, width)
    const roots = () => call(context, `/principals/${principal.id}/files`)
    await page.goto("/user/files")
    await page
      .getByRole("button", { name: "New folder", exact: true })
      .waitFor()
    await page.waitForLoadState("networkidle")
    assert.equal(
      (await roots()).items.length,
      0,
      "viewing files must not create a root"
    )
    assert.equal(
      await page
        .getByRole("button", { name: "Create personal files", exact: true })
        .count(),
      0
    )
    assert.equal(
      await page
        .getByRole("link", { name: "Back to workspace", exact: true })
        .count(),
      1,
      "existing navigation stays intact"
    )
    if (action === "concurrent") {
      const responses = await Promise.all(
        Array.from({ length: 8 }, () =>
          context.request.post(`/api/principals/${principal.id}/files`, {
            headers: { Origin: runtime.webURL },
            multipart: { kind: "directory", scope: "account" },
          })
        )
      )
      const ids = []
      for (const response of responses) {
        assert(response.ok(), `concurrent initialization: ${response.status()}`)
        ids.push((await response.json()).data.id)
      }
      assert.equal(new Set(ids).size, 1)
    }
    if (action === "upload") {
      await page
        .locator('input[type="file"][aria-label="Upload file"]')
        .setInputFiles({
          name: "first.txt",
          mimeType: "text/plain",
          buffer: Buffer.from("first upload"),
        })
      await page
        .locator("[data-file-id]")
        .filter({ hasText: "first.txt" })
        .waitFor()
    } else {
      await page
        .getByRole("button", { name: "New folder", exact: true })
        .click()
      const dialog = page.getByRole("dialog")
      await dialog
        .getByRole("textbox", { name: "Name", exact: true })
        .fill("First folder")
      if (action === "retry") {
        await page.route(`**/api/principals/${principal.id}/files`, (route) =>
          route.request().method() === "POST"
            ? route.fulfill({
                status: 503,
                contentType: "application/json",
                body: JSON.stringify({
                  code: 503,
                  message: "Temporary storage failure",
                }),
              })
            : route.continue()
        )
        await dialog.getByRole("button", { name: "Save", exact: true }).click()
        await page
          .getByText("Temporary storage failure", { exact: true })
          .waitFor()
        assert.equal((await roots()).items.length, 0)
        await page.unroute(`**/api/principals/${principal.id}/files`)
      }
      await dialog.getByRole("button", { name: "Save", exact: true }).click()
      await page
        .locator("[data-file-id]")
        .filter({ hasText: "First folder" })
        .waitFor()
    }
    assert.equal((await roots()).items.length, 1)
    await page.reload()
    await page
      .locator("[data-file-id]")
      .filter({ hasText: action === "upload" ? "first.txt" : "First folder" })
      .waitFor()
    assert.equal((await roots()).items.length, 1)
    console.log(
      `PASS ${width} ${action}: automatic first-write root, reload, existing navigation`
    )
  }
  const sub = await session(
    accounts.find((account) => account.role === "系统子管理员"),
    1440,
    "system"
  )
  const permissions = await call(sub.context, "/system/permissions")
  assert(permissions.includes("tenant_view"))
  assert(permissions.includes("system_account_view"))
  assert(!permissions.includes("platform_administer"))
  assert(
    permissions.every((code) =>
      /_(view|list|traverse|read|download)$/.test(code)
    )
  )
  await sub.page.waitForURL("**/system/accounts/system")
  await sub.page.waitForLoadState("networkidle")
  assert.equal(
    await sub.page.getByRole("button", { name: /^Account view / }).count(),
    0,
    "account browsing must not expose impersonation without account_view_create"
  )
  const paths = await sub.page
    .locator('aside a[href^="/system/"]')
    .evaluateAll((links) => links.map((link) => link.getAttribute("href")))
  for (const path of [
    "/system/accounts/system",
    "/system/departments",
    "/system/roles",
    "/system/menus",
    "/system/operating-platforms",
    "/system/tenants",
    "/system/audit/events",
    "/system/files",
  ]) {
    assert(paths.includes(path), `missing delegated menu: ${path}`)
    const failed = []
    const responseListener = (response) => {
      if (response.url().includes("/api/") && response.status() >= 400)
        failed.push(`${response.status()} ${new URL(response.url()).pathname}`)
    }
    sub.page.on("response", responseListener)
    await sub.page.goto(path)
    await sub.page.waitForLoadState("networkidle")
    assert.equal(new URL(sub.page.url()).pathname, path)
    assert.deepEqual(failed, [], `management page failed: ${path}`)
    sub.page.off("response", responseListener)
  }
  for (const [path, method, data] of [
    [
      "/system/principals",
      "POST",
      {
        login_name: "denied-system-action@example.test",
        password: randomBytes(24).toString("base64url") + "aA1!",
      },
    ],
    ["/system/roles", "POST", { name: "Denied acceptance role" }],
  ]) {
    const response = await sub.context.request.fetch(`/api${path}`, {
      method,
      data,
      headers: { Origin: runtime.webURL },
    })
    assert.equal(response.status(), 403, `read persona must not write ${path}`)
  }
  console.log(
    "PASS delegated system account: system management menus with read permissions and no full administrator grant"
  )
  await admin.page.goto("/system/accounts/system")
  const administratorRow = admin.page.getByRole("row").filter({
    has: admin.page.getByText("admin@example.test", { exact: true }),
  })
  await administratorRow.getByRole("button", { name: /^Account view / }).click()
  const dialog = admin.page.getByRole("dialog")
  const operatorURL = admin.page.url()
  const popupPromise = admin.page.waitForEvent("popup")
  await dialog.getByRole("button", { name: "Open view", exact: true }).click()
  const popup = await popupPromise
  popup.on("pageerror", (error) => errors.push(error.message))
  await popup.getByRole("button", { name: "Exit view", exact: true }).waitFor()
  await popup.waitForURL("**/system/accounts/system")
  assert.equal(admin.page.url(), operatorURL)
  const closed = popup.waitForEvent("close")
  await popup.getByRole("button", { name: "Exit view", exact: true }).click()
  await closed
  assert.equal(admin.page.url(), operatorURL)
  console.log(
    "PASS authorized administrator can open admin system view in isolated tab and exit back to unchanged operator"
  )
  assert.deepEqual(errors, [])
} finally {
  for (const context of contexts) await context.close()
  await browser.close()
}
