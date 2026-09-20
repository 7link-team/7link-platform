import assert from "node:assert/strict"
import { readFileSync } from "node:fs"
import { chromium } from "playwright"

const directory = new URL(
  "../../../../backend-api/.temp/iam-uat/",
  import.meta.url
)
const { webURL } = JSON.parse(
  readFileSync(new URL("state.json", directory), "utf8")
)
assert.equal(new URL(webURL).hostname, "127.0.0.1")
const bundles = Object.fromEntries(
  ["en-US", "zh-CN", "ms-MY"].map((locale) => [
    locale,
    JSON.parse(
      readFileSync(
        new URL(
          `../../../../backend-api/internal/core/i18n/locales/${locale}.json`,
          import.meta.url
        ),
        "utf8"
      )
    ),
  ])
)
const browser = await chromium.launch({ channel: "chrome", headless: true })
try {
  const page = await browser.newPage()
  await page.goto(`${webURL}/login`)
  await page.locator('input[name="login_name"]').fill("admin@example.test")
  await page
    .locator('input[name="password"]')
    .fill(readFileSync(new URL("admin-password", directory), "utf8").trim())
  await page.locator('button[type="submit"]').click()
  await page.waitForURL((url) => url.pathname !== "/login")
  const result = await page.evaluate(async (messages) => {
    const { iamApi, setTenant } = await import("/src/lib/iam-api.ts")
    let updated = 0,
      skipped = 0
    for (const tenant of await iamApi.tenants()) {
      setTenant(tenant.id)
      for (const menu of await iamApi.menus()) {
        const key =
          "menu_" + menu.route.replace(/^\/iam\//, "").replaceAll("-", "_")
        const legacyAccountMenu =
          key === "menu_users" &&
          menu.label === "Principals and members" &&
          Object.keys(menu.translations ?? {}).length === 3 &&
          menu.translations["en-US"] === "Principals and members" &&
          menu.translations["zh-CN"] === "登录主体与成员" &&
          menu.translations["ms-MY"] === "Prinsipal dan ahli"
        if (
          !legacyAccountMenu &&
          (messages["en-US"][key] !== menu.label ||
            Object.keys(menu.translations ?? {}).length !== 0)
        ) {
          skipped++
          continue
        }
        const translations = Object.fromEntries(
          Object.entries(messages).map(([locale, entries]) => [
            locale,
            entries[key],
          ])
        )
        if (Object.values(translations).some((value) => !value))
          throw new Error(`Missing menu translation ${key}`)
        const saved = await iamApi.updateMenu(menu.id, { translations })
        if (saved.label !== menu.label) throw new Error("Default label changed")
        for (const [locale, value] of Object.entries(translations))
          if (saved.translations?.[locale] !== value)
            throw new Error("Translation not persisted")
        updated++
      }
    }
    return { updated, skipped }
  }, bundles)
  console.log(JSON.stringify(result))
} finally {
  await browser.close()
}
