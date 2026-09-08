import assert from "node:assert/strict"
import { mkdirSync, readFileSync } from "node:fs"
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
const load = (locale) =>
  JSON.parse(
    readFileSync(
      new URL(`../src/messages/${locale}.json`, import.meta.url),
      "utf8"
    )
  )
const label = (locale) =>
  JSON.parse(
    readFileSync(
      new URL(
        `../../../packages/ui/src/i18n/messages/${locale}.json`,
        import.meta.url
      ),
      "utf8"
    )
  ).localeSwitcher.label
const nativeNames = {
  "en-US": "English",
  "ms-MY": "Bahasa Melayu",
  "zh-CN": "简体中文",
}
const screenshots = new URL("browser/account-language/", directory)
mkdirSync(screenshots, { recursive: true })
const browser = await chromium.launch({ channel: "chrome", headless: true })
try {
  for (const width of [1440, 390]) {
    const context = await browser.newContext({
      baseURL: webURL,
      viewport: { width, height: 900 },
    })
    const page = await context.newPage()
    const errors = []
    page.on("pageerror", (e) => errors.push(e.message))
    page.on("console", (m) => {
      if (/MISSING_MESSAGE|FORMATTING_ERROR|INVALID_MESSAGE/.test(m.text()))
        errors.push(m.text())
    })
    await page.goto("/account/profile")
    await page.locator('input[name="login_name"]').fill("member@example.test")
    await page
      .locator('input[name="password"]')
      .fill(readFileSync(new URL("member-password", directory), "utf8").trim())
    await page.locator('button[type="submit"]').click()
    await page
      .getByRole("heading", { name: load("en-US").profile, exact: true })
      .waitFor()
    let current = "en-US"
    for (const locale of ["en-US", "ms-MY", "zh-CN"]) {
      if (locale !== current) {
        await page
          .getByRole("button", { name: label(current), exact: true })
          .click()
        await page
          .getByRole("group", { name: label(current), exact: true })
          .getByRole("button")
          .filter({ hasText: nativeNames[locale] })
          .click()
        await page.waitForFunction(
          (l) => document.documentElement.lang === l,
          locale
        )
      }
      current = locale
      const messages = load(locale)
      for (const [route, key] of [
        ["profile", "profile"],
        ["security", "security"],
        ["mfa", "twoFactor"],
      ]) {
        await page.goto(`/account/${route}`)
        await page
          .getByRole("heading", { name: messages[key], exact: true })
          .waitFor()
        if (locale !== "zh-CN")
          assert.doesNotMatch(
            await page.locator("body").innerText(),
            /\p{Script=Han}/u
          )
        if (route === "security") {
          await page
            .getByText(messages.currentDevice, { exact: true })
            .waitFor()
          await page
            .getByRole("button", { name: messages.changePassword, exact: true })
            .click()
          await page.getByRole("dialog").waitFor()
          if (locale !== "zh-CN")
            assert.doesNotMatch(
              await page.getByRole("dialog").innerText(),
              /\p{Script=Han}/u
            )
          await page
            .getByRole("button", { name: messages.cancel, exact: true })
            .click()
          await page.getByRole("dialog").waitFor({ state: "hidden" })
        }
        assert(
          await page.evaluate(
            () => document.documentElement.scrollWidth <= innerWidth
          )
        )
        await page.screenshot({
          path: fileURLToPath(
            new URL(`${width}-${locale}-${route}.png`, screenshots)
          ),
          fullPage: true,
        })
      }
      assert.equal(await page.locator("html").getAttribute("lang"), locale)
      console.log(
        `PASS ${width} ${locale}: switched locale persists, three account pages, dialog, real session, no mixed Chinese`
      )
    }
    await page
      .getByRole("button", { name: load(current).accountMenu, exact: true })
      .click()
    await page
      .getByRole("menuitem", { name: load(current).logout, exact: true })
      .click()
    await page.locator('input[name="login_name"]').waitFor()
    assert.deepEqual(errors, [])
    await context.close()
  }
} finally {
  await browser.close()
}
