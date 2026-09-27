import assert from "node:assert/strict"
import { join } from "node:path"

export async function accountViewTabsAcceptance({
  admin,
  operator,
  systemTarget,
  ownerA,
  platformA,
  tenantB,
  request,
  eventually,
  pass,
  issued,
  viewPages,
  directory,
}) {
  const operatorPage = admin.page
  assert.equal((await request("/me")).subject, operator.subject)
  const inspect = (page) =>
    page.evaluate(async () => {
      const { getAccountView, accountViewRequest } =
        await import("/src/lib/account-view.ts")
      const { iamApi } = await import("/src/lib/iam-api.ts")
      return {
        view: getAccountView(),
        identity: accountViewRequest("GET"),
        subject: (await iamApi.session()).subject,
        storage: JSON.stringify({
          local: { ...localStorage },
          session: { ...sessionStorage },
          url: location.href,
        }),
      }
    })
  const raw = (page, path, method = "GET", body) =>
    page.evaluate(
      async ({ path, method, body }) => {
        const { accountViewRequest } = await import("/src/lib/account-view.ts")
        const identity = accountViewRequest("GET")
        const response = await fetch(`/api${path}`, {
          ...identity,
          method,
          headers: {
            ...identity.headers,
            ...(body === undefined
              ? {}
              : { "Content-Type": "application/json" }),
          },
          ...(body === undefined ? {} : { body: JSON.stringify(body) }),
        })
        return {
          status: response.status,
          body: await response.json().catch(() => null),
        }
      },
      { path, method, body }
    )
  const launch = async (category, target, scope, owner, writable = false) => {
    await operatorPage.goto(`/system/accounts/${category}`)
    if (owner) {
      await operatorPage.locator("#account-owner").click()
      await operatorPage
        .getByRole("option", { name: owner.name, exact: true })
        .click()
    }
    await operatorPage
      .getByRole("button", {
        name: `Account view ${target.display_name}`,
        exact: true,
      })
      .click()
    await eventually(async () => {
      const selector = operatorPage.locator("#account-view-scope")
      return (
        !(await selector.isDisabled()) &&
        (await selector.innerText()).includes(
          owner?.name ?? "System administration"
        )
      )
    }, `${category} default workspace`)
    if (writable) {
      await operatorPage.locator("#account-view-mode").click()
      await operatorPage
        .getByRole("option", { name: "Writable", exact: true })
        .click()
    }
    const operatorURL = operatorPage.url()
    const popupPromise = operatorPage.waitForEvent("popup")
    await operatorPage
      .getByRole("button", { name: "Open view", exact: true })
      .click()
    const page = await popupPromise
    viewPages.add(page)
    await page.getByRole("button", { name: "Exit view", exact: true }).waitFor()
    const state = await inspect(page)
    assert.equal(state.subject, target.id)
    assert.equal(state.view.scope_type, scope)
    assert.equal(state.view.scope_id, scope === "system" ? "1" : owner.id)
    assert.equal(state.view.mode, writable ? "read_write" : "read_only")
    assert.equal(state.identity.credentials, "omit")
    const token = state.identity.headers.Authorization.slice("Bearer ".length)
    assert(token.startsWith("avt_"))
    assert(!state.storage.includes(token))
    assert(!state.storage.includes("avt_"))
    assert.equal(operatorPage.url(), operatorURL)
    assert.equal(
      await operatorPage
        .getByRole("button", { name: "Exit view", exact: true })
        .count(),
      0
    )
    issued.push({ id: state.view.id, token })
    return { page, id: state.view.id, token }
  }
  const close = async (view) => {
    const closed = view.page.waitForEvent("close")
    await view.page
      .getByRole("button", { name: "Exit view", exact: true })
      .click()
    await closed
    viewPages.delete(view.page)
  }
  const system = await launch("system", systemTarget, "system")
  const platform = await launch("platform", ownerA, "platform", platformA, true)
  assert.notEqual(system.id, platform.id)
  assert.notEqual(system.token, platform.token)
  for (const [name, page] of [
    ["operator", operatorPage],
    ["system-readonly", system.page],
    ["platform-writable", platform.page],
  ]) {
    await page.setViewportSize({ width: 1440, height: 1000 })
    await page.waitForLoadState("networkidle")
    await page.screenshot({
      path: join(directory, `account-view-tabs-${name}.png`),
      fullPage: true,
      animations: "disabled",
    })
  }
  assert.equal((await inspect(system.page)).subject, systemTarget.id)
  assert.equal(
    (
      await raw(system.page, "/system/roles", "POST", {
        name: "Forbidden concurrent view role",
      })
    ).status,
    403
  )
  assert.equal((await raw(platform.page, "/system/members")).status, 403)
  const created = await raw(
    platform.page,
    `/operating-platforms/${platformA.id}/roles`,
    "POST",
    {
      name: "Concurrent writable view role",
    }
  )
  assert.equal(created.status, 200)
  assert.equal(
    (await raw(platform.page, `/roles/${created.body.data.id}`, "DELETE"))
      .status,
    200
  )
  await close(system)
  assert.equal((await inspect(platform.page)).subject, ownerA.id)
  const revoked = await launch("system", systemTarget, "system")
  await request(`/account-views/${revoked.id}`, "DELETE")
  assert.equal((await raw(revoked.page, "/me")).status, 401)
  assert.equal((await inspect(platform.page)).subject, ownerA.id)
  await revoked.page.close()
  viewPages.delete(revoked.page)
  const tenant = await launch("tenant", ownerA, "tenant", tenantB)
  assert.notEqual(tenant.id, platform.id)
  assert.equal((await inspect(platform.page)).view.scope_type, "platform")
  await close(tenant)
  pass(
    "independent account-view tabs default to named admitted scopes; credentials, readonly/write modes, exit and revocation remain isolated"
  )

  const assertClosed = async (page) => {
    await page
      .getByRole("heading", { name: "Account view closed", exact: true })
      .waitFor()
    await eventually(
      async () =>
        page.evaluate(async () => {
          const { iamApi } = await import("/src/lib/iam-api.ts")
          try {
            await iamApi.session()
            return false
          } catch {
            return true
          }
        }),
      "view without in-memory authorization fails closed"
    )
    assert.equal(
      await page
        .getByRole("button", { name: "Exit view", exact: true })
        .count(),
      0
    )
    assert.equal(await page.getByTitle("Account", { exact: true }).count(), 0)
    const storage = await page.evaluate(() =>
      JSON.stringify({
        local: { ...localStorage },
        session: { ...sessionStorage },
        url: location.href,
      })
    )
    assert(!storage.includes("avt_"))
  }
  const duplicatePromise = platform.page.waitForEvent("popup")
  await platform.page.evaluate(() => window.open(location.href, "_blank"))
  const duplicate = await duplicatePromise
  viewPages.add(duplicate)
  const fallbackResponses = []
  const recordFallback = (response) => {
    if (
      response.url().includes("/api/") &&
      response.ok() &&
      !response.request().headers().authorization?.startsWith("Bearer avt_")
    )
      fallbackResponses.push(response.url())
  }
  duplicate.on("response", recordFallback)
  await duplicate.waitForLoadState("domcontentloaded")
  await assertClosed(duplicate)
  assert.equal((await inspect(platform.page)).subject, ownerA.id)
  const refreshed = await launch("tenant", ownerA, "tenant", tenantB)
  refreshed.page.on("response", recordFallback)
  await refreshed.page.reload()
  await assertClosed(refreshed.page)
  assert.equal((await inspect(platform.page)).subject, ownerA.id)
  const copiedHandoff = await admin.context.newPage()
  viewPages.add(copiedHandoff)
  copiedHandoff.on("response", recordFallback)
  await copiedHandoff.goto("/account-view/open")
  await assertClosed(copiedHandoff)
  platform.page.on("response", recordFallback)
  await platform.page.addInitScript(() => {
    Storage.prototype.getItem = () => {
      throw new DOMException("Storage unavailable", "SecurityError")
    }
  })
  await platform.page.reload()
  await assertClosed(platform.page)
  await platform.page.setViewportSize({ width: 390, height: 844 })
  await platform.page.screenshot({
    path: join(directory, "account-view-tabs-storage-unavailable-390.png"),
    fullPage: true,
    animations: "disabled",
  })
  assert.equal((await request("/me")).subject, operator.subject)
  assert.equal(
    await operatorPage.evaluate(async () => {
      const { iamApi } = await import("/src/lib/iam-api.ts")
      return (await iamApi.session()).subject
    }),
    operator.subject
  )
  assert.deepEqual(
    fallbackResponses,
    [],
    "unauthorized view tabs never use operator cookies for successful API requests"
  )
  for (const page of [
    duplicate,
    refreshed.page,
    copiedHandoff,
    platform.page,
  ]) {
    await page.close()
    viewPages.delete(page)
  }
  pass(
    "refresh with unavailable storage and duplicate account-view tabs lose authorization without falling back to operator cookies or persisting tokens"
  )
}
