import assert from "node:assert/strict"
import { DatabaseSync } from "node:sqlite"
import { join } from "node:path"
import { writeFile } from "node:fs/promises"

export async function administrationAccountViewAcceptance({
  admin,
  browser,
  tenant,
  webURL,
  directory,
  login,
  eventually,
  pass,
  notifications,
}) {
  const db = new DatabaseSync(join(directory, "lifecycle.sqlite"), {
    readOnly: true,
  })
  db.exec("PRAGMA busy_timeout = 10000")
  const contexts = []
  const issued = []
  const failedResponses = []
  const recordResponse = (response) => {
    if (response.status() >= 400 && response.url().includes("/api/"))
      failedResponses.push({
        path: new URL(response.url()).pathname,
        status: response.status(),
      })
  }
  admin.page.on("response", recordResponse)
  const statement = (sql) => {
    const prepared = db.prepare(sql)
    prepared.setReadBigInts(true)
    return prepared
  }
  const request = async (
    path,
    method = "GET",
    data,
    status,
    context = admin.context
  ) => {
    const response = await context.request.fetch(`/api${path}`, {
      method,
      headers: { Origin: webURL },
      ...(data === undefined ? {} : { data }),
    })
    assert.equal(
      response.status(),
      status ?? (method === "POST" ? response.status() : 200),
      `${method} ${path}`
    )
    if (status && status >= 400) return
    assert(response.ok(), `${method} ${path}: HTTP ${response.status()}`)
    return (await response.json()).data
  }
  const viewRequest = async (
    view,
    path,
    method = "GET",
    body,
    status = 200
  ) => {
    const result = await admin.page.evaluate(
      async ({ token, path, method, body }) => {
        const response = await fetch(`/api${path}`, {
          method,
          credentials: "omit",
          headers: {
            Authorization: `Bearer ${token}`,
            ...(body === undefined
              ? {}
              : { "Content-Type": "application/json" }),
          },
          ...(body === undefined ? {} : { body: JSON.stringify(body) }),
        })
        return {
          status: response.status,
          body: await response.json().catch(() => ({})),
        }
      },
      { token: view.token, path, method, body }
    )
    assert.equal(
      result.status,
      status,
      `${method} ${path}: account view admission`
    )
    return result.body.data
  }
  const open = async (
    target,
    scope_type,
    scope_id,
    mode = "read_only",
    expires_at
  ) => {
    const view = await request("/account-views", "POST", {
      principal_id: target.id,
      scope_type,
      scope_id,
      mode,
      ...(expires_at ? { expires_at } : {}),
    })
    assert.equal(view.mode, mode)
    issued.push(view)
    return view
  }
  const rows = (table, id) =>
    statement(
      `SELECT * FROM e2e_${table} WHERE principal_id = ? ORDER BY principal_id`
    ).all(id)
  const createAccount = async (name) =>
    request("/system/principals", "POST", {
      login_name: `${name}@example.test`,
      display_name: name,
      email: `${name}@example.test`,
      password: "ViewAcceptance-aA1!2026",
    })
  try {
    const operator = await request("/me")
    const systemTarget = await createAccount("view-system")
    const ownerA = await createAccount("view-platform-a")
    const ownerB = await createAccount("view-platform-b")
    const ordinary = await createAccount("view-ordinary")
    await request(`/system/administrators/${systemTarget.id}`, "PUT", {
      full_administrator: true,
    })
    const platformA = await request("/operating-platforms", "POST", {
      name: "Platform A",
      slug: "view-platform-a",
      owner_principal_id: ownerA.id,
    })
    const platformB = await request("/operating-platforms", "POST", {
      name: "Platform B",
      slug: "view-platform-b",
      owner_principal_id: ownerB.id,
    })
    const tenantB = await request("/tenants", "POST", {
      name: "View Tenant",
      slug: "view-tenant",
      owner_principal_id: ownerA.id,
    })
    const targetLogin = await login(
      ownerA.login_name,
      "ViewAcceptance-aA1!2026"
    )
    contexts.push(targetLogin.context)
    const entity = await request(
      `/tenants/${tenantB.id}/entities`,
      "POST",
      { name: "View Entity", type: "unit", status: "active" },
      undefined,
      targetLogin.context
    )
    await request(
      `/operating-platforms/${platformA.id}/members`,
      "POST",
      {
        subject: ordinary.id,
        display_name: ordinary.display_name,
        email: ordinary.email,
      },
      undefined,
      targetLogin.context
    )
    await request(
      `/operating-platforms/${platformA.id}/members`,
      "POST",
      { subject: ordinary.id, display_name: "MUST NOT REPLACE" },
      409,
      targetLogin.context
    )
    const originalMember = await request(
      `/operating-platforms/${platformA.id}/members/${ordinary.id}`,
      "GET",
      undefined,
      undefined,
      targetLogin.context
    )
    assert.equal(originalMember.display_name, ordinary.display_name)
    pass(
      "independent platforms and tenant roots; member creation cannot overwrite an existing profile"
    )

    const ordinaryLogin = await login(
      ordinary.login_name,
      "ViewAcceptance-aA1!2026"
    )
    contexts.push(ordinaryLogin.context)
    await targetLogin.page.goto("about:blank")
    await ordinaryLogin.page.goto("about:blank")
    await targetLogin.page.waitForLoadState("networkidle")
    await ordinaryLogin.page.waitForLoadState("networkidle")
    const baseline = {
      sessions: rows("iam_sessions", ownerA.id),
      credentials: rows("iam_credentials", ownerA.id),
      tokens: rows("iam_personal_tokens", ownerA.id),
      notifications: notifications.length,
    }
    const read = await open(ownerA, "platform", platformA.id)
    assert.equal((await viewRequest(read, "/me")).subject, ownerA.id)
    const localCodes = await viewRequest(
      read,
      `/operating-platforms/${platformA.id}/permissions`
    )
    assert(localCodes.includes("operating_platform_administer"))
    assert(!localCodes.includes("platform_administer"))
    assert(!localCodes.includes("tenant_administer"))
    assert(!localCodes.includes("system_account_view"))
    const menus = await viewRequest(
      read,
      `/operating-platforms/${platformA.id}/navigation`
    )
    assert(menus.length > 0)
    await viewRequest(
      read,
      `/operating-platforms/${platformB.id}/roles`,
      "GET",
      undefined,
      403
    )
    await viewRequest(
      read,
      `/tenants/${tenantB.id}/roles`,
      "GET",
      undefined,
      403
    )
    await viewRequest(read, "/system/principals", "GET", undefined, 403)
    for (const [method, path, body] of [
      [
        "POST",
        `/operating-platforms/${platformA.id}/roles`,
        { name: "Rejected" },
      ],
      [
        "PATCH",
        `/operating-platforms/${platformA.id}/members/${ordinary.id}`,
        { display_name: "Rejected" },
      ],
      ["DELETE", `/operating-platforms/${platformA.id}/members/${ordinary.id}`],
    ])
      await viewRequest(read, path, method, body, 403)
    pass(
      "read-only platform view uses target permissions and rejects writes, sibling platform, tenant and system access"
    )

    const writable = await open(ownerA, "platform", platformA.id, "read_write")
    assert.notEqual(read.id, writable.id)
    const role = await viewRequest(
      writable,
      `/operating-platforms/${platformA.id}/roles`,
      "POST",
      { name: "Writable view role" }
    )
    await viewRequest(writable, `/roles/${role.id}`, "PATCH", {
      name: "Writable role updated",
    })
    const event = statement(
      "SELECT principal_id, detail FROM e2e_iam_audit_events WHERE event_type = 'role_created' AND target_id = ?"
    ).get(role.id)
    assert(event, "write must have persisted transactional audit")
    assert.equal(String(event.principal_id), operator.subject)
    const detail = JSON.parse(event.detail)
    assert.equal(detail.account_view_id, writable.id)
    assert.equal(detail.viewed_principal_id, ownerA.id)
    assert.equal(detail.account_view_read_only, false)
    for (const path of [
      "/login",
      `/principals/${ownerA.id}/mfa/totp-enrollments`,
      "/account-views",
    ]) {
      await viewRequest(writable, path, "POST", {}, 403)
    }
    await viewRequest(writable, "/sessions/current", "DELETE", undefined, 403)
    pass(
      "explicit writable view creates a separate record; real write audit retains operator, target and view; security conversions denied"
    )

    const ordinaryView = await open(
      ordinary,
      "platform",
      platformA.id,
      "read_write"
    )
    assert.deepEqual(
      await viewRequest(
        ordinaryView,
        `/operating-platforms/${platformA.id}/permissions`
      ),
      []
    )
    await viewRequest(
      ordinaryView,
      `/operating-platforms/${platformA.id}/roles`,
      "POST",
      { name: "Unauthorized" },
      403
    )
    const tenantView = await open(ownerA, "tenant", tenantB.id)
    await viewRequest(tenantView, `/tenants/${tenantB.id}/roles`)
    await viewRequest(
      tenantView,
      `/tenants/${tenant.id}/roles`,
      "GET",
      undefined,
      403
    )
    await viewRequest(
      tenantView,
      `/operating-platforms/${platformA.id}/roles`,
      "GET",
      undefined,
      403
    )
    const entityView = await open(ownerA, "entity", entity.id)
    await viewRequest(
      entityView,
      `/tenants/${tenantB.id}/roles`,
      "GET",
      undefined,
      403
    )
    const systemView = await open(systemTarget, "system", "1")
    assert(
      (await viewRequest(systemView, "/system/permissions")).includes(
        "system_account_view"
      )
    )
    await viewRequest(
      systemView,
      `/tenants/${tenantB.id}/roles`,
      "GET",
      undefined,
      403
    )
    const personal = await open(ownerA, "personal", ownerA.id)
    const personalSessions = await viewRequest(
      personal,
      `/principals/${ownerA.id}/sessions`
    )
    assert(personalSessions.every((row) => !row.current))
    await viewRequest(
      personal,
      `/principals/${ownerB.id}/sessions`,
      "GET",
      undefined,
      404
    )
    await viewRequest(
      personal,
      `/operating-platforms/${platformA.id}/roles`,
      "GET",
      undefined,
      403
    )
    pass(
      "system, tenant, entity, ordinary member and personal scopes retain exact boundaries without operator permission expansion"
    )

    assert.deepEqual(rows("iam_sessions", ownerA.id), baseline.sessions)
    assert.deepEqual(rows("iam_credentials", ownerA.id), baseline.credentials)
    assert.deepEqual(rows("iam_personal_tokens", ownerA.id), baseline.tokens)
    assert.equal(notifications.length, baseline.notifications)
    assert(
      !db
        .prepare(
          "SELECT token_hash FROM e2e_iam_account_view_tokens WHERE id = ?"
        )
        .get(read.id)
        .token_hash.includes(read.token)
    )
    await request(`/account-views/${read.id}`, "DELETE")
    await viewRequest(read, "/me", "GET", undefined, 401)
    const expires = await open(
      ownerA,
      "personal",
      ownerA.id,
      "read_only",
      new Date(Date.now() + 3000).toISOString()
    )
    await eventually(
      async () => {
        const result = await admin.page.evaluate(
          async (token) =>
            (
              await fetch("/api/me", {
                credentials: "omit",
                headers: { Authorization: `Bearer ${token}` },
              })
            ).status,
          expires.token
        )
        return result === 401
      },
      "view expiration",
      10_000
    )
    pass(
      "target sessions, credentials, personal-token catalog and notifications unchanged; hashed credentials, revocation and expiry enforced"
    )

    const openUI = async (target, workspace, writable = false) => {
      await admin.page.setViewportSize({ width: 1440, height: 1000 })
      await admin.page.goto("/system/accounts")
      await admin.page
        .getByRole("button", {
          name: `Account view ${target.display_name}`,
          exact: true,
        })
        .click()
      await admin.page.getByRole("dialog").waitFor()
      await admin.page.locator("#account-view-scope").click()
      await admin.page.getByRole("option", { name: workspace }).click()
      if (writable) {
        await admin.page.locator("#account-view-mode").click()
        await admin.page
          .getByRole("option", { name: "Writable", exact: true })
          .click()
      }
      await admin.page
        .getByRole("button", { name: "Open view", exact: true })
        .click()
      await admin.page
        .getByRole("status")
        .filter({ hasText: writable ? "Writable" : "Read-only" })
        .waitFor()
      await eventually(
        async () =>
          admin.page
            .evaluate(async () => {
              const { iamApi } = await import("/src/lib/iam-api.ts")
              const session = await iamApi.session()
              return session.subject
            })
            .then(
              (subject) => subject === target.id,
              () => false
            ),
        "target subject verified"
      )
    }
    const captureUI = async (name) => {
      await admin.page.waitForLoadState("networkidle")
      for (const width of [1440, 390]) {
        await admin.page.setViewportSize({
          width,
          height: width === 390 ? 844 : 1000,
        })
        await admin.page
          .getByRole("button", { name: "Exit view", exact: true })
          .waitFor()
        assert(
          await admin.page.evaluate(
            () => document.documentElement.scrollWidth <= innerWidth
          ),
          `${name} responsive overflow`
        )
        await admin.page.screenshot({
          path: join(directory, `account-view-${name}-${width}.png`),
          fullPage: true,
          animations: "disabled",
        })
        if (width === 390) {
          await admin.page
            .getByRole("button", {
              name:
                name === "personal"
                  ? "Open account navigation"
                  : "Open main navigation",
              exact: true,
            })
            .click()
          await admin.page.getByRole("dialog").waitFor()
          await admin.page.screenshot({
            path: join(
              directory,
              `account-view-${name}-${width}-navigation.png`
            ),
            fullPage: true,
            animations: "disabled",
          })
          await admin.page.keyboard.press("Escape")
        }
      }
      const storage = await admin.page.evaluate(() =>
        JSON.stringify({
          local: { ...localStorage },
          session: { ...sessionStorage },
          url: location.href,
        })
      )
      assert(
        !storage.includes("avt_"),
        "view secret must not be persisted or placed in a URL"
      )
    }
    const exitUI = async () => {
      await admin.page
        .getByRole("button", { name: "Exit view", exact: true })
        .click()
      await admin.page.waitForURL((url) => url.pathname === "/system/accounts")
      await admin.page
        .getByRole("heading", { name: "Global accounts", exact: true })
        .waitFor()
      await admin.page.waitForLoadState("networkidle")
      assert.equal((await request("/me")).subject, operator.subject)
    }
    for (const [name, target, workspace, path, heading] of [
      [
        "system",
        systemTarget,
        /System administration/,
        "/system/accounts",
        "Global accounts",
      ],
      [
        "platform",
        ownerA,
        /Operating platform.*Platform A/,
        `/operating-platforms/${platformA.id}/members`,
        "Platform members",
      ],
      [
        "tenant",
        ownerA,
        /Tenant.*View Tenant/,
        `/tenants/${tenantB.id}`,
        "Identity control plane",
      ],
      [
        "entity",
        ownerA,
        /Entity.*View Entity/,
        `/tenants/${tenantB.id}/entities/${entity.id}/overview`,
        "Entity overview",
      ],
      [
        "personal",
        ownerA,
        /Personal account/,
        "/me/profile",
        "Basic information",
      ],
    ]) {
      await openUI(target, workspace)
      await admin.page.waitForURL((url) => url.pathname === path)
      await admin.page
        .getByRole("heading", { name: heading, exact: true })
        .waitFor()
      if (name === "entity")
        await admin.page
          .getByRole("link", { name: "Entity overview", exact: true })
          .waitFor()
      if (name === "tenant") {
        await admin.page
          .getByRole("link", { name: "Roles and permissions", exact: true })
          .waitFor()
        assert.equal(
          await admin.page.getByText("My sessions", { exact: true }).count(),
          0
        )
      }
      if (name === "personal") {
        await admin.page.getByText(ownerA.login_name, { exact: true }).first().waitFor()
        await admin.page
          .getByRole("button", { name: "Restore random avatar", exact: true })
          .waitFor()
        await eventually(
          async () =>
            admin.page
              .getByRole("alert")
              .count()
              .then((count) => count === 0),
          "personal profile has no failed avatar read"
        )
        assert(
          await admin.page
            .getByRole("button", { name: "Restore random avatar", exact: true })
            .isDisabled(),
          "read-only personal controls disabled"
        )
      }
      if (name !== "personal")
        assert.equal(await admin.page.getByTitle("Account").count(), 0)
      if (name === "platform") {
        await admin.page
          .getByRole("navigation")
          .getByText("Organization", { exact: true })
          .waitFor()
        await admin.page
          .getByRole("navigation")
          .getByText("Access", { exact: true })
          .waitFor()
        assert.equal(
          await admin.page
            .getByRole("button", { name: "Create role", exact: true })
            .count(),
          0
        )
      }
      await captureUI(name)
      await exitUI()
      pass(
        `real desktop/mobile ${name} view resolves target UI, exact workspace, secret isolation and restores operator`
      )
    }
    await openUI(ordinary, /Operating platform.*Platform A/)
    await admin.page
      .getByText("You have no available permissions in this workspace.", {
        exact: true,
      })
      .waitFor()
    await captureUI("ordinary")
    await exitUI()
    await openUI(ownerA, /Operating platform.*Platform A/, true)
    await admin.page
      .getByRole("link", { name: "Roles and permissions", exact: true })
      .click()
    await admin.page.getByRole("heading", { name: "Platform roles and permissions", exact: true }).waitFor()
    await admin.page
      .getByRole("button", { name: "Create role", exact: true })
      .click()
    await admin.page.locator("#role-name").fill("Browser writable view role")
    await admin.page.locator('button[type="submit"][form="role-form"]').click()
    await admin.page
      .getByRole("heading", { name: "Browser writable view role", exact: true })
      .waitFor()
    await captureUI("writable")
    await exitUI()
    const browserEvent = statement(
      "SELECT principal_id, detail FROM e2e_iam_audit_events WHERE event_type = 'role_created' ORDER BY id DESC LIMIT 1"
    ).get()
    assert.equal(String(browserEvent.principal_id), operator.subject)
    assert.equal(JSON.parse(browserEvent.detail).viewed_principal_id, ownerA.id)
    assert.deepEqual(rows("iam_sessions", ownerA.id), baseline.sessions)
    assert.deepEqual(rows("iam_credentials", ownerA.id), baseline.credentials)
    assert.deepEqual(rows("iam_personal_tokens", ownerA.id), baseline.tokens)
    pass(
      "real writable platform UI mutation succeeds and restores operator after exit; ordinary member view shows no authorized pages"
    )
  } catch (cause) {
    await admin.page
      .screenshot({
        path: join(directory, "account-view-failure.png"),
        fullPage: true,
        animations: "disabled",
      })
      .catch(() => {})
    await writeFile(
      join(directory, "account-view-failure.json"),
      JSON.stringify(
        { url: admin.page.url(), failedResponses, message: cause.message },
        null,
        2
      )
    )
    throw cause
  } finally {
    admin.page.off("response", recordResponse)
    for (const view of issued)
      await request(`/account-views/${view.id}`, "DELETE").catch(() => {})
    for (const context of contexts) await context.close()
    db.close()
  }
}
