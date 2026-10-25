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
  assert(
    projects.length >= 2,
    "isolated fixture needs two real project instances"
  )
  const entity = projects[0].project_id
  const other = projects[1].project_id
  const path = `/admin/${tenant}/entities/${entity}/work-items`
  const endpoint = `/api/entities/${entity}/work-items`
  const list = page.waitForResponse(
    (r) => r.url().includes(endpoint) && r.request().method() === "GET"
  )
  await page.goto(path)
  assert.equal((await list).status(), 200, "real work list must be authorized")
  await page.getByRole("button", { name: "New work item", exact: true }).click()
  const dialog = page.getByRole("dialog")
  await dialog
    .getByRole("button", { name: "Confirm and create", exact: true })
    .waitFor()
  await checkFit(page)
  const title = `Work acceptance ${Date.now().toString(36)}`
  await dialog.getByRole("textbox", { name: "Title", exact: true }).fill(title)
  await dialog
    .getByRole("textbox", { name: "Objective", exact: true })
    .fill("Verify independent work creation in the real instance")
  const unicodeClause = "😀".repeat(1500)
  const clauseInput = dialog.getByRole("textbox", {
    name: "Acceptance clause",
    exact: true,
  })
  await clauseInput.fill("x".repeat(2001))
  const validationPosts = []
  const countValidationPosts = (request) => {
    if (request.method() === "POST" && request.url().endsWith(endpoint))
      validationPosts.push(request)
  }
  page.on("request", countValidationPosts)
  await dialog
    .getByRole("textbox", { name: "How to verify", exact: true })
    .fill("Verify the completed work")
  await dialog
    .getByRole("button", { name: "Confirm and create", exact: true })
    .click()
  await dialog.locator("#criteria-custom-error[role=alert]").waitFor()
  assert.equal(validationPosts.length, 0, "invalid criteria must not submit")
  page.off("request", countValidationPosts)
  await clauseInput.fill("")
  await clauseInput.focus()
  await page.keyboard.insertText(unicodeClause)
  assert.equal(
    await clauseInput.inputValue(),
    unicodeClause,
    "criteria accept Unicode code points within the API limit"
  )
  await dialog
    .getByRole("textbox", { name: "How to verify", exact: true })
    .fill("Fetch detail and verify IDs, objective, responsible Human and hash")
  const posts = []
  let loseResponse = true
  await page.route(new RegExp(`${endpoint}$`), async (route) => {
    if (route.request().method() !== "POST") return route.continue()
    const response = await route.fetch()
    posts.push({
      key: route.request().headers()["idempotency-key"],
      status: response.status(),
      replay: response.headers()["idempotent-replayed"],
      body: await response.json(),
    })
    if (loseResponse) {
      loseResponse = false
      return route.abort("failed")
    }
    return route.fulfill({ response })
  })
  await page.setViewportSize({ width: 375, height: 768 })
  await checkFit(page)
  await dialog.locator("form").evaluate((form) => {
    form.requestSubmit()
    form.requestSubmit()
  })
  await dialog.getByRole("alert").first().waitFor()
  assert.equal(
    posts.length,
    1,
    await dialog
      .getByRole("alert")
      .allTextContents()
      .then((items) => items.join("; "))
  )
  assert.equal(posts[0].status, 201)
  assert.equal(
    await dialog
      .getByRole("textbox", { name: "Title", exact: true })
      .inputValue(),
    title
  )
  await dialog
    .getByRole("button", { name: "Confirm and create", exact: true })
    .click()
  await dialog.waitFor({ state: "hidden" })
  assert.equal(posts.length, 2)
  assert.equal(posts[1].key, posts[0].key)
  assert.equal(posts[1].replay, "true")
  assert.deepEqual(posts[0].body.data, posts[1].body.data)
  const created = posts[1].body.data
  assert.equal(created.state, "DRAFT")
  assert.equal(created.contract_version_no, 1)
  const checks = await page.evaluate(
    async ({ entity, other, id, title }) => {
      const { iamApi } = await import("/src/lib/iam-api.ts")
      const detail = await iamApi.workItem(entity, id)
      const matching = await iamApi.workItems(entity, {
        offset: 0,
        limit: 20,
        title: `like:${title}`,
      })
      let foreign = 0
      try {
        await iamApi.workItem(other, id)
      } catch (cause) {
        foreign = cause.status
      }
      const snapshot = JSON.stringify(detail.contract.snapshot)
      const bytes = await crypto.subtle.digest(
        "SHA-256",
        new TextEncoder().encode(snapshot)
      )
      const hash = Array.from(new Uint8Array(bytes), (b) =>
        b.toString(16).padStart(2, "0")
      ).join("")
      return { detail, total: matching.total, foreign, hash }
    },
    { entity, other, id: created.id, title }
  )
  assert.equal(checks.total, 1)
  assert.equal(checks.foreign, 404)
  assert.equal(checks.detail.requirement_id, null)
  assert.equal(checks.detail.baseline_version_no, 0)
  assert.equal(checks.detail.contract.content_hash, checks.hash)
  assert.equal(checks.detail.contract.content_hash, created.content_hash)
  assert(checks.detail.criteria[0].id)
  assert.equal(
    checks.detail.criteria[0].id,
    checks.detail.contract.snapshot.criteria[0].id
  )
  assert.equal(checks.detail.criteria[0].text, unicodeClause)
  await page.setViewportSize({ width: 1366, height: 768 })
  await page.getByRole("searchbox", { name: "Title", exact: true }).fill(title)
  const filtered = page.waitForResponse(
    (r) =>
      r.request().method() === "GET" &&
      r.url().includes(endpoint) &&
      new URL(r.url()).searchParams.get("title") === `like:${title}`
  )
  await page.getByRole("button", { name: "Search", exact: true }).click()
  await page.waitForURL((url) => url.searchParams.get("title") === title)
  assert.equal((await filtered).status(), 200)
  await page.getByRole("cell", { name: title, exact: true }).waitFor()
  await page
    .getByRole("row")
    .filter({ has: page.getByRole("cell", { name: title, exact: true }) })
    .getByRole("button", { name: "Work item details", exact: true })
    .click()
  await page
    .getByRole("dialog")
    .getByText("Confirmed contract · version 1", { exact: true })
    .waitFor()
  await page
    .getByRole("dialog")
    .getByRole("button", { name: "Close", exact: true })
    .click()
  const layout = await page.evaluate(() => {
    const table = document.querySelector("table")
    const cells = [...table.querySelectorAll("thead th")]
    return {
      overflow: document.documentElement.scrollWidth > innerWidth,
      left: getComputedStyle(cells[0]).position,
      right: getComputedStyle(cells.at(-1)).position,
    }
  })
  assert.equal(layout.overflow, false)
  assert.equal(layout.left, "sticky")
  assert.equal(layout.right, "sticky")
  await page.unroute(new RegExp(`${endpoint}$`))
  const large = await page.evaluate(
    async ({ entity, owner }) => {
      const { iamApi } = await import("/src/lib/iam-api.ts")
      const key = crypto.randomUUID()
      const body = {
        title: "Large contract replay acceptance",
        type: "TASK",
        objective: "Verify replay for maximum-sized clauses",
        description: "",
        accountable_principal_id: owner,
        criteria: Array.from({ length: 50 }, (_, i) => ({
          text: `${i} ` + "条".repeat(1900),
          verification_method: "验".repeat(2000),
        })),
      }
      const first = await iamApi.createWorkItem(entity, body, key)
      const replay = await iamApi.createWorkItem(entity, body, key)
      const detail = await iamApi.workItem(entity, first.id)
      return {
        same: first.id === replay.id,
        criteria: detail.criteria.length,
        contract: detail.contract.version_no,
      }
    },
    { entity, owner: checks.detail.accountable_principal_id }
  )
  assert.deepEqual(large, { same: true, criteria: 50, contract: 1 })
  await page.evaluate(
    async ({ entity, owner }) => {
      const { iamApi } = await import("/src/lib/iam-api.ts")
      for (let i = 1; i <= 71; i++) {
        await iamApi.createWorkItem(
          entity,
          {
            title: `Pagination acceptance fixture ${i}`,
            type: "TASK",
            objective: "Verify numbered pagination and bounded table scrolling",
            description: "",
            accountable_principal_id: owner,
            criteria: [
              {
                text: "The table remains usable across pages",
                verification_method: "Navigate pages and scroll both axes",
              },
            ],
          },
          `work-pagination-acceptance-v1-${entity}-${i}`
        )
      }
    },
    { entity, owner: checks.detail.accountable_principal_id }
  )
  await page.goto(`${path}?limit=10&title=Pagination+acceptance+fixture`)
  const pagination = page.getByRole("navigation", {
    name: "Pagination",
    exact: true,
  })
  await pagination
    .getByRole("button", { name: "Page 8", exact: true })
    .waitFor()
  assert.equal(
    await pagination.locator('[data-slot="pagination-ellipsis"]').count(),
    1
  )
  await pagination.getByRole("button", { name: "Page 2", exact: true }).click()
  await page.waitForURL((url) => url.searchParams.get("offset") === "10")
  await pagination
    .getByRole("button", { name: "Page 2", exact: true })
    .and(page.locator('[aria-current="page"]'))
    .waitFor()
  await page
    .locator("tbody tr")
    .first()
    .getByRole("cell", { name: "11", exact: true })
    .waitFor()
  const scroll = await page.evaluate(() => {
    const table = document.querySelector('[data-slot="auto-table-data"]')
    const viewport = table.parentElement
    viewport.scrollLeft = 300
    viewport.scrollTop = 200
    const footer = document
      .querySelector('[data-slot="table-footer"]')
      .getBoundingClientRect()
    return {
      x: viewport.scrollLeft,
      y: viewport.scrollTop,
      footerBottom: footer.bottom,
      height: innerHeight,
      overflow: document.documentElement.scrollWidth > innerWidth,
    }
  })
  assert(scroll.x > 0, "table scrolls horizontally")
  assert(scroll.y > 0, "table scrolls vertically")
  assert.equal(scroll.overflow, false)
  assert(
    scroll.footerBottom <= scroll.height &&
      scroll.footerBottom > scroll.height - 70,
    "footer fills the workspace bottom"
  )
  await page.reload()
  await page.getByRole("searchbox", { name: "Title", exact: true }).waitFor()
  assert.equal(
    await page
      .getByRole("searchbox", { name: "Title", exact: true })
      .inputValue(),
    "Pagination acceptance fixture"
  )
  assert.deepEqual(errors, [])
  await context.close()
  console.log(
    JSON.stringify({
      result: "PASS",
      url: baseURL + path,
      verified: [
        "real create/list/detail",
        "lost-response replay",
        "duplicate-submit lock",
        "stable criterion IDs",
        "snapshot hash",
        "instance isolation",
        "URL filters",
        "sticky columns",
        "mobile dialog",
        "large Unicode replay",
        "criteria validation feedback",
        "numbered pagination with ellipsis",
        "both scroll axes and bottom footer",
        "filter URL restoration",
      ],
    })
  )
} finally {
  await browser.close()
}
