// Real isolated API/browser acceptance; uses the prefilled development persona.
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
    const entity = projects.items.find(
      (project) => !["CLOSED", "CANCELLED"].includes(project.stage)
    )?.project_id
    if (!entity) throw new Error("An open real project is required")
    return {
      entity,
      summary: `Constitution acceptance ${Date.now().toString(36)}`,
    }
  }, tenant)
  const body = "# Delivery rules\n\nRun real checks and retain the evidence."
  await page.goto(
    `/admin/${tenant}/entities/${fixture.entity}/project?view=constitution`
  )
  await page
    .getByRole("button", { name: "Propose constitution", exact: true })
    .click()
  let dialog = page.getByRole("dialog")
  await dialog
    .getByRole("textbox", { name: "Change summary", exact: true })
    .fill(fixture.summary)
  await dialog
    .getByRole("textbox", { name: "Constitution (Markdown)", exact: true })
    .fill(body)
  const proposals = []
  let lose = true
  await page.route(
    `**/api/entities/${fixture.entity}/project/constitution-versions`,
    async (route) => {
      if (route.request().method() !== "POST") return route.continue()
      proposals.push({
        key: route.request().headers()["idempotency-key"],
        body: route.request().postDataJSON(),
      })
      const response = await route.fetch()
      if (lose) {
        lose = false
        return route.abort("failed")
      }
      return route.fulfill({ response })
    }
  )
  const propose = dialog.getByRole("button", {
    name: "Propose constitution",
    exact: true,
  })
  await propose.click()
  await dialog.getByRole("alert").first().waitFor()
  assert.equal(
    await dialog
      .getByRole("textbox", { name: "Constitution (Markdown)", exact: true })
      .inputValue(),
    body
  )
  await propose.click()
  await dialog.waitFor({ state: "hidden" })
  assert.equal(proposals.length, 2)
  assert.deepEqual(proposals[0], proposals[1])
  await page.unroute(
    `**/api/entities/${fixture.entity}/project/constitution-versions`
  )
  await page
    .getByRole("searchbox", { name: "Change summary", exact: true })
    .fill(fixture.summary)
  await page.getByRole("button", { name: "Search", exact: true }).click()
  await page.waitForURL(
    (url) => url.searchParams.get("constitutionSummary") === fixture.summary
  )
  const details = page
    .getByRole("row")
    .filter({ hasText: fixture.summary })
    .getByRole("button", { name: "Constitution details", exact: true })
  await details.waitFor()
  await page.waitForFunction((summary) => {
    const buttons = [
      ...document.querySelectorAll('button[aria-label="Constitution details"]'),
    ]
    return (
      buttons.length === 1 &&
      buttons[0].closest("tr")?.textContent.includes(summary)
    )
  }, fixture.summary)
  assert.equal(await details.count(), 1)
  await details.click()
  dialog = page.getByRole("dialog")
  await dialog
    .getByRole("button", { name: "Submit for review", exact: true })
    .click()
  await dialog
    .getByRole("button", { name: "Record decision", exact: true })
    .waitFor()
  const read = () =>
    page.evaluate(async ({ entity, summary }) => {
      const { iamApi } = await import("/src/lib/iam-api.ts")
      const result = await iamApi.constitutions(entity, {
        limit: 50,
        change_summary: `like:${summary}`,
      })
      if (result.items.length !== 1)
        throw new Error("Expected exactly one proposal")
      return iamApi.constitution(result.items[0].id)
    }, fixture)
  let current = await read()
  assert.equal(current.state, "IN_REVIEW")
  assert.equal(current.body, body)
  const comment = dialog.getByRole("textbox", {
    name: "Review comment",
    exact: true,
  })
  await comment.fill("Verified actual commands")
  const decisions = []
  lose = true
  await page.route(
    `**/api/constitution-versions/${current.id}/decision`,
    async (route) => {
      decisions.push({
        key: route.request().headers()["idempotency-key"],
        match: route.request().headers()["if-match"],
        body: route.request().postDataJSON(),
      })
      const response = await route.fetch()
      if (lose) {
        lose = false
        return route.abort("failed")
      }
      return route.fulfill({ response })
    }
  )
  const decide = dialog.getByRole("button", {
    name: "Record decision",
    exact: true,
  })
  await decide.click()
  await dialog.getByRole("alert").first().waitFor()
  assert.equal(await comment.inputValue(), "Verified actual commands")
  await decide.click()
  await decide.waitFor({ state: "hidden" })
  assert.equal(decisions.length, 2)
  assert.deepEqual(decisions[0], decisions[1])
  current = await read()
  assert.equal(current.state, "ACTIVE")
  assert.equal(current.body, body)
  assert.equal(current.review_comment, "Verified actual commands")
  const original = current
  await dialog
    .getByRole("button", { name: "Close", exact: true })
    .first()
    .click()
  await page.unroute(`**/api/constitution-versions/${current.id}/decision`)
  // A second real approval supersedes the old active version and updates the project pointer.
  await page.evaluate(
    async ({ entity, original }) => {
      const { iamApi } = await import("/src/lib/iam-api.ts")
      const next = await iamApi.proposeConstitution(
        entity,
        {
          body: "# Updated rules\nKeep immutable evidence",
          change_summary: "Replacement acceptance",
        },
        crypto.randomUUID()
      )
      const review = await iamApi.submitConstitution(
        next.id,
        next.version,
        crypto.randomUUID()
      )
      await iamApi.decideConstitution(
        next.id,
        review.version,
        { result: "APPROVE", comment: "Checked replacement" },
        crypto.randomUUID()
      )
      const old = await iamApi.constitution(original.id)
      if (
        old.state !== "SUPERSEDED" ||
        old.body !== original.body ||
        old.body_sha256 !== original.body_sha256
      )
        throw new Error("Immutable superseding failed")
      const project = await iamApi.project(entity)
      if (project.constitution_version_no !== next.version_no)
        throw new Error("Active project pointer differs")
    },
    { entity: fixture.entity, original }
  )
  await page.reload()
  await details.waitFor()
  assert.equal(
    await page
      .getByRole("searchbox", { name: "Change summary", exact: true })
      .inputValue(),
    fixture.summary
  )
  const recovery = await page.evaluate(async ({ entity, summary }) => {
    const { iamApi } = await import("/src/lib/iam-api.ts")
    return iamApi.proposeConstitution(
      entity,
      { body: "# Recovery rules", change_summary: `${summary} recovery` },
      crypto.randomUUID()
    )
  }, fixture)
  await page
    .getByRole("tab", { name: "Instance overview", exact: true })
    .click()
  await page
    .getByRole("region", { name: "Project details", exact: true })
    .waitFor()
  let failIdentity = true
  await page.route("**/api/me", (route) => {
    if (failIdentity) return route.abort("failed")
    return route.continue()
  })
  await page
    .getByRole("tab", { name: "Project constitution", exact: true })
    .click()
  await page
    .getByText(
      "Unable to confirm the current proposer identity. Retry to enable submission of your drafts."
    )
    .waitFor()
  const recoveryDetails = page
    .getByRole("row")
    .filter({ hasText: `${fixture.summary} recovery` })
    .getByRole("button", { name: "Constitution details", exact: true })
  await recoveryDetails.click()
  dialog = page.getByRole("dialog")
  assert.equal(
    await dialog
      .getByRole("button", { name: "Submit for review", exact: true })
      .count(),
    0
  )
  await dialog
    .getByRole("button", { name: "Close", exact: true })
    .first()
    .click()
  failIdentity = false
  await page.getByRole("button", { name: "Retry now", exact: true }).click()
  await page
    .getByText(
      "Unable to confirm the current proposer identity. Retry to enable submission of your drafts."
    )
    .waitFor({ state: "hidden" })
  await page.unroute("**/api/me")
  await recoveryDetails.click()
  await dialog
    .getByRole("button", { name: "Submit for review", exact: true })
    .click()
  await dialog
    .getByRole("button", { name: "Record decision", exact: true })
    .waitFor()
  await dialog
    .getByRole("textbox", { name: "Review comment", exact: true })
    .fill("Preserve this stale review input")
  await page.evaluate(async (id) => {
    const { iamApi } = await import("/src/lib/iam-api.ts")
    const current = await iamApi.constitution(id)
    await iamApi.decideConstitution(
      id,
      current.version,
      { result: "REJECT", comment: "Reviewed elsewhere" },
      crypto.randomUUID()
    )
  }, recovery.id)
  await dialog
    .getByRole("button", { name: "Record decision", exact: true })
    .click()
  await dialog.getByRole("alert").first().waitFor()
  assert.equal(
    await dialog
      .getByRole("textbox", { name: "Review comment", exact: true })
      .inputValue(),
    "Preserve this stale review input"
  )
  await dialog.getByRole("button", { name: "Refresh", exact: true }).click()
  await dialog
    .getByRole("button", { name: "Record decision", exact: true })
    .waitFor({ state: "hidden" })
  await dialog.getByText("Reviewed elsewhere", { exact: true }).waitFor()
  await dialog
    .getByRole("button", { name: "Close", exact: true })
    .first()
    .click()
  const rejected = await page.evaluate(async ({ entity, summary }) => {
    const { iamApi } = await import("/src/lib/iam-api.ts")
    const draft = await iamApi.proposeConstitution(
      entity,
      { body: "# Incomplete rules", change_summary: `${summary} rejection` },
      crypto.randomUUID()
    )
    return iamApi.submitConstitution(
      draft.id,
      draft.version,
      crypto.randomUUID()
    )
  }, fixture)
  await page.getByRole("button", { name: "Refresh", exact: true }).click()
  await page
    .getByRole("row")
    .filter({ hasText: `${fixture.summary} rejection` })
    .getByRole("button", { name: "Constitution details", exact: true })
    .click()
  dialog = page.getByRole("dialog")
  await dialog
    .getByRole("combobox", { name: "Review decision", exact: true })
    .click()
  await page.getByRole("option", { name: "Reject", exact: true }).click()
  await dialog
    .getByRole("button", { name: "Record decision", exact: true })
    .click()
  await dialog.getByRole("alert").first().waitFor()
  const unchanged = await page.evaluate(async (id) => {
    const { iamApi } = await import("/src/lib/iam-api.ts")
    return iamApi.constitution(id)
  }, rejected.id)
  assert.equal(unchanged.state, "IN_REVIEW")
  await dialog
    .getByRole("textbox", { name: "Review comment", exact: true })
    .fill("Missing verification command")
  await dialog
    .getByRole("button", { name: "Record decision", exact: true })
    .click()
  await dialog
    .getByRole("button", { name: "Record decision", exact: true })
    .waitFor({ state: "hidden" })
  const refusal = await page.evaluate(async (id) => {
    const { iamApi } = await import("/src/lib/iam-api.ts")
    return iamApi.constitution(id)
  }, rejected.id)
  assert.equal(refusal.state, "REJECTED")
  assert.equal(refusal.review_comment, "Missing verification command")
  await dialog
    .getByRole("button", { name: "Close", exact: true })
    .first()
    .click()
  await page.evaluate(async ({ entity, summary }) => {
    const { iamApi } = await import("/src/lib/iam-api.ts")
    for (let i = 0; i < 12; i++)
      await iamApi.proposeConstitution(
        entity,
        {
          body: `# Pagination rules ${i}`,
          change_summary: `${summary} page ${i}`,
        },
        crypto.randomUUID()
      )
  }, fixture)
  await page
    .getByRole("combobox", { name: "Rows per page", exact: true })
    .selectOption("10")
  await page.getByRole("button", { name: "Page 2", exact: true }).click()
  await page.waitForURL(
    (url) => url.searchParams.get("constitutionOffset") === "10"
  )
  await page.getByRole("cell", { name: "11", exact: true }).waitFor()
  await page.reload()
  await page.getByRole("cell", { name: "11", exact: true }).waitFor()
  assert.equal(new URL(page.url()).searchParams.get("constitutionOffset"), "10")
  const downloadPending = page.waitForEvent("download")
  await page
    .getByRole("button", { name: "Export current page", exact: true })
    .click()
  const download = await downloadPending
  assert.match(download.suggestedFilename(), /\.csv$/)
  assert.equal(await download.failure(), null)
  await page.getByRole("button", { name: "Auto refresh", exact: true }).click()
  await page
    .getByRole("menuitemradio", { name: "Every 30 seconds", exact: true })
    .waitFor()
  await page.keyboard.press("Escape")
  const compared = await page.evaluate(async ({ entity, summary }) => {
    const { iamApi } = await import("/src/lib/iam-api.ts")
    const tail = Array.from(
      { length: 70 },
      (_, i) => `Unchanged rule ${i}`
    ).join("\n")
    const before = await iamApi.proposeConstitution(
      entity,
      {
        body: `# Comparison\nOld rule\n${tail}`,
        change_summary: `${summary} comparison base`,
      },
      crypto.randomUUID()
    )
    const after = await iamApi.proposeConstitution(
      entity,
      {
        body: `# Comparison\nNew rule <script>alert(1)</script>\n${tail}`,
        change_summary: `${summary} comparison target`,
      },
      crypto.randomUUID()
    )
    const identical = await iamApi.proposeConstitution(
      entity,
      {
        body: `# Comparison\nOld rule\n${tail}`,
        change_summary: `${summary} comparison identical`,
      },
      crypto.randomUUID()
    )
    return { before, after, identical }
  }, fixture)
  await page
    .getByRole("searchbox", { name: "Change summary", exact: true })
    .fill(`${fixture.summary} comparison`)
  await page.getByRole("button", { name: "Search", exact: true }).click()
  const baseRow = page
    .getByRole("row")
    .filter({ hasText: `${fixture.summary} comparison base` })
  await baseRow
    .getByRole("button", { name: "Select comparison base", exact: true })
    .click()
  await page
    .getByText(`Comparison base: v${compared.before.version_no}`, {
      exact: true,
    })
    .waitFor()
  assert(
    await baseRow
      .getByRole("button", { name: "Compare versions", exact: true })
      .isDisabled()
  )
  let failComparison = true
  await page.route(
    `**/api/constitution-versions/${compared.after.id}`,
    async (route) => {
      if (failComparison) {
        failComparison = false
        return route.abort("failed")
      }
      return route.continue()
    }
  )
  await page
    .getByRole("row")
    .filter({ hasText: `${fixture.summary} comparison target` })
    .getByRole("button", { name: "Compare versions", exact: true })
    .click()
  dialog = page.getByRole("dialog")
  await dialog.getByRole("alert").waitFor()
  await dialog.getByRole("button", { name: "Retry now", exact: true }).click()
  await dialog.getByRole("cell", { name: "Removed (−)", exact: true }).waitFor()
  await dialog.getByRole("cell", { name: "Added (+)", exact: true }).waitFor()
  await dialog
    .getByText("+ New rule <script>alert(1)</script>", { exact: true })
    .waitFor()
  assert.equal(await dialog.locator("script").count(), 0)
  await dialog.getByRole("button", { name: "Page 2", exact: true }).click()
  await dialog.getByText("Unchanged rule 69", { exact: false }).waitFor()
  await page.setViewportSize({ width: 390, height: 844 })
  assert(
    await dialog.evaluate((element) => {
      const bounds = element.getBoundingClientRect()
      return (
        bounds.left >= 0 &&
        bounds.right <= innerWidth &&
        bounds.bottom <= innerHeight
      )
    })
  )
  assert(
    await dialog
      .locator('[data-slot="auto-table-data"]')
      .locator("..")
      .evaluate((element) => element.scrollWidth > element.clientWidth)
  )
  await dialog
    .getByRole("button", { name: "Close", exact: true })
    .first()
    .click()
  await page.setViewportSize({ width: 1366, height: 768 })
  await page
    .getByRole("row")
    .filter({ hasText: `${fixture.summary} comparison identical` })
    .getByRole("button", { name: "Compare versions", exact: true })
    .click()
  dialog = page.getByRole("dialog")
  await dialog
    .getByText("These versions have identical content.", { exact: true })
    .waitFor()
  assert.equal(
    await dialog
      .getByRole("cell", { name: "Removed (−)", exact: true })
      .count(),
    0
  )
  await dialog
    .getByRole("button", { name: "Close", exact: true })
    .first()
    .click()
  await page.getByRole("button", { name: "Refresh", exact: true }).click()
  await page
    .getByText(`Comparison base: v${compared.before.version_no}`, {
      exact: true,
    })
    .waitFor()
  await page
    .getByRole("button", { name: "Clear comparison base", exact: true })
    .click()
  assert(
    await baseRow
      .getByRole("button", { name: "Compare versions", exact: true })
      .isDisabled()
  )
  await page.setViewportSize({ width: 390, height: 844 })
  const dimensions = await page.evaluate(() => ({
    width: document.documentElement.clientWidth,
    scroll: document.documentElement.scrollWidth,
    sticky: [...document.querySelectorAll("th")].map(
      (element) => getComputedStyle(element).position
    ),
  }))
  assert(dimensions.scroll <= dimensions.width + 1)
  assert(dimensions.sticky.filter((value) => value === "sticky").length >= 2)
  assert.deepEqual(errors, [])
  console.log(
    "PASS: constitution lifecycle/retries, immutable content/hash, identity recovery, comparison base/direction/line changes/retry/identical bodies/XSS text/paging/mobile scroll, filter/page restoration, CSV, auto-refresh and sticky columns"
  )
} finally {
  await browser.close()
}
