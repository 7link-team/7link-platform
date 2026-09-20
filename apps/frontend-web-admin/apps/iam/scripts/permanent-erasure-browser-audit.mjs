import assert from "node:assert/strict"
import { randomBytes } from "node:crypto"
import { mkdirSync, readFileSync, writeFileSync } from "node:fs"
import { join } from "node:path"
import { chromium } from "playwright"

if (process.env.IAM_ERASURE_ACCEPT_DISPOSABLE_DATABASE !== "true") {
  throw new Error(
    "Set IAM_ERASURE_ACCEPT_DISPOSABLE_DATABASE=true only for an isolated disposable database"
  )
}

const baseURL = (process.env.IAM_CONSOLE_URL ?? "").replace(/\/$/, "")
const tenant = process.env.IAM_BROWSER_TENANT ?? ""
const login = process.env.IAM_BROWSER_LOGIN ?? "admin@example.test"
const passwordFile = process.env.IAM_BROWSER_PASSWORD_FILE ?? ""
const reportDirectory = process.env.IAM_ERASURE_REPORT_DIR ?? ""
if (!baseURL || !tenant || !passwordFile || !reportDirectory) {
  throw new Error(
    "IAM_CONSOLE_URL, IAM_BROWSER_TENANT, IAM_BROWSER_PASSWORD_FILE, and IAM_ERASURE_REPORT_DIR are required"
  )
}
const parsedBase = new URL(baseURL)
assert(
  ["127.0.0.1", "localhost"].includes(parsedBase.hostname),
  "Permanent-erasure browser acceptance is restricted to a loopback deployment"
)

mkdirSync(reportDirectory, { recursive: true, mode: 0o700 })
const stamp = `${Date.now()}-${randomBytes(4).toString("hex")}`
const targetLogin = `erasure-browser-${stamp}`
const targetDisplay = `Erasure Browser ${stamp}`
const targetEmail = `${targetLogin}@example.test`
const targetPassword = `${randomBytes(24).toString("base64url")}aA1!`
const reportPath = join(reportDirectory, "report.json")
const beforePath = join(reportDirectory, "before-erasure.png")
const afterPath = join(reportDirectory, "after-erasure.png")
let browser
let targetID = ""

const writeReport = (value) =>
  writeFileSync(reportPath, JSON.stringify(value, null, 2) + "\n", {
    mode: 0o600,
  })

writeReport({ status: "RUNNING", at: new Date().toISOString() })

try {
  browser = await chromium.launch({ channel: "chrome", headless: true })
  const context = await browser.newContext({
    baseURL,
    viewport: { width: 1440, height: 1000 },
    locale: "zh-CN",
  })
  await context.addCookies([
    { name: "NEXT_LOCALE", value: "zh-CN", url: baseURL },
  ])
  await context.addInitScript((tenantID) => {
    localStorage.setItem("iam.tenant", tenantID)
  }, tenant)
  const page = await context.newPage()
  const browserErrors = []
  page.on("pageerror", (error) => browserErrors.push(error.message))
  page.on("console", (message) => {
    if (
      message.type() === "error" &&
      message.text().includes("Maximum update depth")
    ) {
      browserErrors.push(message.text())
    }
  })

  await page.goto("/login")
  await page.locator('input[name="login_name"]').fill(login)
  await page
    .locator('input[name="password"]')
    .fill(readFileSync(passwordFile, "utf8").trim())
  await page.locator('button[type="submit"]').click()
  await page.getByRole("button", { name: "切换工作空间" }).waitFor()

  const createResponse = await context.request.post(
    baseURL + `/api/tenants/${tenant}/principals`,
    {
      headers: { Origin: baseURL },
      data: {
        login_name: targetLogin,
        password: targetPassword,
        display_name: targetDisplay,
        email: targetEmail,
      },
    }
  )
  assert.equal(createResponse.status(), 201, await createResponse.text())
  const created = (await createResponse.json()).data
  targetID = created.id
  assert(targetID, "The synthetic principal must have an id")

  await page.goto("/users")
  await page.getByLabel("搜索账号").fill(targetLogin)
  const row = page.getByRole("row").filter({ hasText: targetLogin })
  await row.waitFor()

  await Promise.all([
    page.waitForResponse(
      (response) =>
        response.url().endsWith(`/api/principals/${targetID}/suspension`) &&
        response.request().method() === "PUT" &&
        response.status() === 200
    ),
    row.getByRole("button", { name: "停用", exact: true }).click(),
  ])
  await row.getByText("停用", { exact: true }).first().waitFor()

  await row
    .getByRole("button", {
      name: `${targetDisplay}的数据保留与擦除`,
      exact: true,
    })
    .click()
  const dialog = page.getByRole("dialog")
  await dialog.waitFor()
  await page.waitForLoadState("networkidle")
  await dialog.locator("#retain-until").fill("2000-01-01")
  await dialog.locator("#retention-reference").fill(`uat-${stamp}`)
  await Promise.all([
    page.waitForResponse(
      (response) =>
        response
          .url()
          .endsWith(
            `/api/platform/principals/${targetID}/lifecycle/retention`
          ) &&
        response.request().method() === "PUT" &&
        response.status() === 200
    ),
    dialog.getByRole("button", { name: "保存保留期" }).click(),
  ])
  await dialog.getByRole("status").getByText("保留期已更新").waitFor()
  await dialog.locator("#anonymize-confirmation").fill(targetLogin)
  const erase = dialog.getByRole("button", {
    name: "永久擦除",
    exact: true,
  })
  assert.equal(await erase.isDisabled(), false)
  await page.screenshot({
    path: beforePath,
    fullPage: true,
    animations: "disabled",
  })

  await Promise.all([
    page.waitForResponse(
      (response) =>
        response
          .url()
          .endsWith(
            `/api/platform/principals/${targetID}/lifecycle/anonymization`
          ) &&
        response.request().method() === "POST" &&
        response.status() === 200
    ),
    erase.click(),
  ])
  await dialog
    .getByRole("status")
    .getByText("该账号的个人数据已被永久擦除")
    .waitFor()
  await dialog.getByText("已擦除（墓碑）", { exact: true }).waitFor()
  assert.equal(
    await dialog.getByRole("button", { name: "永久擦除" }).count(),
    0
  )
  await page.screenshot({
    path: afterPath,
    fullPage: true,
    animations: "disabled",
  })

  const lifecycleResponse = await context.request.get(
    baseURL + `/api/platform/principals/${targetID}/lifecycle`,
    { headers: { Origin: baseURL } }
  )
  assert.equal(lifecycleResponse.status(), 200, await lifecycleResponse.text())
  const lifecycle = (await lifecycleResponse.json()).data
  assert.equal(lifecycle.anonymized, true)
  assert(lifecycle.lifecycle.anonymized_at)
  assert.deepEqual(browserErrors, [])

  writeReport({
    status: "PASS",
    at: new Date().toISOString(),
    target_id: targetID,
    lifecycle_version: lifecycle.lifecycle.version,
    checks: [
      "synthetic-principal-created",
      "principal-disabled-in-ui",
      "expired-retention-recorded-in-ui",
      "confirmation-gated-erasure-in-ui",
      "tombstone-rendered-in-ui",
      "tombstone-read-back-from-api",
    ],
  })
  console.log(JSON.stringify({ status: "PASS", targetID }, null, 2))
} catch (error) {
  writeReport({
    status: "FAIL",
    at: new Date().toISOString(),
    target_id: targetID || undefined,
    error: error.message,
  })
  throw error
} finally {
  await browser?.close()
}
