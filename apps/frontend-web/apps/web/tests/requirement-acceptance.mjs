// Real isolated API acceptance. The transport fault occurs after a real commit.
// Created projects remain available for manual acceptance; credentials are never logged.
import assert from "node:assert/strict"
import { chromium } from "playwright"

const baseURL = process.env.IAM_CONSOLE_URL ?? "http://127.0.0.1:4317"
assert(["localhost", "127.0.0.1"].includes(new URL(baseURL).hostname))
const browser = await chromium.launch({ channel: "chrome", headless: true })
const errors = []
async function login(accountID = "tenant-admin") {
  const context = await browser.newContext({
    baseURL,
    viewport: { width: 1366, height: 768 },
  })
  await context.addCookies([
    { name: "NEXT_LOCALE", value: "en-US", url: baseURL },
  ])
  const page = await context.newPage()
  page.on("pageerror", (error) => errors.push(error.message))
  await page.goto("/admin/login")
  const account = await page.evaluate(async (id) => {
    const accounts = (
      await import("/src/lib/dev-login-accounts.ts")
    ).devAccountsForEntrance("tenant")
    const found = accounts.find((account) => account.id === id)
    return found ? { role: found.role, tenant: found.tenantID } : null
  }, accountID)
  assert(account?.tenant, "existing development persona is required")
  if (accountID !== "tenant-admin") {
    await page.getByRole("combobox").click()
    await page.getByRole("option").filter({ hasText: account.role }).click()
  }
  await page.getByRole("button", { name: "Sign in", exact: true }).click()
  await page.waitForURL((url) => url.pathname !== "/admin/login")
  // A single workspace redirects automatically; multiple workspaces render links.
  const tenantSwitch = page.getByRole("button", {
    name: "Switch tenant",
    exact: true,
  })
  const tenantLink = page.locator(`a[href="/admin/${account.tenant}"]`)
  await tenantSwitch.or(tenantLink).first().waitFor()
  if (await tenantLink.isVisible()) await tenantLink.click()
  await page
    .getByRole("button", { name: "Switch tenant", exact: true })
    .waitFor()
  return { context, page, tenant: account.tenant }
}
async function checkFit(page) {
  assert(
    await page.evaluate(
      () => document.documentElement.scrollWidth <= innerWidth
    ),
    "page overflow"
  )
  const box = await page.getByRole("dialog").boundingBox()
  const size = page.viewportSize()
  assert(
    box &&
      box.x >= 0 &&
      box.y >= 0 &&
      box.x + box.width <= size.width + 1 &&
      box.y + box.height <= size.height + 1,
    "creation dialog outside viewport"
  )
}
try {
  const { context, page, tenant } = await login()
  const projects = await page.evaluate(async (tenant) => {
    const { iamApi } = await import("/src/lib/iam-api.ts")
    return (await iamApi.projects(tenant, { offset: 0, limit: 100 })).items
  }, tenant)
  assert(projects.length >= 2)
  const entity = projects[0].project_id
  const other = projects[1].project_id
  const path = `/admin/${tenant}/entities/${entity}/requirements`
  const endpoint = `/api/entities/${entity}/requirements`
  await page.goto(path)
  await page
    .getByRole("heading", { name: "Requirements", exact: true })
    .waitFor()
  await page
    .getByRole("button", { name: "New requirement", exact: true })
    .click()
  const dialog = page.getByRole("dialog")
  await checkFit(page)
  const title = `Requirement acceptance ${Date.now().toString(36)}`
  await dialog.getByRole("textbox", { name: "Title", exact: true }).fill(title)
  await dialog
    .getByRole("textbox", { name: "Description", exact: true })
    .fill("Freeze real business requirements before work begins")
  await dialog
    .getByRole("textbox", { name: "Acceptance clause", exact: true })
    .fill("x".repeat(2001))
  await dialog
    .getByRole("button", { name: "New requirement", exact: true })
    .click()
  await dialog.locator("#criteria-custom-error[role=alert]").waitFor()
  const unicode = "😀".repeat(1500)
  await dialog
    .getByRole("textbox", { name: "Acceptance clause", exact: true })
    .fill(unicode)
  const posts = []
  let loseCreate = true
  await page.route(new RegExp(`${endpoint}$`), async (route) => {
    if (route.request().method() !== "POST") return route.continue()
    const response = await route.fetch()
    posts.push({
      key: route.request().headers()["idempotency-key"],
      status: response.status(),
      body: await response.json(),
      replay: response.headers()["idempotent-replayed"],
    })
    if (loseCreate) {
      loseCreate = false
      return route.abort("failed")
    }
    await route.fulfill({ response })
  })
  await dialog
    .getByRole("button", { name: "New requirement", exact: true })
    .click()
  await dialog.getByRole("alert").first().waitFor()
  assert.equal(
    await dialog
      .getByRole("textbox", { name: "Title", exact: true })
      .inputValue(),
    title
  )
  await dialog
    .getByRole("button", { name: "New requirement", exact: true })
    .click()
  await dialog.waitFor({ state: "hidden" })
  assert.equal(posts.length, 2)
  assert.equal(posts[0].status, 201)
  assert.equal(posts[1].replay, "true")
  assert.equal(posts[0].key, posts[1].key)
  assert.equal(posts[0].body.data.id, posts[1].body.data.id)
  const id = posts[0].body.data.id
  await page.unroute(new RegExp(`${endpoint}$`))
  await page.getByRole("searchbox", { name: "Title", exact: true }).fill(title)
  await page.getByRole("button", { name: "Search", exact: true }).click()
  const row = page.getByRole("row").filter({ hasText: title })
  await row
    .getByRole("button", { name: "Work item details", exact: true })
    .click()
  await dialog
    .getByRole("button", { name: "Confirm baseline", exact: true })
    .waitFor()
  assert.equal(
    await dialog
      .getByRole("textbox", { name: "Acceptance clause", exact: true })
      .inputValue(),
    unicode
  )
  async function saveDraft() {
    const read = page.waitForResponse(
      (response) =>
        response.url().endsWith(`${endpoint}/${id}`) &&
        response.request().method() === "GET"
    )
    await page
      .getByRole("dialog")
      .last()
      .getByRole("button", { name: "Save", exact: true })
      .click()
    assert.equal((await read).status(), 200)
    await page.waitForFunction(
      () => document.querySelectorAll('[role="dialog"]').length === 1
    )
  }
  await dialog
    .getByRole("button", { name: "Edit draft", exact: true })
    .last()
    .click()
  await page
    .getByRole("dialog")
    .last()
    .getByRole("textbox", { name: "Description", exact: true })
    .fill("Revised business objective")
  await saveDraft()
  await dialog.getByRole("button", { name: "Add clause", exact: true }).click()
  await page
    .getByRole("dialog")
    .last()
    .getByRole("textbox", { name: "Acceptance clause", exact: true })
    .fill("Disposable criterion")
  await saveDraft()
  await page.waitForFunction(() =>
    [...document.querySelectorAll("textarea")].some(
      (input) => input.value === "Disposable criterion"
    )
  )
  await dialog
    .getByRole("button", { name: "Edit draft", exact: true })
    .nth(1)
    .click()
  await page
    .getByRole("dialog")
    .last()
    .getByRole("checkbox", {
      name: "Remove this criterion from the draft",
      exact: true,
    })
    .check()
  await saveDraft()
  await page.waitForFunction(
    () =>
      ![...document.querySelectorAll("textarea")].some(
        (input) => input.value === "Disposable criterion"
      )
  )
  await dialog.getByRole("button", { name: "Add clause", exact: true }).click()
  await page
    .getByRole("dialog")
    .last()
    .getByRole("textbox", { name: "Acceptance clause", exact: true })
    .fill("Replacement criterion")
  await saveDraft()
  await page.waitForFunction(() =>
    [...document.querySelectorAll("textarea")].some(
      (input) => input.value === "Replacement criterion"
    )
  )
  const baselines = []
  let loseBaseline = true
  await page.route(new RegExp(`${endpoint}/${id}/versions$`), async (route) => {
    const response = await route.fetch()
    baselines.push({
      key: route.request().headers()["idempotency-key"],
      match: route.request().headers()["if-match"],
      status: response.status(),
      replay: response.headers()["idempotent-replayed"],
    })
    if (loseBaseline) {
      loseBaseline = false
      return route.abort("failed")
    }
    await route.fulfill({ response })
  })
  await dialog
    .getByRole("button", { name: "Confirm baseline", exact: true })
    .click()
  await dialog.getByRole("alert").first().waitFor()
  await dialog
    .getByRole("button", { name: "Confirm baseline", exact: true })
    .click()
  await dialog
    .getByRole("heading", { name: "Baseline v1", exact: true })
    .waitFor()
  assert.equal(baselines.length, 2)
  assert.equal(baselines[0].key, baselines[1].key)
  assert.equal(baselines[0].match, '"5"')
  assert.equal(baselines[1].replay, "true")
  const data = await page.evaluate(
    async ({ entity, id, other }) => {
      const { iamApi } = await import("/src/lib/iam-api.ts")
      const detail = await iamApi.requirement(entity, id)
      let crossStatus
      try {
        await iamApi.requirement(other, id)
      } catch (error) {
        crossStatus = error.status
      }
      const encoded = new TextEncoder().encode(
        JSON.stringify(detail.versions[0].snapshot)
      )
      const hash = [
        ...new Uint8Array(await crypto.subtle.digest("SHA-256", encoded)),
      ]
        .map((byte) => byte.toString(16).padStart(2, "0"))
        .join("")
      return { detail, hash, crossStatus }
    },
    { entity, id, other }
  )
  assert.equal(data.crossStatus, 404)
  assert.equal(data.detail.versions.length, 1)
  assert.equal(data.hash, data.detail.versions[0].content_hash)
  assert.equal(
    data.detail.criteria[0].id,
    data.detail.versions[0].snapshot.criteria[0].id
  )
  assert.deepEqual(
    data.detail.criteria.map((criterion) => criterion.code),
    ["C1", "C3"]
  )
  assert.equal(data.detail.description, "Revised business objective")
  assert.equal(
    await dialog
      .getByRole("button", { name: "Edit draft", exact: true })
      .count(),
    0
  )
  await page.setViewportSize({ width: 390, height: 844 })
  await checkFit(page)
  await page.setViewportSize({ width: 1366, height: 768 })
  await page.goto(`/admin/${tenant}/entities/${entity}/work-items`)
  await page.getByRole("button", { name: "New work item", exact: true }).click()
  await dialog
    .getByRole("combobox", { name: "Requirement (optional)", exact: true })
    .selectOption(id)
  await dialog
    .getByRole("textbox", { name: "Title", exact: true })
    .fill(`Work linked to ${title}`)
  await dialog
    .getByRole("textbox", { name: "Objective", exact: true })
    .fill("Deliver against the confirmed requirement")
  await dialog
    .getByRole("textbox", { name: "Acceptance clause", exact: true })
    .fill("Demonstrate the agreed result")
  await dialog
    .getByRole("textbox", { name: "How to verify", exact: true })
    .fill("Exercise the business acceptance path")
  const workResponse = page.waitForResponse(
    (r) =>
      r.url().endsWith(`/api/entities/${entity}/work-items`) &&
      r.request().method() === "POST"
  )
  await dialog
    .getByRole("button", { name: "Confirm and create", exact: true })
    .click()
  const work = await workResponse
  assert.equal(work.status(), 201)
  const workID = (await work.json()).data.id
  await dialog.waitFor({ state: "hidden" })
  const linked = await page.evaluate(
    async ({ entity, workID }) => {
      const { iamApi } = await import("/src/lib/iam-api.ts")
      return await iamApi.workItem(entity, workID)
    },
    { entity, workID }
  )
  assert.equal(linked.requirement_id, id)
  assert.equal(linked.baseline_version_no, 1)
  assert.equal(linked.contract.snapshot.requirement_id, id)
  assert.equal(linked.state, "DRAFT")
  assert.deepEqual(errors, [])
  console.log(
    `PASS real Requirement ${id}: Unicode validation, creation replay, baseline replay/If-Match/hash, immutable clause IDs, cross-instance 404, responsive detail, real work association`
  )
  await context.close()
} finally {
  await browser.close()
}
