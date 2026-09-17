import assert from "node:assert/strict"
import { randomBytes } from "node:crypto"
import { mkdirSync } from "node:fs"
import { createServer } from "node:http"
import { fileURLToPath } from "node:url"
import { chromium } from "playwright"
import { auditPassword } from "./audit-password.mjs"

const baseURL = (
  process.env.IAM_CONSOLE_URL ?? "http://localhost:8091"
).replace(/\/$/, "")
const screenshots = new URL(
  "../../../../../.local/screenshots/scim-targets/",
  import.meta.url
)
mkdirSync(screenshots, { recursive: true })
const received = []
let rejectRemoteStatus = 0
const receiver = createServer(async (request, response) => {
  const chunks = []
  for await (const chunk of request) chunks.push(chunk)
  const raw = Buffer.concat(chunks).toString()
  const body = raw ? JSON.parse(raw) : undefined
  received.push({
    method: request.method,
    path: request.url,
    authorization: request.headers.authorization,
    body,
  })
  response.writeHead(rejectRemoteStatus || 200, {
    "Content-Type": "application/scim+json",
  })
  response.end(
    JSON.stringify(
      rejectRemoteStatus
        ? { detail: "Acceptance receiver unavailable" }
        : { id: `remote-${body?.externalId ?? "deleted"}` }
    )
  )
})
await new Promise((resolve, reject) => {
  receiver.once("error", reject)
  receiver.listen(0, "127.0.0.1", resolve)
})
let browser
const exceptions = []
async function api(context, path, method = "GET", body, tenant = "") {
  const response = await context.request.fetch("/api" + path, {
    method,
    data: body,
    headers: { Origin: baseURL, ...(tenant ? { "X-Tenant-Id": tenant } : {}) },
  })
  const result = await response.json()
  return { status: response.status(), data: result.data }
}
async function ok(...args) {
  const result = await api(...args)
  assert(
    result.status >= 200 && result.status < 300,
    `${args[2] ?? "GET"} ${args[1]}: ${result.status}`
  )
  return result.data
}
try {
  browser = await chromium.launch({
    channel: process.env.IAM_BROWSER_CHANNEL ?? "chrome",
    headless: true,
  })
  const admin = await browser.newContext({ baseURL })
  await ok(admin, "/authn/sessions", "POST", {
    login_name: process.env.IAM_BROWSER_LOGIN ?? "admin@example.test",
    password: auditPassword(),
    return_url: baseURL + "/",
  })
  const me = await ok(admin, "/authn/sessions/current")
  const stamp = Date.now()
  const tenant = await ok(admin, "/iam/tenants", "POST", {
    slug: `outbound-audit-${stamp}`,
    name: "Outbound acceptance",
    owner_principal_id: me.subject,
  })
  const other = await ok(admin, "/iam/tenants", "POST", {
    slug: `outbound-other-${stamp}`,
    name: "Outbound isolation",
    owner_principal_id: me.subject,
  })
  const local = (path, method, body) => ok(admin, path, method, body, tenant.id)
  const actorPassword = randomBytes(32).toString("base64url")
  const actor = await local("/iam/principals", "POST", {
    login_name: `outbound-operator-${stamp}`,
    password: actorPassword,
    display_name: "Outbound operator",
  })
  const person = await local("/iam/principals", "POST", {
    login_name: `outbound-person-${stamp}`,
    password: actorPassword,
    display_name: "Outbound person",
    email: `private-${stamp}@example.test`,
  })
  const roles = await local("/iam/roles")
  const adminRole = roles.find((role) => role.name === "Administrator")
  assert(adminRole)
  await local(`/iam/roles/${adminRole.id}/members/${actor.id}`, "PUT", {})
  const first = await local("/iam/service-accounts", "POST", {
    login_name: `outbound-first-${stamp}`,
    display_name: "First automation",
  })
  const second = await local("/iam/service-accounts", "POST", {
    login_name: `outbound-second-${stamp}`,
    display_name: "Second automation",
  })
  const restricted = await local("/iam/roles", "POST", {
    name: "Restricted automation",
  })
  await local(`/iam/roles/${restricted.id}/members/${first.id}`, "PUT", {})
  const group = await local("/iam/groups", "POST", {
    name: "Outbound group",
    type: "static",
  })
  await local(`/iam/groups/${group.id}/members/${person.id}`, "PUT", {})
  const context = await browser.newContext({
    baseURL,
    viewport: { width: 1440, height: 1000 },
    reducedMotion: "reduce",
  })
  await ok(context, "/authn/sessions", "POST", {
    login_name: actor.login_name,
    password: actorPassword,
    return_url: baseURL + "/",
  })
  const actorSession = await ok(context, "/authn/sessions/current")
  await context.addCookies([
    { name: "NEXT_LOCALE", value: "zh-CN", url: baseURL },
  ])
  await context.addInitScript(
    ({ tenant, subject }) => {
      if (localStorage.getItem("iam.tenant:subject") === subject) return
      localStorage.setItem("iam.tenant", tenant)
      localStorage.setItem("iam.tenant:subject", subject)
    },
    { tenant: tenant.id, subject: actorSession.subject }
  )
  const page = await context.newPage()
  page.on("pageerror", (error) => exceptions.push(error.message))
  await page.goto("/iam/scim-directories?view=targets")
  await page.getByRole("button", { name: "创建目标", exact: true }).click()
  let name = `SCIM outbound ${stamp}`
  const token = randomBytes(24).toString("base64url")
  const rotated = randomBytes(24).toString("base64url")
  const targetURL = `http://127.0.0.1:${receiver.address().port}/scim/v2`
  await page.locator("#scim-target-name").fill(name)
  await page.locator("#scim-target-url").fill(targetURL)
  await page.locator("#scim-target-token").fill(token)
  await page.locator("#scim-target-account").fill(first.id)
  const dialog = page.getByRole("dialog")
  async function capture(filename) {
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
      "page overflow"
    )
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
    await page.screenshot({
      path: fileURLToPath(new URL(filename + ".png", screenshots)),
      fullPage: true,
      animations: "disabled",
    })
  }
  async function save() {
    await dialog.getByRole("button", { name: "保存", exact: true }).click()
    await dialog.waitFor({ state: "hidden" })
    await page
      .getByRole("button", { name: `编辑 ${name}`, exact: true })
      .waitFor()
  }
  async function edit() {
    await page
      .getByRole("button", { name: `编辑 ${name}`, exact: true })
      .click()
    await dialog.waitFor()
    assert.equal(
      await page.locator("#scim-target-token").inputValue(),
      "",
      "stored tokens never populate the editor"
    )
  }
  await capture("desktop-create")
  await save()
  let target = (await local("/iam/scim/targets")).find(
    (item) => item.name === name
  )
  assert.equal(target.service_account_id, first.id)
  assert(!JSON.stringify(target).includes(token))
  const getTarget = async () =>
    (await local("/iam/scim/targets")).find((item) => item.id === target.id)
  const pushPath = `/iam/scim/targets/${target.id}/push`
  async function operate(type, id, remove = false, expected = 200) {
    await page
      .getByRole("button", {
        name: remove ? `从 ${name} 移除资源` : `推送到 ${name}`,
        exact: true,
      })
      .click()
    await dialog.waitFor()
    if (type === "Group") {
      await page.locator("#scim-resource-type").click()
      await page.getByRole("option", { name: "用户组", exact: true }).click()
    }
    await page.locator("#scim-resource-id").fill(id)
    const responsePromise = page.waitForResponse(
      (response) =>
        response
          .url()
          .endsWith(
            `/iam/scim/targets/${target.id}/${remove ? "deprovision" : "push"}`
          ) && response.request().method() === "POST"
    )
    await dialog
      .getByRole("button", { name: remove ? "确认移除" : "推送", exact: true })
      .click()
    const response = await responsePromise
    assert.equal(response.status(), expected)
    if (expected === 200) await dialog.waitFor({ state: "hidden" })
    else {
      await dialog.getByRole("alert").waitFor()
      await dialog.getByRole("button", { name: "取消", exact: true }).click()
    }
  }
  await operate("User", person.id)
  assert.equal(received.length, 1)
  assert.equal(received[0].path, `/scim/v2/Users/${person.id}`)
  assert.equal(received[0].body.userName, person.login_name)
  assert.equal(received[0].body.emails[0].value, person.email)
  assert(
    received[0].authorization === "Bearer " + token,
    "initial credential used"
  )
  await edit()
  name += " renamed"
  await page.locator("#scim-target-name").fill(name)
  await save()
  assert.equal((await getTarget()).service_account_id, first.id)
  await operate("User", person.id)
  assert.equal(received.at(-1).path, `/scim/v2/Users/remote-${person.id}`)
  assert(
    received.at(-1).authorization === "Bearer " + token,
    "ordinary edit preserves credential"
  )
  await operate("Group", group.id)
  assert.deepEqual(received.at(-1).body.members, [
    { value: `remote-${person.id}` },
  ])
  await edit()
  await page.locator("#scim-target-account").fill(second.id)
  await page.locator("#scim-target-token").fill(rotated)
  await save()
  assert.equal((await getTarget()).service_account_id, second.id)
  await operate("User", person.id)
  assert(
    received.at(-1).authorization === "Bearer " + rotated,
    "rotated credential used"
  )
  // The bound automation actor, not the human administrator, controls projection.
  const rule = {
    read: { default: "allow", allow_roles: [], deny_roles: [restricted.id] },
    write: { default: "allow", allow_roles: [], deny_roles: [] },
    mask: "omit",
  }
  await local("/iam/field-policies/user_profile/email", "PUT", {
    version: 0,
    policy: rule,
  })
  await edit()
  await page.locator("#scim-target-account").fill(first.id)
  await save()
  await operate("User", person.id)
  assert(
    !Object.hasOwn(received.at(-1).body, "emails"),
    "bound actor cannot export hidden email"
  )
  const loginPolicy = await local(
    "/iam/field-policies/user_profile/login_name",
    "PUT",
    { version: 0, policy: rule }
  )
  let calls = received.length
  await operate("User", person.id, false, 403)
  assert.equal(
    received.length,
    calls,
    "denied identifier sends no remote request"
  )
  await local(
    `/iam/field-policies/user_profile/login_name?version=${loginPolicy.version}`,
    "DELETE"
  )
  // A definite rejection releases the claim and permits a deliberate retry.
  rejectRemoteStatus = 429
  await operate("User", person.id, false, 502)
  assert.equal(
    received.length,
    calls + 1,
    "remote failure reaches receiver exactly once"
  )
  rejectRemoteStatus = 0
  await operate("User", person.id)
  await operate("Group", group.id, true)
  assert.equal(received.at(-1).method, "DELETE")
  assert.equal(received.at(-1).path, `/scim/v2/Groups/remote-${group.id}`)
  await operate("User", person.id, true)
  assert.equal(received.at(-1).path, `/scim/v2/Users/remote-${person.id}`)
  calls = received.length
  await operate("User", person.id, true, 404)
  assert.equal(received.length, calls, "removed mapping sends no second DELETE")
  await edit()
  target = await getTarget()
  target = await local(`/iam/scim/targets/${target.id}`, "PUT", {
    name,
    base_url: targetURL,
    service_account_id: second.id,
    status: "disabled",
    version: target.version,
  })
  await page.locator("#scim-target-name").fill(name + " stale")
  await dialog.getByRole("button", { name: "保存", exact: true }).click()
  await dialog.getByRole("button", { name: "重新加载", exact: true }).waitFor()
  assert(await page.locator("#scim-target-name").isDisabled())
  await dialog.getByRole("button", { name: "重新加载", exact: true }).click()
  await page.waitForFunction(
    (id) => document.querySelector("#scim-target-account")?.value === id,
    second.id
  )
  assert.equal(await page.locator("#scim-target-name").inputValue(), name)
  await page.setViewportSize({ width: 390, height: 844 })
  await capture("mobile-editor")
  await dialog.getByRole("button", { name: "取消", exact: true }).click()
  assert(
    await page
      .getByRole("button", { name: `推送到 ${name}`, exact: true })
      .isDisabled()
  )
  assert.equal(
    (
      await api(
        context,
        pushPath,
        "POST",
        { resource_type: "User", resource_id: person.id },
        tenant.id
      )
    ).status,
    409
  )
  assert.equal(received.length, calls)
  await edit()
  await dialog.getByRole("switch").click()
  await page.locator("#scim-target-account").fill("")
  await save()
  assert.equal((await getTarget()).service_account_id, undefined)
  assert(
    await page
      .getByRole("button", { name: `推送到 ${name}`, exact: true })
      .isDisabled()
  )
  assert.equal(
    (
      await api(
        context,
        pushPath,
        "POST",
        { resource_type: "User", resource_id: person.id },
        tenant.id
      )
    ).status,
    409
  )
  assert.equal(
    received.length,
    calls,
    "unbound and disabled targets send no request"
  )
  assert.equal(
    (
      await api(
        admin,
        pushPath,
        "POST",
        { resource_type: "User", resource_id: person.id },
        other.id
      )
    ).status,
    404
  )
  assert.equal(received.length, calls, "cross-tenant lookup sends no request")
  await capture("mobile-list")
  await edit()
  await page.evaluate((id) => {
    localStorage.setItem("iam.tenant", id)
    window.dispatchEvent(new Event("tenant-change"))
  }, other.id)
  await dialog.waitFor({ state: "hidden" })
  assert.equal(
    await page
      .getByRole("button", { name: `编辑 ${name}`, exact: true })
      .count(),
    0
  )
  await page.evaluate((id) => {
    localStorage.setItem("iam.tenant", id)
    window.dispatchEvent(new Event("tenant-change"))
  }, tenant.id)
  await page
    .getByRole("button", { name: `编辑 ${name}`, exact: true })
    .waitFor()
  await page.setViewportSize({ width: 1440, height: 1000 })
  await capture("desktop-list")
  await page.getByRole("button", { name: `删除 ${name}`, exact: true }).click()
  await dialog.getByRole("button", { name: "仅删除配置", exact: true }).click()
  await dialog.waitFor({ state: "hidden" })
  assert.equal(await getTarget(), undefined)
  assert.equal(
    received.length,
    calls,
    "deleting local configuration leaves remote resources untouched"
  )

  // A 5xx cannot prove the write was rejected; never resend or erase its claim.
  name = `SCIM uncertain ${stamp}`
  await page.getByRole("button", { name: "创建目标", exact: true }).click()
  await page.locator("#scim-target-name").fill(name)
  await page.locator("#scim-target-url").fill(targetURL)
  await page.locator("#scim-target-token").fill(rotated)
  await page.locator("#scim-target-account").fill(second.id)
  await save()
  target = (await local("/iam/scim/targets")).find((item) => item.name === name)
  rejectRemoteStatus = 503
  await operate("User", person.id, false, 502)
  assert.equal(received.length, calls + 1)
  rejectRemoteStatus = 0
  await operate("User", person.id, false, 409)
  assert.equal(received.length, calls + 1, "uncertain writes are not resent")
  assert.equal(
    (
      await api(
        context,
        `/iam/scim/targets/${target.id}`,
        "DELETE",
        undefined,
        tenant.id
      )
    ).status,
    409,
    "uncertain write claim cannot be deleted"
  )
  assert.equal(received.length, calls + 1)
  assert.deepEqual(exceptions, [])
  console.log(
    "PASS: outbound UI, ordinary tenant admin, binding/token lifecycle, User/Group wire payloads, projection, denials, definite rejection/retry, uncertain write protection, conflict reload, deletion, tenant isolation, desktop/mobile"
  )
} finally {
  await browser?.close()
  receiver.closeAllConnections()
  await new Promise((resolve) => receiver.close(resolve))
}
