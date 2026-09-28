import assert from "node:assert/strict"
import { createHash } from "node:crypto"
import { mkdirSync, readFileSync } from "node:fs"
import { fileURLToPath } from "node:url"
import { chromium } from "playwright"
import { auditPassword } from "../scripts/audit-password.mjs"

const baseURL = process.env.IAM_CONSOLE_URL
const control = process.env.IAM_ARCHIVE_FIXTURE_CONTROL
const disabledMode = process.env.IAM_GOVERNANCE_DISABLED === "true"
assert(
  baseURL && (control || disabledMode),
  "run through the composed governance browser harness"
)
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
const scopePath = (path, tenant) =>
  tenant ? `/tenants/${tenant}${path}` : path
async function login(context, name) {
  const response = await context.request.post("/api/login", {
    headers: headers(),
    data: { login_name: name, password, return_url: baseURL + "/" },
  })
  assert.equal(response.status(), 201, "fixture browser login")
  return (await response.json()).data
}
async function get(context, path, tenant) {
  const response = await context.request.get("/api" + scopePath(path, tenant), {
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
async function disabledAcceptance() {
  for (const [name, viewport, tenant, account] of [
    ["desktop", { width: 1440, height: 1000 }, "91001", "admin-a"],
    ["mobile", { width: 390, height: 844 }, "91002", "admin-b"],
  ]) {
    const context = await browser.newContext({
      baseURL,
      viewport,
      reducedMotion: "reduce",
    })
    await context.addCookies([
      { name: "NEXT_LOCALE", value: "zh-CN", url: baseURL },
    ])
    await login(context, account)
    const identity = await get(context, "/me")
    const page = await context.newPage()
    page.on("pageerror", (error) => failures.push(error.message))
    await page.goto(`/admin/${tenant}/audit-governance`)
    await page.getByText("系统默认", { exact: true }).waitFor()
    assert.equal(await page.locator("[data-audit-anchor]").isDisabled(), true)
    assert.equal(await page.locator("[data-audit-sign]").isDisabled(), true)
    assert.equal(
      await page
        .getByRole("button", { name: "创建归档任务", exact: true })
        .isDisabled(),
      true
    )
    const governance = await get(context, "/audit/governance-policy", tenant)
    assert.equal(governance.integrity.anchor.enabled, false)
    assert.equal(governance.integrity.anchor.signing_enabled, false)
    await capture(page, `${name}-disabled-capabilities`)

    const roleResponse = await context.request.post(
      `/api/tenants/${tenant}/roles`,
      {
        headers: headers(),
        data: { name: "Governance acceptance role" },
      }
    )
    assert.equal(roleResponse.status(), 201)
    const role = (await roleResponse.json()).data
    const fallbackResponse = await context.request.post(
      `/api/tenants/${tenant}/roles`,
      {
        headers: headers(),
        data: { name: "Permanent tenant administrator" },
      }
    )
    assert.equal(fallbackResponse.status(), 201)
    const fallback = (await fallbackResponse.json()).data
    assert.equal(
      (
        await context.request.put(
          `/api/roles/${fallback.id}/permissions/tenant_administer`,
          { headers: headers() }
        )
      ).status(),
      200
    )
    assert.equal(
      (
        await context.request.put(
          `/api/roles/${fallback.id}/members/${identity.subject}`,
          { headers: headers() }
        )
      ).status(),
      200
    )
    const grant = await context.request.put(
      `/api/roles/${role.id}/permissions/tenant_administer`,
      { headers: headers() }
    )
    assert.equal(grant.status(), 200)
    const membership = await context.request.put(
      `/api/roles/${role.id}/members/${identity.subject}`,
      { headers: headers() }
    )
    assert.equal(membership.status(), 200)
    await page.goto(`/admin/${tenant}/roles?role=${role.id}`)
    await page
      .getByRole("heading", { name: "租户角色与权限", exact: true })
      .waitFor()
    await page
      .locator('[data-edit-permission-condition="tenant_administer"]')
      .click()
    await page
      .locator("[data-permission-condition-effect]")
      .getByText("无附加限制。仅填写时区不会限制访问。", { exact: true })
      .waitFor()
    assert.equal(await page.locator("#permission-timezone").isDisabled(), true)
    await page.locator("#permission-minimum-acr").click()
    await page
      .getByRole("option", { name: "加强认证（等级 2）", exact: true })
      .click()
    await page.locator("#permission-client-id").fill("acceptance-client")
    await page
      .locator("[data-permission-condition-effect]")
      .getByText("当前认证等级至少为 2", { exact: true })
      .waitFor()
    await capture(page, `${name}-role-condition-effect`)
    const saved = page.waitForResponse(
      (response) =>
        response.request().method() === "PUT" &&
        new URL(response.url()).pathname.endsWith("/condition")
    )
    await page.locator("[data-save-permission-condition]").click()
    assert.equal((await saved).status(), 200)
    await page.getByRole("dialog").waitFor({ state: "hidden" })
    const grants = await get(context, `/roles/${role.id}/permission-grants`)
    assert.deepEqual(
      grants.find((item) => item.permission_code === "tenant_administer")
        .condition.all,
      [
        { gte: [{ context: "auth.acr" }, { value: 2 }] },
        { eq: [{ context: "client.id" }, { value: "acceptance-client" }] },
      ]
    )
    await page.reload()
    await page
      .locator('[data-edit-permission-condition="tenant_administer"]')
      .click()
    assert.equal(
      await page.locator("#permission-client-id").inputValue(),
      "acceptance-client"
    )
    await page.keyboard.press("Escape")
    const permissions = await get(
      context,
      `/principals/${identity.subject}/permissions`
    )
    assert(
      permissions.includes("platform_administer"),
      "tenant condition cannot alter platform administrator authority"
    )
    const tenantPermissions = await get(
      context,
      `/principals/${identity.subject}/permissions`,
      tenant
    )
    assert(
      tenantPermissions.includes("tenant_administer"),
      "platform administrator authority remains effective in an active tenant"
    )
    assert(
      !(await page
        .locator('[data-permission-code="platform_administer"]')
        .count()),
      "platform authority is not editable in tenant roles"
    )
    if (name === "mobile") {
      const member = await browser.newContext({
        baseURL,
        viewport,
        reducedMotion: "reduce",
      })
      await member.addCookies([
        { name: "NEXT_LOCALE", value: "zh-CN", url: baseURL },
      ])
      await login(member, "requester-b")
      const memberIdentity = await get(member, "/me")
      assert.equal(
        (
          await context.request.put(
            `/api/roles/${role.id}/permissions/audit_event_view`,
            { headers: headers() }
          )
        ).status(),
        200
      )
      assert.equal(
        (
          await context.request.put(
            `/api/roles/${role.id}/members/${memberIdentity.subject}`,
            { headers: headers() }
          )
        ).status(),
        200
      )
      const memberPermissions = await get(
        member,
        `/principals/${memberIdentity.subject}/permissions`,
        tenant
      )
      assert(memberPermissions.includes("audit_event_view"))
      assert(
        !memberPermissions.includes("tenant_administer"),
        "an ordinary member must satisfy the conditioned grant"
      )
      const denied = await member.request.patch(`/api/roles/${role.id}`, {
        headers: headers(),
        data: { name: "Must be denied" },
      })
      assert.equal(denied.status(), 403)
      const memberPage = await member.newPage()
      memberPage.on("pageerror", (error) => failures.push(error.message))
      await memberPage.goto(`/admin/${tenant}/audit-governance`)
      await memberPage.getByText("系统默认", { exact: true }).waitFor()
      assert.equal(
        await memberPage
          .getByRole("tab", { name: "平台", exact: true })
          .count(),
        0
      )
      assert.equal(
        await memberPage
          .locator("[data-audit-anchor], [data-audit-sign]")
          .count(),
        0
      )
      await capture(memberPage, "mobile-tenant-auditor-no-platform-authority")
      await member.close()
    }
    console.log(
      `${name}: disabled governance and tenant-role condition persistence/platform isolation PASS`
    )
    await context.close()
  }
  assert.deepEqual(failures, [])
}
try {
  if (disabledMode) {
    await disabledAcceptance()
  } else {
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
      await context.addCookies([
        { name: "NEXT_LOCALE", value: "zh-CN", url: baseURL },
      ])
      await login(context, account)
      await context.addInitScript(
        (value) => sessionStorage.setItem("iam.tenant", value),
        tenant
      )
      const page = await context.newPage()
      page.on("pageerror", (error) => failures.push(error.message))
      page.on("request", (request) => {
        if (new URL(request.url()).pathname.startsWith("/api/system/audit")) {
          assert(!new URL(request.url()).pathname.includes("/tenants/"))
        }
      })
      page.on("response", (response) => {
        // A principal without an uploaded avatar uses the shell's initials fallback.
        if (
          response.status() === 404 &&
          /\/api\/principals\/[^/]+\/avatar$/.test(
            new URL(response.url()).pathname
          )
        )
          return
        if (response.url().includes("/api/") && response.status() >= 400)
          failures.push(
            `${response.status()} ${new URL(response.url()).pathname}`
          )
      })
      await page.goto(`/admin/${tenant}/audit-governance`)
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
            new URL(response.url()).pathname ===
              `/api/tenants/${tenant}/audit/archives`
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
      const anchorButton = page.locator("[data-audit-anchor]")
      const signButton = page.locator("[data-audit-sign]")
      await anchorButton.waitFor()
      assert.equal(await anchorButton.isEnabled(), true)
      assert.equal(await signButton.isEnabled(), true)
      const anchored = page.waitForResponse(
        (response) =>
          response.request().method() === "POST" &&
          new URL(response.url()).pathname ===
            `/api/tenants/${tenant}/audit/anchors`
      )
      await anchorButton.click()
      assert.equal((await anchored).status(), 200)
      await page.getByText("已签名", { exact: true }).first().waitFor()
      const signed = page.waitForResponse(
        (response) =>
          response.request().method() === "POST" &&
          new URL(response.url()).pathname ===
            `/api/tenants/${tenant}/audit/root-signatures`
      )
      await signButton.click()
      assert.equal((await signed).status(), 200)
      const governance = await get(context, "/audit/governance-policy", tenant)
      assert.equal(governance.integrity.anchor.signing_enabled, true)
      assert.equal(governance.integrity.anchor.signature_valid, true)
      assert(governance.anchors.length > 0)
      await capture(page, name + "-signed-root")
      await page.getByRole("tab", { name: "平台", exact: true }).click()
      await page
        .getByRole("heading", { name: "平台审计治理", exact: true })
        .waitFor()
      jobs = await get(context, "/system/audit/archives")
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
        .getByRole("button", {
          name: `下载归档 ${platformJob.id}`,
          exact: true,
        })
        .waitFor()
      await inspect(
        page,
        context,
        "/system/audit",
        undefined,
        platformJob,
        name + "-platform"
      )
      if (name === "mobile") {
        const foreign = await context.request.get(
          `/api/tenants/${tenant}/audit/archives/${tenantArchive}/content`,
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
      ["/system/audit/archive-status", undefined],
    ]) {
      const denied = await member.request.get(
        "/api" + scopePath(path, tenant),
        {
          headers: headers(tenant),
        }
      )
      assert.equal(
        denied.status(),
        403,
        "ordinary membership is not archive administration"
      )
    }
    await member.close()
    assert.deepEqual(failures, [])
  }
  console.log("screenshots: " + fileURLToPath(screenshots))
} finally {
  await browser.close()
}
