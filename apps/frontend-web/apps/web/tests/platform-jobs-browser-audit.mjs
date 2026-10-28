import assert from "node:assert/strict"
import { chromium } from "playwright"

// Isolated API doubles only: this audit must never send job mutations to a service.
const baseURL = process.env.PLATFORM_JOBS_FIXTURE_URL
assert(
  baseURL && ["localhost", "127.0.0.1"].includes(new URL(baseURL).hostname),
  "An isolated local Vite fixture URL is required"
)
const browser = await chromium.launch({ channel: "chrome", headless: true })
try {
  const page = await browser.newPage({
    viewport: { width: 1440, height: 1000 },
  })
  const errors = []
  page.on("pageerror", (error) => errors.push(error.message))
  const fixture = `${baseURL}/tests/fixtures/platform-jobs.html`
  await page.goto(fixture)
  const rows = page.locator("tbody tr")
  await rows.nth(2).waitFor()
  const retry = page.getByRole("button", {
    name: "Retry selected jobs (0/100)",
    exact: true,
  })
  assert(await retry.isDisabled())
  assert(await page.locator("#jobs-retry-selection-help").isVisible())
  assert(
    !(await page.locator("#jobs-retry-selection-help").innerText()).includes(
      "iam."
    )
  )
  assert(
    await page
      .getByRole("columnheader", { name: "Select for retry", exact: true })
      .isVisible()
  )
  assert.equal(await rows.nth(0).getByRole("checkbox").isDisabled(), false)
  assert(await rows.nth(1).getByRole("checkbox").isDisabled())
  assert(await rows.nth(2).getByRole("checkbox").isDisabled())
  assert((await rows.nth(2).innerText()).includes("System-wide"))
  assert(
    await rows
      .nth(2)
      .getByRole("button", { name: "125", exact: true })
      .isVisible()
  )
  for (const text of [
    "agents.webhook_wake",
    "Wake waiting agents",
    "Delivers due webhook wake signals.",
    "2026-10-20T18:00:00Z",
  ])
    assert((await rows.nth(2).innerText()).includes(text), text)
  await rows
    .nth(2)
    .getByRole("button", { name: "Details", exact: true })
    .click()
  const dialog = page.getByRole("dialog")
  await dialog.waitFor()
  assert((await dialog.innerText()).includes("125"))
  assert((await dialog.innerText()).includes("System-wide"))
  assert((await dialog.innerText()).includes("Wake waiting agents"))
  assert(
    await dialog
      .getByRole("button", { name: "Retry job", exact: true })
      .isDisabled()
  )
  assert.equal(
    await dialog
      .getByRole("button", { name: "Cancel job", exact: true })
      .isDisabled(),
    false
  )
  await page.keyboard.press("Escape")
  await rows.nth(0).getByRole("checkbox").click()
  const selectedRetry = page.getByRole("button", {
    name: "Retry selected jobs (1/100)",
    exact: true,
  })
  assert.equal(await selectedRetry.isDisabled(), false)
  await selectedRetry.click()
  await dialog.waitFor()
  await dialog.getByRole("textbox").fill("Reviewed fixture failure")
  await dialog.getByRole("button", { name: "Confirm", exact: true }).click()
  await dialog.waitFor({ state: "hidden" })
  assert((await rows.nth(0).innerText()).includes("Pending"))
  assert(await rows.nth(0).getByRole("checkbox").isDisabled())

  await page.goto(`${fixture}?readonly`)
  await rows.nth(2).waitFor()
  assert.equal(await page.getByRole("checkbox").count(), 0)
  assert.equal(
    await page.getByRole("button", { name: /Retry selected jobs/ }).count(),
    0
  )
  await rows
    .nth(2)
    .getByRole("button", { name: "Details", exact: true })
    .click()
  await dialog.waitFor()
  assert.equal(
    await dialog
      .getByRole("button", { name: "Cancel job", exact: true })
      .count(),
    0
  )
  await page.keyboard.press("Escape")
  await page.setViewportSize({ width: 390, height: 844 })
  await page
    .getByRole("button")
    .filter({ hasText: "Dispatch scheduled work to tenants" })
    .click()
  await dialog.waitFor()
  assert((await dialog.innerText()).includes("agents.webhook_wake"))
  assert(
    await page.evaluate(
      () => document.documentElement.scrollWidth <= innerWidth
    )
  )
  assert.deepEqual(errors, [])
  console.log(
    "PASS: failed-job retry selection, pending-job details, frozen fanout context, platform scope, viewer permissions and mobile details"
  )
} finally {
  await browser.close()
}
