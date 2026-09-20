import assert from "node:assert/strict"
import { readFileSync } from "node:fs"
import { join } from "node:path"
import { chromium } from "playwright"

const webURL = (process.env.IAM_WEB_URL ?? "http://127.0.0.1:18091").replace(
  /\/$/u,
  ""
)
const directory = process.env.IAM_UAT_DIRECTORY
if (!directory) throw new Error("IAM_UAT_DIRECTORY is required")

const fixture = JSON.parse(
  readFileSync(join(directory, "file-personas.json"), "utf8")
)
const accountPasswords = new Map([
  [
    "super-admin",
    readFileSync(join(directory, "admin-password"), "utf8").trim(),
  ],
  ...fixture.accounts
    .filter((account) => account.id !== "super-admin")
    .map((account) => [
      account.id,
      readFileSync(join(directory, `${account.id}-password`), "utf8").trim(),
    ]),
])
const expectedScopes = {
  "super-admin": ["tenant", "entity", "department", "department"],
  "system-admin": ["tenant", "entity", "department", "department"],
  "tenant-admin": ["tenant", "entity", "department", "department"],
  "entity-account": ["entity", "department", "department"],
  "department-account": ["department"],
}

const browser = await chromium.launch({ channel: "chrome", headless: true })
const results = []
try {
  for (const account of fixture.accounts) {
    process.stdout.write(`Auditing ${account.id}...\n`)
    const context = await browser.newContext({
      baseURL: webURL,
      viewport: { width: 1440, height: 960 },
    })
    const page = await context.newPage()
    const consoleErrors = []
    const failedResponses = []
    page.on("console", (message) => {
      if (message.type() === "error") consoleErrors.push(message.text())
    })
    page.on("pageerror", (error) => consoleErrors.push(error.message))
    page.on("response", (response) => {
      if (response.status() >= 400)
        failedResponses.push(`${response.status()} ${response.url()}`)
    })

    await page.goto("/login")
    await page.getByLabel("Development account").click()
    await page
      .getByRole("option", {
        name: `${account.role} · ${account.loginName}`,
        exact: true,
      })
      .click()
    assert.equal(
      await page.locator("#login-name").inputValue(),
      account.loginName
    )
    assert.equal(
      await page.locator("#password").inputValue(),
      accountPasswords.get(account.id)
    )
    await page.locator('button[type="submit"]').click()
    await page.waitForURL((url) => url.pathname !== "/login")
    const entityBase = `/tenants/${encodeURIComponent(fixture.tenantID)}/entities/${encodeURIComponent(fixture.entityID)}`
    const response = await context.request.get(`/api${entityBase}/files`, {
      headers: { Origin: webURL },
    })
    assert.equal(response.status(), 200, `${account.id} file roots request`)
    const roots = (await response.json()).data.items
    const scopes = roots.map((root) => root.scope).sort()
    assert.deepEqual(
      scopes,
      [...expectedScopes[account.id]].sort(),
      `${account.id} file root visibility`
    )
    if (account.id === "department-account")
      assert.equal(
        roots[0].department_id,
        fixture.departments["UAT Finance"].id
      )

    consoleErrors.length = 0
    failedResponses.length = 0
    await page.goto(`/tenants/${encodeURIComponent(fixture.tenantID)}`)
    const tenantFilesLink = page
      .getByRole("link", { name: "File management", exact: true })
      .first()
    const tenantWide = ["super-admin", "system-admin", "tenant-admin"].includes(
      account.id
    )
    if (tenantWide) {
      await tenantFilesLink.waitFor()
      assert.equal(
        await tenantFilesLink.getAttribute("href"),
        `/tenants/${fixture.tenantID}/files`
      )
      await page.goto(`/tenants/${fixture.tenantID}/files`)
      await page.getByRole("region", { name: "Files" }).waitFor()
      await page.getByText("Loading files…", { exact: true }).waitFor({
        state: "hidden",
      })
    } else assert.equal(await tenantFilesLink.count(), 0)

    await page.goto(`${entityBase}/files`)
    try {
      await page.getByRole("region", { name: "Files" }).waitFor()
    } catch (cause) {
      const pageText = (await page.locator("body").innerText())
        .replace(/\s+/gu, " ")
        .slice(0, 500)
      throw new Error(
        `${account.id} file page did not render at ${page.url()}: ${pageText}`,
        { cause }
      )
    }
    await page.getByText("Loading files…", { exact: true }).waitFor({
      state: "hidden",
    })
    const entityFilesLink = page
      .getByRole("link", { name: "File management", exact: true })
      .first()
    assert.equal(
      await entityFilesLink.getAttribute("href"),
      `${entityBase}/files`
    )
    for (const root of roots) {
      const label =
        root.scope === "tenant"
          ? "Tenant files"
          : root.scope === "entity"
            ? "Entity files"
            : root.scope === "department"
              ? "Department files"
              : "Personal files"
      await page
        .getByRole("button", { name: label, exact: true })
        .first()
        .waitFor()
    }
    const unexpectedResponses = failedResponses.filter((failure) => {
      if (!failure.startsWith("403 ")) return true
      const pathname = new URL(failure.slice(4)).pathname
      return (
        pathname !== `/api/tenants/${fixture.tenantID}/field-catalog` &&
        pathname !== "/api/tenants"
      )
    })
    const unexpectedConsoleErrors = consoleErrors.filter(
      (message) => !message.startsWith("Failed to load resource:")
    )
    assert.deepEqual(
      unexpectedResponses,
      [],
      `${account.id} failed browser responses`
    )
    assert.deepEqual(
      unexpectedConsoleErrors,
      [],
      `${account.id} browser console errors`
    )
    results.push({ account: account.id, scopes })
    await context.close()
  }
} finally {
  await browser.close()
}

process.stdout.write(
  JSON.stringify({ status: "PASS", results }, null, 2) + "\n"
)
