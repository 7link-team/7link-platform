import assert from "node:assert/strict"
import { readFileSync } from "node:fs"
import { resolve } from "node:path"
import { chromium } from "playwright"

const directory = resolve(
  process.env.IAM_DEV_DIRECTORY ?? "../../../backend-api/.temp/dev"
)
const runtime = JSON.parse(
  readFileSync(resolve(directory, "runtime.json"), "utf8")
)
const browser = await chromium.launch({ channel: "chrome", headless: true })
try {
  const context = await browser.newContext({ baseURL: runtime.webURL })
  const page = await context.newPage()
  await page.goto("/login")
  await page.locator("#login-name").waitFor()
  assert.equal(
    await page.getByRole("combobox").count(),
    runtime.demoAccounts ? 1 : 0
  )
  assert.equal(await page.locator("#login-name").inputValue(), "")
  assert.equal(await page.locator("#password").inputValue(), "")
  await context.close()
  if (runtime.demoAccounts) {
    const { accounts } = JSON.parse(
      readFileSync(resolve(directory, "demo-accounts.json"), "utf8")
    )
    assert.equal(accounts.length, runtime.accountCount)
    for (const account of accounts) {
      const context = await browser.newContext({ baseURL: runtime.webURL })
      const page = await context.newPage()
      await page.goto("/login")
      await page.getByRole("combobox").click()
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
        account.password
      )
      await page.locator('form button[type="submit"]').click()
      await page.waitForURL((url) => url.pathname !== "/login")
      const identity = await context.request.get("/api/me")
      assert.equal(identity.status(), 200)
      const { subject } = (await identity.json()).data
      for (const path of [
        "/me",
        "/sessions/current",
        `/principals/${subject}/identity-links`,
        `/principals/${subject}/identity-link-providers`,
        `/principals/${subject}/reauthentication/methods`,
      ])
        assert.equal(
          (await context.request.get("/api" + path)).status(),
          200,
          path
        )
      await page.goto("/me/identities")
      await page.waitForLoadState("networkidle")
      assert.equal(await page.getByText("HTTP 404", { exact: true }).count(), 0)
      console.log(
        `PASS ${account.role}: selector, login, session and identity links`
      )
      await context.close()
    }
  }
  const response = await fetch(runtime.apiURL + "/openapi.json")
  assert.equal(response.status, 200)
  const spec = await response.json()
  let count = 0
  for (const [path, item] of Object.entries(spec.paths)) {
    assert(!path.includes("/me/"), `retired implicit principal path: ${path}`)
    for (const [method, operation] of Object.entries(item)) {
      if (
        !["get", "post", "patch", "put", "delete", "head", "options"].includes(
          method
        )
      )
        continue
      assert.deepEqual(
        operation.tags,
        [path.split("/")[1]],
        `${method} ${path}`
      )
      count++
    }
  }
  assert.equal((await fetch(runtime.apiURL + "/docs")).status, 200)
  console.log(
    `PASS Swagger: ${count} operations grouped by path prefix; demo selector ${runtime.demoAccounts ? "enabled" : "disabled"}`
  )
} finally {
  await browser.close()
}
