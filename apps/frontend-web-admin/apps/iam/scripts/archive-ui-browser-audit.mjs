// UI contract fixtures only. Real database/S3 acceptance is a separate gate.
import assert from "node:assert/strict"
import { mkdirSync } from "node:fs"
import { fileURLToPath } from "node:url"
import { chromium } from "playwright"

const baseURL = process.env.IAM_CONSOLE_URL ?? "http://127.0.0.1:8093"
const screenshots = new URL(
  "../../../../../.local/screenshots/archive-ui/",
  import.meta.url
)
mkdirSync(screenshots, { recursive: true })
const browser = await chromium.launch({
  channel: process.env.IAM_BROWSER_CHANNEL ?? "chrome",
  headless: true,
})
const errors = []
const object = {
  bucket: "audit-fixture",
  key: "audit/archives/11/1001.ndjson",
  version_id: "immutable-version-1",
  sha256: "a".repeat(64),
  size: 200,
  retain_until: "2030-09-01T00:00:00Z",
}
const makeJob = (id, state = "complete") => ({
  id: String(id),
  tenant_id: "11",
  after_sequence: 0,
  head_sequence: 2,
  state,
  attempts: 1,
  next_attempt: 0,
  error_code: state === "failed" ? "storage_unavailable" : "",
  object: state === "complete" ? object : undefined,
  retain_until: object.retain_until,
  created_at: "2026-09-01T00:00:00Z",
  updated_at: "2026-09-01T00:00:00Z",
})
async function fits(page) {
  await page.evaluate(() =>
    Promise.all(
      document
        .getAnimations()
        .map((animation) => animation.finished.catch(() => {}))
    )
  )
  assert(
    await page.evaluate(
      () => document.documentElement.scrollWidth <= innerWidth
    ),
    "page horizontal overflow"
  )
  const dialog = page.getByRole("dialog")
  if (await dialog.count()) {
    const box = await dialog.boundingBox()
    const viewport = page.viewportSize()
    assert(
      box.x >= 0 &&
        box.y >= 0 &&
        box.x + box.width <= viewport.width + 1 &&
        box.y + box.height <= viewport.height + 1,
      "dialog overflow"
    )
  }
}
try {
  for (const [name, viewport] of [
    ["desktop", { width: 1440, height: 1000 }],
    ["mobile", { width: 390, height: 844 }],
  ]) {
    const context = await browser.newContext({ baseURL, viewport })
    await context.addCookies([
      { name: "NEXT_LOCALE", value: "zh-CN", url: baseURL },
    ])
    const page = await context.newPage()
    page.on("pageerror", (error) => errors.push(error.message))
    const calls = []
    let enabled = true
    let records = Array.from({ length: 23 }, (_, i) =>
      makeJob(1001 + i, i === 1 ? "failed" : "complete")
    )
    let corrupt = false
    let rejectPlatform = false
    let blockVerify = null
    let verifyStarted = null
    const policy = {
      tenant_id: "11",
      min_days: 365,
      archive_after_days: 730,
      updated_at: "0001-01-01T00:00:00Z",
    }
    await context.route("**/api/**", async (route) => {
      const request = route.request()
      assert.equal(request.headers()["x-tenant-id"], undefined)
      const url = new URL(request.url())
      const path = url.pathname.slice(4)
      const platform = path.startsWith("/system/")
      const tenant = /^\/tenants\/([^/]+)/.exec(path)?.[1]
      calls.push({ path, method: request.method(), tenant })
      if (platform)
        assert.equal(tenant, undefined, "platform API must omit tenant path")
      const json = (data) =>
        route.fulfill({ json: { code: 0, message: "ok", data } })
      if (path === "/me")
        return json({
          subject: "1",
          organization_id: "11",
          display_name: "Archive operator",
          email: "operator@example.test",
        })
      if (path.endsWith("/principals/1/permissions"))
        return json([
          "audit_event_export",
          "audit_event_manage",
          "platform_administer",
        ])
      if (path === "/principals/1/tenants" || path === "/tenants")
        return json([
          { id: "11", name: "Archive tenant", status: "active" },
          { id: "12", name: "Other tenant", status: "active" },
        ])
      if (platform && rejectPlatform)
        return route.fulfill({
          status: 403,
          json: { code: 403, message: "forbidden", data: null },
        })
      const jobs = platform
        ? [{ ...makeJob(7000), tenant_id: null }]
        : tenant === "12"
          ? []
          : records
      if (path.endsWith("/archive-status"))
        return json({
          enabled,
          policy: { ...policy, tenant_id: platform ? null : tenant },
          total_events: 48,
          eligible_events: 46,
          archived_events:
            jobs.filter((j) => j.state === "complete").length * 2,
          pending_events: 2,
          queued_jobs: jobs.filter((j) => j.state === "queued").length,
          running_jobs: 0,
          failed_jobs: jobs.filter((j) => j.state === "failed").length,
          completed_jobs: jobs.filter((j) => j.state === "complete").length,
        })
      if (path.endsWith("/governance-policy"))
        return json({
          policy,
          integrity: {
            valid: true,
            verified_events: 48,
            head_sequence: 48,
            anchor: { enabled: false, signed: false, valid: true },
          },
          anchors: [],
          total_events: 48,
          archive_ready_events: 46,
          anchored_events: 0,
        })
      if (path.endsWith("/archives") && request.method() === "GET") {
        const offset = Number(url.searchParams.get("offset") ?? 0)
        return route.fulfill({
          json: {
            code: 0,
            message: "ok",
            data: jobs.slice(offset, offset + 20),
            meta: { page: { total: jobs.length } },
          },
        })
      }
      if (path.endsWith("/archives") && request.method() === "POST") {
        records = [makeJob(8000, "queued")]
        return json(records[0])
      }
      if (path.endsWith("/attempts")) {
        const job = records.find((j) => path.includes(`/${j.id}/`))
        job.state = "complete"
        job.object = object
        job.error_code = ""
        return json(job)
      }
      if (path.endsWith("/verifications")) {
        verifyStarted?.()
        if (blockVerify) await blockVerify
        return json(object)
      }
      if (path.endsWith("/content")) {
        if (corrupt)
          return route.fulfill({
            status: 409,
            json: { detail: "audit.archive_integrity" },
          })
        return route.fulfill({
          contentType: "application/x-ndjson",
          headers: {
            "Content-Disposition":
              'attachment; filename="audit-archive-1001.ndjson"',
          },
          body:
            JSON.stringify({
              type: "complete",
              exported_events: 2,
              content_sha256: "a".repeat(64),
            }) + "\n",
        })
      }
      if (path.endsWith("/retention-policy")) {
        Object.assign(policy, request.postDataJSON())
        return json(policy)
      }
      return json([])
    })
    await page.goto("/tenants/11/audit-governance")
    await page.getByText("共23 个任务", { exact: true }).waitFor()
    await page.getByText("平台默认", { exact: true }).waitFor()
    await fits(page)
    await page.screenshot({
      path: fileURLToPath(new URL(`${name}-jobs.png`, screenshots)),
      fullPage: true,
    })
    await page.getByRole("button", { name: "下一页归档", exact: true }).click()
    await page.getByText("1023", { exact: true }).waitFor()
    assert.equal(await page.getByText("1001", { exact: true }).count(), 0)
    await page.getByRole("button", { name: "上一页归档", exact: true }).click()
    await page
      .getByRole("button", { name: "重试归档 1002", exact: true })
      .click()
    await page
      .getByRole("button", { name: "下载归档 1002", exact: true })
      .waitFor()
    await page
      .getByRole("button", { name: "校验归档 1001", exact: true })
      .click()
    await page
      .getByRole("dialog")
      .getByText("版本、内容和保留锁校验通过", { exact: true })
      .waitFor()
    await fits(page)
    await page.screenshot({
      path: fileURLToPath(new URL(`${name}-verified.png`, screenshots)),
    })
    await page.keyboard.press("Escape")
    const downloaded = page.waitForEvent("download")
    await page
      .getByRole("button", { name: "下载归档 1001", exact: true })
      .click()
    assert.equal(
      (await downloaded).suggestedFilename(),
      "audit-archive-1001.ndjson"
    )
    corrupt = true
    await page
      .getByRole("button", { name: "下载归档 1001", exact: true })
      .click()
    await page.getByText("audit.archive_integrity", { exact: true }).waitFor()
    corrupt = false
    await page
      .getByRole("button", { name: "更新保留策略", exact: true })
      .click()
    await page.locator("#min-days").fill("30")
    await page.locator("#archive-days").fill("60")
    await fits(page)
    await page.getByRole("button", { name: "保存", exact: true }).click()
    await page.getByRole("dialog").waitFor({ state: "hidden" })
    assert.equal(policy.archive_after_days, 60)
    await page.getByRole("tab", { name: "平台", exact: true }).click()
    await page
      .getByRole("heading", { name: "平台审计治理", exact: true })
      .waitFor()
    await page.getByText("7000", { exact: true }).waitFor()
    assert.equal(await page.getByText("1001", { exact: true }).count(), 0)
    rejectPlatform = true
    await page.getByRole("button", { name: "刷新", exact: true }).click()
    await page.getByText("禁止", { exact: true }).waitFor()
    assert.equal(await page.getByText("7000", { exact: true }).count(), 0)
    rejectPlatform = false
    await page.getByRole("tab", { name: "当前租户", exact: true }).click()
    await page
      .getByRole("button", { name: "校验归档 1001", exact: true })
      .waitFor()
    let releaseVerify
    blockVerify = new Promise((resolve) => {
      releaseVerify = resolve
    })
    const started = new Promise((resolve) => {
      verifyStarted = resolve
    })
    await page
      .getByRole("button", { name: "校验归档 1001", exact: true })
      .click()
    await started
    await page.evaluate(() => {
      localStorage.setItem("iam.tenant", "12")
      window.dispatchEvent(new Event("tenant-change"))
    })
    await page.getByText("暂无归档任务", { exact: true }).waitFor()
    releaseVerify()
    await page.waitForResponse((response) =>
      response.url().endsWith("/1001/verifications")
    )
    await page.getByRole("heading", { name: "审计治理", exact: true }).waitFor()
    assert.equal(
      await page.getByRole("dialog").count(),
      0,
      "old tenant verification must not open a dialog"
    )
    records = []
    await page.evaluate(() => {
      localStorage.setItem("iam.tenant", "11")
      window.dispatchEvent(new Event("tenant-change"))
    })
    await page.getByText("暂无归档任务", { exact: true }).waitFor()
    await page
      .getByRole("button", { name: "创建归档任务", exact: true })
      .click()
    await page.getByText("8000", { exact: true }).waitFor()
    assert(
      await page
        .getByRole("button", { name: "创建归档任务", exact: true })
        .isDisabled()
    )
    enabled = false
    await page.getByRole("button", { name: "刷新", exact: true }).click()
    await page
      .getByRole("region", { name: "归档任务", exact: true })
      .getByText("未启用", { exact: true })
      .waitFor()
    assert(calls.some((call) => call.path.endsWith("/1002/attempts")))
    await fits(page)
    await context.close()
    console.log(`${name}: archive UI fixtures PASS`)
  }
  assert.deepEqual(errors, [])
} finally {
  await browser.close()
}
