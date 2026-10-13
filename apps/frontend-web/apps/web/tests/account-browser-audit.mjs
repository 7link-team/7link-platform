import assert from "node:assert/strict"
import { readFileSync, mkdirSync } from "node:fs"
import { fileURLToPath } from "node:url"
import { chromium } from "playwright"

const directory = new URL(
  "../../../../backend-api/.temp/iam-uat/",
  import.meta.url
)
const { webURL } = JSON.parse(
  readFileSync(new URL("state.json", directory), "utf8")
)
assert.equal(new URL(webURL).hostname, "127.0.0.1")
const screenshots = new URL("browser/account/", directory)
mkdirSync(screenshots, { recursive: true })
const browser = await chromium.launch({ channel: "chrome", headless: true })
try {
  for (const persona of ["admin", "member"]) {
    for (const width of [1440, 390]) {
      const context = await browser.newContext({
        baseURL: webURL,
        viewport: { width, height: 900 },
      })
      const page = await context.newPage()
      await context.addCookies([
        { name: "NEXT_LOCALE", value: "zh-CN", url: webURL },
      ])
      const errors = []
      page.on("pageerror", (error) => errors.push(error.message))
      await page.goto("/user/profile")
      await page.locator('input[name="login_name"]').waitFor()
      assert.equal(new URL(page.url()).pathname, "/login")
      await page
        .locator('input[name="login_name"]')
        .fill(`${persona}@example.test`)
      await page
        .locator('input[name="password"]')
        .fill(
          readFileSync(new URL(`${persona}-password`, directory), "utf8").trim()
        )
      await page.locator('button[type="submit"]').click()
      await page
        .getByRole("heading", { name: "基本信息", exact: true })
        .waitFor()
      assert.match(await page.locator("main dd").last().innerText(), /^\d+$/)
      assert.equal(
        await page.getByText("passkey_unavailable", { exact: true }).count(),
        0
      )
      assert.equal(await page.locator('[aria-label="当前工作区"]').count(), 0)
      assert.equal(
        await page
          .getByRole("button", { name: "切换工作空间", exact: true })
          .count(),
        0
      )
      assert.equal(
        await page
          .getByRole("navigation", { name: "主导航", exact: true })
          .count(),
        0
      )
      const navigate = async (label) => {
        if (width < 768)
          await page
            .getByRole("button", { name: "打开个人中心导航", exact: true })
            .click()
        await page
          .getByRole("navigation", { name: "个人中心导航", exact: true })
          .filter({ visible: true })
          .getByRole("link", { name: label, exact: true })
          .click()
      }
      await navigate("安全管理")
      await page
        .getByRole("heading", { name: "登录设备", exact: true })
        .waitFor()
      await page.getByText("当前设备", { exact: true }).waitFor()
      assert.equal(
        await page
          .getByRole("heading", { name: "主邮箱", exact: true })
          .count(),
        0
      )
      await page.getByRole("button", { name: "修改密码", exact: true }).click()
      await page.getByRole("dialog").waitFor()
      await page.getByRole("button", { name: "取消", exact: true }).click()
      await navigate("双重验证（2FA）")
      await page
        .getByRole("button", { name: "启用验证器", exact: true })
        .waitFor()
      await page
        .getByRole("button", { name: "启用验证器", exact: true })
        .click()
      await page.getByRole("dialog").waitFor()
      await page.keyboard.press("Escape")
      await page.getByRole("dialog").waitFor({ state: "hidden" })
      assert.equal(
        await page
          .getByRole("heading", { name: "登录设备", exact: true })
          .count(),
        0
      )
      assert(
        await page.evaluate(
          () => document.documentElement.scrollWidth <= innerWidth
        )
      )
      await page.screenshot({
        path: fileURLToPath(new URL(`${persona}-${width}.png`, screenshots)),
        fullPage: true,
      })
      await page.getByRole("link", { name: "返回工作区", exact: true }).click()
      await page
        .getByRole("button", { name: "切换工作空间", exact: true })
        .waitFor()
      assert.equal(
        await page
          .locator('aside a[href="/user/security"], aside a[href^="/user/"]')
          .count(),
        0
      )
      await page.getByRole("button", { name: "账户菜单", exact: true }).click()
      await page
        .getByRole("menuitem", { name: "个人中心", exact: true })
        .click()
      await page
        .getByRole("heading", { name: "基本信息", exact: true })
        .waitFor()
      await page.goto("/security")
      await page
        .getByRole("heading", { name: "安全管理", exact: true })
        .waitFor()
      assert.equal(new URL(page.url()).pathname, "/me/security")
      await page.getByRole("button", { name: "账户菜单", exact: true }).click()
      await page
        .getByRole("menuitem", { name: "退出登录", exact: true })
        .click()
      await page.locator('input[name="login_name"]').waitFor()
      assert.deepEqual(errors, [])
      console.log(
        `PASS ${persona} ${width}: protected routes, avatar, isolated navigation, real session, security dialogs, legacy redirect`
      )
      await context.close()
    }
  }
} finally {
  await browser.close()
}
