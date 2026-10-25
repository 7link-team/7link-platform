// Real isolated API and browser acceptance. No credentials or tokens are logged.
import assert from "node:assert/strict"
import { chromium } from "playwright"

const baseURL = process.env.IAM_CONSOLE_URL ?? "http://127.0.0.1:4317"
assert(["localhost", "127.0.0.1"].includes(new URL(baseURL).hostname))
const browser = await chromium.launch({ channel: "chrome", headless: true })
try {
  const context = await browser.newContext({
    baseURL,
    viewport: { width: 1366, height: 768 },
  })
  await context.addCookies([
    { name: "NEXT_LOCALE", value: "en-US", url: baseURL },
  ])
  const page = await context.newPage()
  const errors = []
  page.on("pageerror", (error) => errors.push(error.message))
  await page.goto("/admin/login")
  const tenant = await page.evaluate(async () => {
    const { devAccountsForEntrance } =
      await import("/src/lib/dev-login-accounts.ts")
    return devAccountsForEntrance("tenant").find(
      (account) => account.id === "tenant-admin"
    )?.tenantID
  })
  assert(tenant)
  await page.getByRole("button", { name: "Sign in", exact: true }).click()
  await page.waitForURL((url) => url.pathname !== "/admin/login")
  const tenantLink = page.locator(`a[href="/admin/${tenant}"]`)
  await page
    .getByRole("button", { name: "Switch tenant", exact: true })
    .or(tenantLink)
    .first()
    .waitFor()
  if (await tenantLink.isVisible()) await tenantLink.click()
  await page
    .getByRole("button", { name: "Switch tenant", exact: true })
    .waitFor()
  const fixture = await page.evaluate(async (tenant) => {
    const { iamApi } = await import("/src/lib/iam-api.ts")
    const projects = await iamApi.projects(tenant, { limit: 20 })
    const entity = projects.items[0]?.project_id
    if (!entity) throw new Error("Real instance fixture is required")
    // Use the actual authenticated persona via the existing member directory.
    const members = await iamApi.members()
    const subject =
      members.find(
        (member) =>
          member.status === "active" && member.email?.includes("tenant-admin")
      )?.subject ??
      members.find((member) => member.status === "active")?.subject
    if (!subject) throw new Error("Active Human fixture required")
    const title = `Readiness acceptance ${Date.now().toString(36)}`
    const work = await iamApi.createWorkItem(
      entity,
      {
        title,
        description: "",
        type: "TASK",
        objective: "Verify preparation without changing the frozen contract",
        accountable_principal_id: subject,
        criteria: [
          {
            text: "Actual behavior satisfies this clause",
            verification_method: "Exercise actual behavior",
          },
        ],
      },
      crypto.randomUUID()
    )
    const detail = await iamApi.workItem(entity, work.id)
    return { entity, id: work.id, title, contract: detail.contract }
  }, tenant)
  const path = `/admin/${tenant}/entities/${fixture.entity}/work-items?title=${encodeURIComponent(fixture.title)}`
  const endpoint = `/api/entities/${fixture.entity}/work-items/${fixture.id}`
  await page.goto(path)
  await page
    .getByRole("button", { name: "Work item details", exact: true })
    .click()
  const dialog = page.getByRole("dialog")
  const region = dialog.getByRole("region", {
    name: "Preparation and readiness",
  })
  await region.waitFor()
  const save = region.getByRole("button", {
    name: "Save and check",
    exact: true,
  })
  const estimate = region.getByRole("spinbutton", {
    name: "Estimated minutes (15–4800)",
  })
  await estimate.fill("30")
  await region
    .getByRole("checkbox", {
      name: "I confirm the exclusions, including when there are none.",
    })
    .check()
  await region.getByRole("button", { name: "Add context", exact: true }).click()
  await region
    .getByRole("textbox", { name: "Web URL", exact: true })
    .fill("https://example.test/readiness")
  await region
    .getByRole("combobox", { name: "Primary verification method", exact: true })
    .selectOption("HUMAN")
  await region
    .getByRole("textbox", {
      name: "Verification steps and expected result",
      exact: true,
    })
    .fill("Exercise the actual feature and compare with the acceptance clause")
  await region
    .getByRole("checkbox", { name: "Configure budget (unchecked clears it)" })
    .check()
  await region
    .getByRole("textbox", { name: "USD limit in microdollars (optional)" })
    .fill("9007199254740993")
  const bodies = []
  let loseResponse = true
  await page.route(`**${endpoint}`, async (route) => {
    if (route.request().method() !== "PATCH") return route.continue()
    bodies.push({
      key: route.request().headers()["idempotency-key"],
      body: route.request().postDataJSON(),
    })
    const response = await route.fetch()
    if (loseResponse) {
      loseResponse = false
      return route.abort("failed")
    }
    return route.fulfill({ response })
  })
  await save.click()
  await region.getByRole("alert").first().waitFor()
  await save.click()
  await region
    .getByText(
      "Preparation passed. Execution still requires current system guards."
    )
    .waitFor()
  assert.equal(bodies.length, 2)
  assert.equal(bodies[0].key, bodies[1].key)
  assert.deepEqual(bodies[0].body, bodies[1].body)
  const read = () =>
    page.evaluate(async ({ entity, id }) => {
      const { iamApi } = await import("/src/lib/iam-api.ts")
      return iamApi.workItem(entity, id)
    }, fixture)
  let current = await read()
  assert.equal(current.state, "READY")
  assert.equal(current.version, 2)
  assert.equal(current.preparation.budget.usd_micros, "9007199254740993")
  assert.deepEqual(current.contract, fixture.contract)
  await estimate.fill("")
  await save.click()
  await region.getByText("Estimated effort is missing.").waitFor()
  current = await read()
  assert.equal(current.state, "BLOCKED")
  assert.equal(current.preparation.estimate_minutes, undefined)
  await estimate.fill("30")
  await save.click()
  await region
    .getByText(
      "Preparation passed. Execution still requires current system guards."
    )
    .waitFor()
  await region
    .getByRole("checkbox", { name: "Configure budget (unchecked clears it)" })
    .uncheck()
  await save.click()
  await region
    .getByText("Missing preparation information", { exact: false })
    .waitFor()
  current = await read()
  assert.equal(current.state, "DRAFT")
  assert.equal(current.preparation.budget, undefined)
  await region
    .getByRole("checkbox", { name: "Configure budget (unchecked clears it)" })
    .check()
  await save.click()
  await region
    .getByText(
      "Preparation passed. Execution still requires current system guards."
    )
    .waitFor()
  const previous = await read()
  await region.getByRole("button", { name: "Check again", exact: true }).click()
  await region
    .getByText(`Ready · v${previous.version + 1}`, { exact: true })
    .waitFor()
  current = await read()
  assert.equal(current.preparation.last_check.trigger, "MANUAL")
  assert.deepEqual(current.contract, fixture.contract)
  // An update committed elsewhere must reject this stale form and preserve input.
  await page.evaluate(async ({ entity, id }) => {
    const { iamApi } = await import("/src/lib/iam-api.ts")
    const current = await iamApi.workItem(entity, id)
    await iamApi.checkWorkReadiness(
      entity,
      id,
      current.version,
      crypto.randomUUID()
    )
  }, fixture)
  await estimate.fill("45")
  await save.click()
  await region.getByRole("alert").first().waitFor()
  assert.equal(await estimate.inputValue(), "45")
  const refreshedVersion = (await read()).version
  await region.getByRole("button", { name: "Refresh", exact: true }).click()
  await region
    .getByText(`Ready · v${refreshedVersion}`, { exact: true })
    .waitFor()
  await estimate.fill("60")
  let release
  const responseGate = new Promise((resolve) => {
    release = resolve
  })
  let committed
  const committedGate = new Promise((resolve) => {
    committed = resolve
  })
  await page.unroute(`**${endpoint}`)
  await page.route(`**${endpoint}`, async (route) => {
    if (route.request().method() !== "PATCH") return route.continue()
    const response = await route.fetch()
    assert.equal(response.status(), 200)
    committed()
    await responseGate
    await route.fulfill({ response })
  })
  await save.click()
  await committedGate
  await dialog.getByRole("button", { name: "Close", exact: true }).click()
  release()
  await page.waitForResponse(
    (response) =>
      response.url().endsWith(endpoint) && response.request().method() === "GET"
  )
  assert.equal(
    await page.getByRole("dialog").count(),
    0,
    "late save must not reopen closed detail"
  )
  await page
    .getByRole("button", { name: "Work item details", exact: true })
    .click()
  await estimate.waitFor()
  assert.equal(await estimate.inputValue(), "60")
  await page.setViewportSize({ width: 390, height: 844 })
  assert(
    await page.evaluate(
      () => document.documentElement.scrollWidth <= innerWidth
    ),
    "mobile page overflow"
  )
  const box = await dialog.boundingBox()
  assert(
    box &&
      box.x >= 0 &&
      box.y >= 0 &&
      box.x + box.width <= 391 &&
      box.y + box.height <= 845
  )
  assert.deepEqual(errors, [])
  console.log(
    JSON.stringify({
      result: "PASS",
      path,
      work: fixture.id,
      scenarios: [
        "real preparation",
        "exact budget",
        "same-key replay",
        "clear optional estimate",
        "budget omission",
        "manual check",
        "immutable contract",
        "stale version",
        "close during save",
        "mobile fit",
      ],
    })
  )
} finally {
  await browser.close()
}
