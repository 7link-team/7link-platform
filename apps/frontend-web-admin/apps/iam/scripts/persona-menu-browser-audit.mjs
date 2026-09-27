import assert from "node:assert/strict"
import { mkdirSync, readFileSync, writeFileSync } from "node:fs"
import { join } from "node:path"
import { chromium } from "playwright"

const webURL = (process.env.IAM_WEB_URL ?? "http://localhost:18091").replace(
  /\/$/u,
  ""
)
const directory =
  process.env.IAM_UAT_DIRECTORY ??
  join(import.meta.dirname, "../../../../backend-api/.temp/iam-uat")
const fixture = JSON.parse(
  readFileSync(join(directory, "file-personas.json"), "utf8")
)
const accountFilter = new Set(
  (process.env.IAM_PERSONAS ?? "").split(",").filter(Boolean)
)
const accounts = fixture.accounts.filter(
  (account) => accountFilter.size === 0 || accountFilter.has(account.id)
)
const outputDirectory = join(directory, "browser", "persona-menus")
mkdirSync(outputDirectory, { recursive: true, mode: 0o700 })

const passwords = new Map(
  accounts.map((account) => [
    account.id,
    readFileSync(
      join(
        directory,
        account.id === "super-admin"
          ? "admin-password"
          : `${account.id}-password`
      ),
      "utf8"
    ).trim(),
  ])
)
const tenantBase = `/tenants/${encodeURIComponent(fixture.tenantID)}`
const entityBase = `${tenantBase}/entities/${encodeURIComponent(fixture.entityID)}`
const report = { at: new Date().toISOString(), status: "RUNNING", accounts: [] }
const saveReport = () =>
  writeFileSync(
    join(outputDirectory, "report.json"),
    JSON.stringify(report, null, 2) + "\n",
    { mode: 0o600 }
  )

const browser = await chromium.launch({ channel: "chrome", headless: true })
try {
  for (const account of accounts) {
    process.stdout.write(`Auditing ${account.id} menus...\n`)
    const result = { account: account.id, pages: [], failures: [] }
    report.accounts.push(result)
    const context = await browser.newContext({
      baseURL: webURL,
      viewport: { width: 1440, height: 960 },
    })
    await context.addInitScript(() => {
      if (!sessionStorage.getItem("iam.tenant"))
        sessionStorage.setItem("iam.tenant", "1")
      if (!localStorage.getItem("iam.locale"))
        localStorage.setItem("iam.locale", "en-US")
    })
    const page = await context.newPage()
    let responseFailures = []
    let browserErrors = []
    page.on("response", (response) => {
      const optionalMissingAvatar =
        response.status() === 404 &&
        /^\/api\/principals\/[^/]+\/avatar$/.test(
          new URL(response.url()).pathname
        )
      if (
        !optionalMissingAvatar &&
        (response.status() === 403 ||
          response.status() === 404 ||
          response.status() >= 500)
      )
        responseFailures.push(`${response.status()} ${response.url()}`)
    })
    page.on("pageerror", (error) => browserErrors.push(error.message))
    page.on("console", (message) => {
      if (
        message.type() === "error" &&
        !message.text().startsWith("Failed to load resource:")
      )
        browserErrors.push(message.text())
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
      await page.locator("#password").inputValue(),
      passwords.get(account.id)
    )
    await page.locator('button[type="submit"]').click()
    await page.waitForURL((url) => url.pathname !== "/login")
    assert.equal(
      await page.evaluate(() => sessionStorage.getItem("iam.tenant")),
      fixture.tenantID,
      `${account.id} must replace stale tenant state before navigation`
    )

    const queue = [tenantBase, `${entityBase}/files`]
    const visited = new Set()
    while (queue.length > 0) {
      const path = queue.shift()
      if (!path || visited.has(path)) continue
      visited.add(path)
      responseFailures = []
      browserErrors = []
      const navigation = await page.goto(path)
      await page
        .waitForLoadState("networkidle", { timeout: 5_000 })
        .catch(() => {})
      const body = (await page.locator("body").innerText()).replace(
        /\s+/gu,
        " "
      )
      const pageFailures = [
        ...(navigation && navigation.status() >= 400
          ? [`${navigation.status()} ${page.url()}`]
          : []),
        ...responseFailures.filter((failure) => !failure.startsWith("403 ")),
        ...browserErrors,
      ]
      if (
        /HTTP 404|文件加载失败|Failed to load files|Page not found|页面不存在/iu.test(
          body
        )
      )
        pageFailures.push(`visible error: ${body.slice(0, 500)}`)
      const alerts = await page.locator('[role="alert"]').allInnerTexts()
      if (alerts.some((text) => /^(Forbidden|禁止)$/iu.test(text.trim())))
        pageFailures.push(
          `visible authorization error: ${alerts.join(" | ")}`,
          ...responseFailures.filter((failure) => failure.startsWith("403 "))
        )
      if (
        path.includes("/files") &&
        !/^\/principals\/[^/]+\/files$/.test(path)
      ) {
        if (
          /Create personal files|创建个人文件区|Cipta fail peribadi/iu.test(
            body
          )
        )
          pageFailures.push(
            "organization file page exposes personal root creation"
          )
        if (
          /No file root has been created for this account|当前账号尚未创建文件根目录|Belum ada akar fail untuk akaun ini/iu.test(
            body
          )
        )
          pageFailures.push(
            "organization file page uses a personal empty state"
          )
      }
      const links = await page
        .locator("aside nav a[href]")
        .evaluateAll((nodes) => nodes.map((node) => node.href))
      for (const href of links) {
        const url = new URL(href)
        if (url.origin !== webURL) continue
        const candidate = `${url.pathname}${url.search}`
        if (!visited.has(candidate)) queue.push(candidate)
        if (
          candidate === `${entityBase}/roles` &&
          !visited.has(`${entityBase}/resources`)
        )
          queue.push(`${entityBase}/resources`)
      }
      result.pages.push({ path, links: links.length })
      if (pageFailures.length > 0) {
        result.failures.push({ path, errors: [...new Set(pageFailures)] })
        await page.screenshot({
          path: join(
            outputDirectory,
            `${account.id}-${result.failures.length}.png`
          ),
          fullPage: true,
        })
      }
    }
    await context.close()
    saveReport()
  }
} finally {
  await browser.close()
}

report.status = report.accounts.every(
  (account) => account.failures.length === 0
)
  ? "PASS"
  : "FAIL"
saveReport()
assert.equal(
  report.status,
  "PASS",
  `persona menu audit failed; see ${join(outputDirectory, "report.json")}`
)
process.stdout.write(JSON.stringify(report, null, 2) + "\n")
