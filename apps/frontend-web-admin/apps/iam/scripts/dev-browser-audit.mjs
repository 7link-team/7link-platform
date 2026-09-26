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
      assert.equal(await page.locator('a[href*="/positions"]').count(), 0)
      console.log(
        `PASS ${account.role}: selector, login, session and identity links`
      )
      await context.close()
    }
  }
  const response = await fetch(runtime.apiURL + "/openapi.json")
  assert.equal(response.status, 200)
  const spec = await response.json()
  assert(
    !Object.keys(spec.paths).some((path) =>
      /\/(job-positions|positions)(\/|$)/.test(path)
    )
  )
  assert(
    !Object.keys(spec.components.schemas).some((name) => /position/i.test(name))
  )
  const published = JSON.parse(
    readFileSync(
      new URL("../../../../backend-api/docs/openapi.json", import.meta.url),
      "utf8"
    )
  )
  const liveOperations = new Map(
    Object.entries(spec.paths).flatMap(([path, item]) =>
      Object.entries(item)
        .filter(([, operation]) => operation.operationId)
        .map(([method, operation]) => [operation.operationId, { method, path }])
    )
  )
  for (const [path, item] of Object.entries(published.paths)) {
    for (const [method, operation] of Object.entries(item)) {
      if (!operation.operationId) continue
      assert.deepEqual(
        liveOperations.get(operation.operationId),
        { method, path },
        `running API must match the published contract: ${operation.operationId}`
      )
    }
  }
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
  const docsPage = await browser.newPage()
  await docsPage.goto(runtime.apiURL + "/docs/swagger/")
  await docsPage.locator(".opblock-tag").first().waitFor()
  const renderedGroups = await docsPage
    .locator(".opblock-tag-section")
    .evaluateAll((sections) =>
      sections.map((section) => ({
        tag: section.querySelector(".opblock-tag")?.getAttribute("data-tag"),
        paths: Array.from(
          section.querySelectorAll(
            ".opblock-summary-path, .opblock-summary-path__deprecated"
          ),
          (node) => node.textContent.trim()
        ),
      }))
    )
  const expectedGroups = [
    ...new Set(Object.keys(spec.paths).map((path) => path.split("/")[1])),
  ].sort()
  assert.deepEqual(
    renderedGroups.map((group) => group.tag).sort(),
    expectedGroups
  )
  let renderedCount = 0
  for (const group of renderedGroups) {
    for (const path of group.paths) {
      assert.equal(
        path.split("/")[1],
        group.tag,
        `Swagger UI ${group.tag}: ${path}`
      )
      renderedCount++
    }
  }
  assert.equal(renderedCount, count)
  await docsPage.close()
  console.log(
    `PASS Swagger schema and rendered UI: ${count} operations grouped by path prefix; demo selector ${runtime.demoAccounts ? "enabled" : "disabled"}`
  )
} finally {
  await browser.close()
}
