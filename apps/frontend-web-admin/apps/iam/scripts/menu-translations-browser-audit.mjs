import assert from "node:assert/strict"
import { readFileSync } from "node:fs"
import { chromium } from "playwright"
const directory = new URL(
  "../../../../backend-api/.temp/iam-uat/",
  import.meta.url
)
const { webURL, apiURL } = JSON.parse(
  readFileSync(new URL("state.json", directory), "utf8")
)
assert.equal(new URL(webURL).hostname, "127.0.0.1")
const spec = await (await fetch(`${apiURL}/openapi.json`)).json()
for (const name of ["CreateMenuInputBody", "UpdateMenuInputBody", "APIMenu"])
  assert(
    spec.components.schemas[name].properties.translations,
    `${name} must accept translations`
  )
const browser = await chromium.launch({ channel: "chrome", headless: true })
try {
  const context = await browser.newContext({ baseURL: webURL })
  await context.addCookies([
    { name: "NEXT_LOCALE", value: "zh-CN", url: webURL },
  ])
  const page = await context.newPage()
  await page.goto("/iam/menus")
  await page.locator('input[name="login_name"]').fill("admin@example.test")
  await page
    .locator('input[name="password"]')
    .fill(readFileSync(new URL("admin-password", directory), "utf8").trim())
  await page.locator('button[type="submit"]').click()
  await page
    .getByRole("button", { name: "切换工作空间", exact: true })
    .waitFor()
  await page.evaluate(
    async (tenantID) => {
      const { setTenant } = await import("/src/lib/iam-api.ts")
      setTenant(tenantID)
    },
    readFileSync(new URL("tenant-id", directory), "utf8").trim()
  )
  await page.goto("/iam/menus")
  const languageNames = ["English", "简体中文", "Bahasa Melayu"]
  await page.getByLabel("菜单预览语言", { exact: true }).waitFor()
  assert.deepEqual(
    await page
      .getByLabel("菜单预览语言", { exact: true })
      .locator("option")
      .allTextContents(),
    languageNames
  )
  await page.getByRole("button", { name: "创建菜单", exact: true }).click()
  await page.getByLabel("语言标签 1", { exact: true }).waitFor()
  assert.deepEqual(
    await page
      .getByLabel("语言标签 1", { exact: true })
      .locator("option")
      .allTextContents(),
    languageNames
  )
  const label = `translation-audit-${Date.now()}`
  const values = {
    "en-US": "Translation audit",
    "zh-CN": "翻译验收",
    "ms-MY": "Pengesahan terjemahan",
  }
  await page.locator('input[name="label"]').fill(label)
  for (let i = 1; i <= 3; i++) {
    const locale = await page
      .getByLabel(`语言标签 ${i}`, { exact: true })
      .inputValue()
    await page.getByLabel(`菜单译文 ${i}`, { exact: true }).fill(values[locale])
  }
  const responsePromise = page.waitForResponse(
    (r) => r.url().endsWith("/iam/menus") && r.request().method() === "POST"
  )
  await page.getByRole("button", { name: "创建", exact: true }).click()
  const response = await responsePromise
  assert(response.ok(), await response.text())
  const created = (await response.json()).data
  assert.deepEqual(created.translations, values)
  await page.getByRole("dialog").waitFor({ state: "hidden" })
  await page.reload()
  const row = page.getByRole("row").filter({ hasText: "翻译验收" })
  await row.waitFor()
  await row.getByRole("button", { name: `编辑${label}`, exact: true }).click()
  assert.equal(await page.locator('input[name="label"]').inputValue(), label)
  for (let i = 1; i <= 3; i++) {
    const locale = await page
      .getByLabel(`语言标签 ${i}`, { exact: true })
      .inputValue()
    assert.equal(
      await page.getByLabel(`菜单译文 ${i}`, { exact: true }).inputValue(),
      values[locale]
    )
  }
  const updatePromise = page.waitForResponse(
    (r) =>
      r.url().endsWith(`/iam/menus/${created.id}`) &&
      r.request().method() === "PATCH"
  )
  await page.getByRole("button", { name: "保存", exact: true }).click()
  const updated = await updatePromise
  assert(updated.ok(), await updated.text())
  assert.deepEqual((await updated.json()).data.translations, values)
  await page.getByRole("dialog").waitFor({ state: "hidden" })
  await row.getByRole("button", { name: `删除${label}`, exact: true }).click()
  await row.waitFor({ state: "hidden" })
  console.log(
    "PASS: live schema, three-language create, reload, edit, update, audit fixture cleanup"
  )
} finally {
  await browser.close()
}
