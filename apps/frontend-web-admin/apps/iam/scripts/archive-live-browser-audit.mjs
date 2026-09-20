import assert from "node:assert/strict"
import { createHash } from "node:crypto"
import { mkdirSync, readFileSync } from "node:fs"
import { fileURLToPath } from "node:url"
import { chromium } from "playwright"
import { auditPassword } from "./audit-password.mjs"

const baseURL = process.env.IAM_CONSOLE_URL
const control = process.env.IAM_ARCHIVE_FIXTURE_CONTROL
assert(baseURL && control, "run through TestComposedArchiveBrowserAcceptance")
const password = auditPassword()
const screenshots = new URL(
  `../../../../../.local/screenshots/archive-live/${Date.now()}/`,
  import.meta.url
)
mkdirSync(screenshots, { recursive: true })
const browser = await chromium.launch({
  channel: process.env.IAM_BROWSER_CHANNEL ?? "chrome",
  headless: true,
})
const failures = []
const headers = () => ({ Origin: baseURL })
async function login(context, name) {
  const response = await context.request.post("/api/sessions", {
    headers: headers(),
    data: { login_name: name, password, return_url: baseURL + "/" },
  })
  assert.equal(response.status(), 200, "fixture browser login")
}
async function get(context, path, tenant) {
  const response = await context.request.get("/api" + path, {
    headers: headers(tenant),
  })
  assert.equal(response.status(), 200, path)
  return (await response.json()).data
}
async function capture(page, name) {
  await page.evaluate(() =>
    Promise.all(document.getAnimations().map((a) => a.finished.catch(() => {})))
  )
  assert(
    await page.evaluate(
      () => document.documentElement.scrollWidth <= innerWidth
    )
  )
  const dialog = page.getByRole("dialog")
  if (await dialog.count()) {
    const box = await dialog.boundingBox()
    const viewport = page.viewportSize()
    assert(
      box.x >= 0 &&
        box.y >= 0 &&
        box.x + box.width <= viewport.width + 1 &&
        box.y + box.height <= viewport.height + 1
    )
  }
  await page.screenshot({
    path: fileURLToPath(new URL(name + ".png", screenshots)),
  })
}
async function inspect(page, context, prefix, tenant, job, name) {
  await page
    .getByRole("button", { name: `校验归档 ${job.id}`, exact: true })
    .click()
  await page
    .getByRole("dialog")
    .getByText("版本、内容和保留锁校验通过", { exact: true })
    .waitFor()
  await capture(page, name + "-verified")
  await page.keyboard.press("Escape")
  const downloadEvent = page.waitForEvent("download")
  await page
    .getByRole("button", { name: `下载归档 ${job.id}`, exact: true })
    .click()
  const download = await downloadEvent
  assert.equal(await download.failure(), null)
  assert.equal(download.suggestedFilename(), `audit-archive-${job.id}.ndjson`)
  const bytes = readFileSync(await download.path())
  const jobs = await get(context, prefix + "/archives", tenant)
  const persisted = jobs.find((item) => item.id === job.id)
  assert.equal(bytes.length, persisted.object.size)
  assert.equal(
    createHash("sha256").update(bytes).digest("hex"),
    persisted.object.sha256
  )
  assert(persisted.object.version_id && persisted.object.version_id !== "null")
  const records = bytes
    .toString("utf8")
    .trim()
    .split("\n")
    .map((line) => JSON.parse(line))
  assert.equal(records[0].type, "manifest")
  assert.equal(records.at(-1).type, "complete")
  assert.equal(
    records.at(-1).exported_events,
    job.head_sequence - job.after_sequence
  )
  assert.equal(records.length, records.at(-1).exported_events + 2)
  await capture(page, name + "-jobs")
  return persisted
}
try {
  const recovered = await fetch(control + "/restore-storage", {
    method: "POST",
  })
  assert.equal(recovered.status, 204)
  let tenantArchive
  for (const [name, viewport, tenant, account] of [
    ["desktop", { width: 1440, height: 1000 }, "91001", "admin-a"],
    ["mobile", { width: 390, height: 844 }, "91002", "admin-b"],
  ]) {
    const context = await browser.newContext({
      baseURL,
      viewport,
      acceptDownloads: true,
      reducedMotion: "reduce",
    })
    context.setDefaultTimeout(65000)
    await login(context, account)
    await context.addInitScript(
      (value) => localStorage.setItem("iam.tenant", value),
      tenant
    )
    const page = await context.newPage()
    page.on("pageerror", (error) => failures.push(error.message))
    page.on("request", (request) => {
      if (
        new URL(request.url()).pathname.startsWith("/api/platform/audit")
      ) {
        assert(!new URL(request.url()).pathname.includes("/tenants/"))
      }
    })
    page.on("response", (response) => {
      if (response.url().includes("/api/") && response.status() >= 400)
        failures.push(
          `${response.status()} ${new URL(response.url()).pathname}`
        )
    })
    await page.goto("/audit-governance")
    const prefix = "/audit"
    let jobs = await get(context, prefix + "/archives", tenant)
    assert(jobs.length >= 1)
    let job = jobs[0]
    if (job.state === "failed") {
      await page
        .getByRole("button", { name: `重试归档 ${job.id}`, exact: true })
        .click()
    }
    await page
      .getByRole("button", { name: `下载归档 ${job.id}`, exact: true })
      .waitFor()
    if (name === "desktop") {
      const queued = page.waitForResponse(
        (response) =>
          response.request().method() === "POST" &&
          new URL(response.url()).pathname === "/api/audit/archives"
      )
      await page
        .getByRole("button", { name: "创建归档任务", exact: true })
        .click()
      const response = await queued
      assert(response.ok())
      job = (await response.json()).data
      assert.equal(job.after_sequence, 500)
      assert.equal(job.head_sequence, 501)
      await page
        .getByRole("button", { name: `下载归档 ${job.id}`, exact: true })
        .waitFor()
    }
    const verified = await inspect(
      page,
      context,
      prefix,
      tenant,
      job,
      name + "-tenant"
    )
    if (name === "desktop") tenantArchive = verified.id
    const status = await get(context, prefix + "/archive-status", tenant)
    assert.equal(status.enabled, true)
    assert.equal(status.pending_events, 0)
    assert.equal(status.archived_events, name === "desktop" ? 501 : 2)
    await page.getByRole("tab", { name: "平台", exact: true }).click()
    await page
      .getByRole("heading", { name: "平台审计治理", exact: true })
      .waitFor()
    jobs = await get(context, "/platform/audit/archives")
    const platformJob = jobs[0]
    if (platformJob.state === "failed") {
      await page
        .getByRole("button", {
          name: `重试归档 ${platformJob.id}`,
          exact: true,
        })
        .click()
    }
    await page
      .getByRole("button", { name: `下载归档 ${platformJob.id}`, exact: true })
      .waitFor()
    await inspect(
      page,
      context,
      "/platform/audit",
      undefined,
      platformJob,
      name + "-platform"
    )
    if (name === "mobile") {
      const foreign = await context.request.get(
        `/api/audit/archives/${tenantArchive}/download`,
        { headers: headers(tenant) }
      )
      assert.equal(
        foreign.status(),
        404,
        "other tenant's object must remain inaccessible"
      )
    }
    await context.close()
    console.log(
      `${name}: real database/worker/MinIO archive verification and download PASS`
    )
  }
  const member = await browser.newContext({ baseURL })
  await login(member, "requester-b")
  for (const [path, tenant] of [
    ["/audit/archive-status", "91002"],
    ["/platform/audit/archive-status", undefined],
  ]) {
    const denied = await member.request.get("/api" + path, {
      headers: headers(tenant),
    })
    assert.equal(
      denied.status(),
      403,
      "ordinary membership is not archive administration"
    )
  }
  await member.close()
  assert.deepEqual(failures, [])
  console.log("screenshots: " + fileURLToPath(screenshots))
} finally {
  await browser.close()
}
