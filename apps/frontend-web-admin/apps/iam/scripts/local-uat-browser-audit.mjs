import assert from "node:assert/strict"
import { mkdirSync, readFileSync, writeFileSync } from "node:fs"
import { fileURLToPath } from "node:url"
import { chromium } from "playwright"

const directory = new URL(
  "../../../../backend-api/.temp/iam-uat/",
  import.meta.url
)
const settings = JSON.parse(
  readFileSync(new URL("state.json", directory), "utf8")
)
const tenant = readFileSync(new URL("tenant-id", directory), "utf8").trim()
const baseURL = settings.webURL
assert.equal(
  new URL(baseURL).hostname,
  "127.0.0.1",
  "Only the isolated local UAT is in scope"
)
const screenshots = new URL("browser/", directory)
mkdirSync(screenshots, { recursive: true, mode: 0o700 })
let browser
const results = []
const reportPath = new URL("report.json", screenshots)
writeFileSync(
  reportPath,
  JSON.stringify({ status: "RUNNING", at: new Date().toISOString() }) + "\n",
  { mode: 0o600 }
)

try {
  browser = await chromium.launch({ channel: "chrome", headless: true })
  for (const persona of ["admin", "member"]) {
    for (const [device, viewport] of [
      ["desktop", { width: 1440, height: 1000 }],
      ["mobile", { width: 390, height: 844 }],
    ]) {
      const context = await browser.newContext({ baseURL, viewport })
      await context.addCookies([
        { name: "NEXT_LOCALE", value: "zh-CN", url: baseURL },
      ])
      const page = await context.newPage()
      const errors = []
      page.on("pageerror", (error) => errors.push(error.message))
      page.on("console", (message) => {
        if (
          message.type() === "error" &&
          message.text().includes("Maximum update depth")
        )
          errors.push(message.text())
      })
      const capture = async (name) => {
        assert(
          await page.evaluate(
            () => document.documentElement.scrollWidth <= innerWidth
          ),
          "Page must not overflow horizontally"
        )
        await page.screenshot({
          path: fileURLToPath(
            new URL(`${persona}-${device}-${name}.png`, screenshots)
          ),
          fullPage: true,
          animations: "disabled",
        })
      }
      try {
        await page.goto("/login")
        await page.locator('input[name="login_name"]').waitFor()
        await capture("login")
        await page
          .locator('input[name="login_name"]')
          .fill(`${persona}@example.test`)
        await page
          .locator('input[name="password"]')
          .fill(
            readFileSync(
              new URL(`${persona}-password`, directory),
              "utf8"
            ).trim()
          )
        await page.locator('button[type="submit"]').click()
        await page
          .getByRole("button", { name: "切换工作空间", exact: true })
          .waitFor()
        await page
          .locator('[aria-label="当前工作区"]')
          .getByText(tenant, { exact: true })
          .waitFor()
        await page.locator("main article").first().waitFor()
        await capture("dashboard")
        if (device === "mobile") {
          await page.getByRole("button", { name: "打开主导航" }).click()
          await page.getByRole("dialog").waitFor()
        }
        const navigation = page
          .getByRole("navigation", { name: "主导航" })
          .filter({ visible: true })
        await navigation.waitFor()
        if (persona === "admin") {
          await navigation
            .getByRole("link", { name: "账号与成员", exact: true })
            .waitFor()
          await capture("navigation")
          await navigation
            .getByRole("link", { name: "账号与成员", exact: true })
            .click()
          if (device === "mobile") await page.keyboard.press("Escape")
          await page
            .getByRole("button", {
              name: "UAT Member的数据保留与擦除",
              exact: true,
            })
            .waitFor()
          await capture("users")
          await page
            .getByRole("button", {
              name: "UAT Member的数据保留与擦除",
              exact: true,
            })
            .click()
          await page.getByRole("dialog").waitFor()
          const erase = page.getByRole("button", {
            name: "永久擦除",
            exact: true,
          })
          await erase.waitFor()
          await page
            .getByRole("dialog")
            .getByText("启用中", { exact: true })
            .waitFor()
          await page
            .getByRole("dialog")
            .getByText(/账号仍可登录/)
            .waitFor()
          assert(
            await erase.isDisabled(),
            "An active unconfirmed fixture must not be erasable"
          )
          await capture("lifecycle")
          await page.keyboard.press("Escape")
        } else {
          assert.equal(
            await navigation
              .getByRole("link", { name: "账号与成员", exact: true })
              .count(),
            0
          )
          assert.equal(
            await navigation
              .getByRole("link", { name: "租户管理", exact: true })
              .count(),
            0
          )
          await capture("navigation")
          if (device === "mobile") await page.keyboard.press("Escape")
          const denied = page.waitForResponse(
            (response) =>
              response.url().includes("/api/iam/principals?") &&
              response.status() === 403
          )
          await page.goto("/iam/users")
          await denied
          await page.getByRole("alert").filter({ hasText: "禁止" }).waitFor()
          assert.equal(await page.locator("tbody tr").count(), 0)
          assert.equal(
            await page
              .getByRole("button", { name: /的数据保留与擦除$/ })
              .count(),
            0
          )
          await capture("denied")
        }
        await page.locator('header button[aria-haspopup="menu"]').last().click()
        await page.getByRole("menuitem", { name: "退出登录" }).click()
        await page.locator('input[name="login_name"]').waitFor()
        assert.equal(
          (await context.request.get("/api/authn/sessions/current")).status(),
          401
        )
        await page.goto("/iam/users")
        await page.locator('input[name="login_name"]').waitFor()
        assert.equal(new URL(page.url()).pathname, "/login")
        assert.deepEqual(errors, [])
        results.push({ persona, device, status: "PASS" })
      } catch (error) {
        await page
          .screenshot({
            path: fileURLToPath(
              new URL(`${persona}-${device}-failure.png`, screenshots)
            ),
            fullPage: true,
          })
          .catch(() => {})
        throw error
      } finally {
        await context.request
          .delete("/api/authn/sessions/current", {
            headers: { Origin: baseURL },
          })
          .catch(() => {})
        await context.close()
      }
    }
  }
  const report = {
    status: "PASS",
    at: new Date().toISOString(),
    browser: browser.version(),
    binarySHA256: settings.binarySHA256,
    results,
    scope:
      "UI login, tenant choice, navigation, read-only lifecycle guard, member denial, logout and protected-route redirect",
    destructiveLifecycle: "NOT_RUN",
    productionAcceptance: "NOT_READY",
  }
  writeFileSync(
    new URL("report.json", screenshots),
    JSON.stringify(report, null, 2) + "\n",
    { mode: 0o600 }
  )
  console.log(JSON.stringify(report, null, 2))
} catch (error) {
  writeFileSync(
    reportPath,
    JSON.stringify(
      { status: "FAIL", at: new Date().toISOString(), results },
      null,
      2
    ) + "\n",
    { mode: 0o600 }
  )
  throw error
} finally {
  await browser?.close()
}
