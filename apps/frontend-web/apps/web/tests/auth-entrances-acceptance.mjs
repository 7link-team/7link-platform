import assert from "node:assert/strict"
import { join } from "node:path"

export async function authEntrancesAcceptance({
  admin,
  browser,
  webURL,
  directory,
  notifications,
  eventually,
  pass,
  adminLogin,
  adminPassword,
  captchaAnswer,
}) {
  const roots = {
    system: "/system",
    platform: "/platform",
    tenant: "/admin",
    user: "/user",
  }
  const contexts = []
  const pages = []
  const errors = []
  const password = "EntranceAcceptance-aA1!2026"
  const replacement = "EntranceRecovered-bB2!2026"
  const createBrowser = async () => {
    const context = await browser.newContext({
      baseURL: webURL,
      viewport: { width: 1440, height: 1000 },
    })
    contexts.push(context)
    const page = await context.newPage()
    pages.push(page)
    page.on("pageerror", (error) => errors.push(error.message))
    return { context, page }
  }
  const api = async (context, path, method = "GET", body, expected = 200) => {
    const response = await context.request.fetch(`/api${path}`, {
      method,
      headers: { Origin: webURL },
      ...(body ? { data: body } : {}),
    })
    assert.equal(response.status(), expected, `${method} ${path}`)
    return (await response.json()).data
  }
  const signIn = async (record, entry, email, secret) => {
    await record.page.goto(`${roots[entry]}/login`)
    await record.page.locator("#login-name").fill(email)
    await record.page.locator("#password").fill(secret)
    await record.page.locator('button[type="submit"]').click()
    await record.page.waitForURL((url) => !url.pathname.endsWith("/user/login"))
    await record.page.waitForLoadState("networkidle")
  }
  const register = async (entry, email) => {
    const record = await createBrowser()
    await record.page.goto(`${roots[entry]}/register`)
    await record.page.locator("#registration-email").fill(email)
    await record.page.locator("#registration-name").fill(`Entrance ${entry}`)
    await record.page.locator("#registration-password").fill(password)
    await record.page.locator("#registration-confirm").fill(password)
    await record.page.locator("#registration-captcha").fill("12345678")
    await record.page.locator('button[type="submit"]').click()
    await record.page.getByRole("status").waitFor()
    await eventually(
      () =>
        notifications.some(
          (message) =>
            message.type === "email_verification" && message.recipient === email
        ),
      "registration verification delivered"
    )
    const message = notifications.find(
      (message) =>
        message.type === "email_verification" && message.recipient === email
    )
    assert.equal(
      new URL(message.verification_url).searchParams.get("entry"),
      entry
    )
    assert.equal(
      new URL(message.verification_url).pathname,
      `${roots[entry]}/verify-email`
    )
    const denied = await record.context.request.post("/api/login", {
      data: {
        captcha_type: "text",
        captcha_id: "automated",
        captcha_answer: captchaAnswer,
        login_name: email,
        password,
        return_url: webURL + `${roots[entry]}/workspaces`,
      },
      headers: { Origin: webURL },
    })
    assert.equal(denied.status(), 401, "unverified registration cannot log in")
    await record.page.goto(message.verification_url)
    // The link proves the mailbox; activation also needs the registration password.
    await record.page.locator("#verification-password").fill(password)
    await record.page.locator('form button[type="submit"]').click()
    await record.page.getByRole("status").waitFor()
    await record.page.locator(`a[href="${roots[entry]}/login"]`).waitFor()
    assert.equal(
      await record.page
        .locator(`a[href="${roots[entry]}/login"]`)
        .getAttribute("href"),
      `${roots[entry]}/login`
    )
    assert(!record.page.url().includes("token="))
    await signIn(record, entry, email, password)
    return record
  }
  try {
    const anonymous = await createBrowser()
    for (const entry of ["system", "platform", "tenant", "user"]) {
      await anonymous.page.goto(`${roots[entry]}/login`)
      await anonymous.page.waitForLoadState("networkidle")
      assert.equal(
        new URL(anonymous.page.url()).pathname,
        `${roots[entry]}/login`
      )
      assert.equal(
        await anonymous.page.getByRole("navigation", { name: "Sign-in entrances" }).count(),
        0,
        "normal entrances must not expose demo switching"
      )
      assert.equal(await anonymous.page.getByRole("combobox").count(), 0)
      await anonymous.page
        .getByRole("heading", {
          name: `${entry[0].toUpperCase()}${entry.slice(1)} sign-in`,
          exact: true,
        })
        .waitFor()
      assert.equal(
        await anonymous.page.locator('a[href$="/register"]').count(),
        ["user", "tenant"].includes(entry) ? 1 : 0
      )
      assert.equal(
        await anonymous.page.locator('a[href$="/recover"]').count(),
        ["user", "tenant"].includes(entry) ? 1 : 0
      )
      for (const width of [1440, 390]) {
        await anonymous.page.setViewportSize({
          width,
          height: width === 390 ? 844 : 1000,
        })
        assert(
          await anonymous.page.evaluate(
            () => document.documentElement.scrollWidth <= innerWidth
          ),
          "entrance must not overflow horizontally"
        )
        await anonymous.page.screenshot({
          path: join(directory, `auth-entrance-${entry}-${width}.png`),
          fullPage: true,
        })
      }
    }
    await api(
      anonymous.context,
      "/tenant-registrations",
      "POST",
      { slug: "anonymous-tenant", name: "Anonymous" },
      401
    )
    pass(
      "four separate desktop/mobile login entrances; only tenant/user expose registration/recovery; anonymous tenant registration denied"
    )

    await anonymous.page.goto("/login/system?source=bookmark#retained")
    await anonymous.page.waitForURL((url) => url.pathname === "/system/login")
    assert.equal(new URL(anonymous.page.url()).search, "?source=bookmark")
    assert.equal(new URL(anonymous.page.url()).hash, "#retained")
    for (const [old, expected] of [
      ["/me/files", "/user/login"],
      ["/tenants/42/roles", "/admin/login"],
      ["/operating-platforms/42/roles", "/platform/login"],
    ]) {
      await anonymous.page.goto(old)
      await anonymous.page.waitForURL((url) => url.pathname === expected)
    }
    await anonymous.page.goto("/system/roles?source=deep-link")
    await anonymous.page.waitForURL((url) => url.pathname === "/system/login")
    await anonymous.page.locator("#login-name").fill(adminLogin)
    await anonymous.page.locator("#password").fill(adminPassword)
    await anonymous.page.locator('button[type="submit"]').click()
    await anonymous.page.waitForURL(
      (url) =>
        url.pathname === "/system/roles" && url.search === "?source=deep-link"
    )
    pass(
      "legacy bookmarks canonicalize with query/hash; protected routes use their own login entrance and restore the requested authorized page"
    )

    const system = await createBrowser()
    await signIn(system, "system", adminLogin, adminPassword)
    assert(
      new URL(system.page.url()).pathname.startsWith("/system/"),
      "system entrance opens the authorized system home"
    )
    await system.page.goto("/user/login")
    await system.page.waitForURL((url) => url.pathname === "/user/files")
    pass(
      "same authorized principal enters system management or personal file workspace according to the chosen entrance without changing its identity"
    )

    const tenantEmail = "entrance-tenant@example.test"
    const tenant = await register("tenant", tenantEmail)
    await tenant.page
      .getByRole("button", { name: "Register tenant", exact: true })
      .click()
    const createTenant = async (name, slug) => {
      await tenant.page.locator("#tenant-registration-name").fill(name)
      await tenant.page.locator("#tenant-registration-slug").fill(slug)
      const [response] = await Promise.all([
        tenant.page.waitForResponse(
          (response) =>
            new URL(response.url()).pathname === "/api/tenant-registrations" &&
            response.request().method() === "POST"
        ),
        tenant.page.locator('button[type="submit"]').click(),
      ])
      assert.equal(response.status(), 201)
      const created = (await response.json()).data
      await tenant.page.waitForURL(
        (url) =>
          url.pathname === `/admin/${created.id}` ||
          url.pathname.startsWith(`/admin/${created.id}/`)
      )
      await tenant.page.waitForLoadState("networkidle")
      assert.equal(await tenant.page.getByRole("alert").count(), 0)
      return created
    }
    const first = await createTenant(
      "Entrance Tenant One",
      "entrance-tenant-one"
    )
    const principal = await api(tenant.context, "/me")
    const members = await api(tenant.context, `/tenants/${first.id}/members`)
    assert(
      members.some(
        (member) =>
          member.subject === principal.subject && member.status === "active"
      ),
      "registrant owns initialized membership"
    )
    await api(
      tenant.context,
      "/tenant-registrations",
      "POST",
      { name: "Spoofed", slug: "spoofed-owner", owner_principal_id: "1" },
      422
    )
    await api(
      tenant.context,
      "/operating-platforms",
      "POST",
      {
        name: "Forbidden",
        slug: "forbidden-platform",
        owner_principal_id: principal.subject,
      },
      403
    )
    await tenant.page.goto("/admin/workspaces?create=1")
    const second = await createTenant(
      "Entrance Tenant Two",
      "entrance-tenant-two"
    )
    await tenant.page.goto("/admin")
    await tenant.page
      .getByRole("heading", { name: "Select workspace", exact: true })
      .waitFor()
    await tenant.page.screenshot({
      path: join(directory, "auth-tenant-workspaces.png"),
      fullPage: true,
    })
    await tenant.page
      .getByRole("link", { name: first.name, exact: true })
      .waitFor()
    await tenant.page
      .getByRole("link", { name: second.name, exact: true })
      .waitFor()
    assert.equal(new URL(tenant.page.url()).pathname, "/admin/workspaces")
    await tenant.page
      .getByRole("link", { name: second.name, exact: true })
      .click()
    await tenant.page.waitForURL(
      (url) =>
        url.pathname === `/admin/${second.id}` ||
        url.pathname.startsWith(`/admin/${second.id}/`)
    )
    pass(
      "real tenant signup, delivered email activation, caller-owned atomic bootstrap and multiple-tenant selection; owner spoofing and platform privilege escalation denied"
    )

    const userEmail = "entrance-user@example.test"
    const user = await register("user", userEmail)
    await user.page.waitForURL((url) => url.pathname === "/user/files")
    const userPrincipal = await api(user.context, "/me")
    assert.deepEqual(
      await api(user.context, `/principals/${userPrincipal.subject}/tenants`),
      []
    )
    await user.page.goto("/system/login")
    await user.page
      .getByText("This account has no access to this entrance.", {
        exact: true,
      })
      .waitFor()
    await api(user.context, "/system/roles", "GET", undefined, 403)
    await user.page.goto("/platform")
    await user.page
      .getByText("This account has no access to this entrance.", {
        exact: true,
      })
      .waitFor()
    const platform = await api(
      admin.context,
      "/operating-platforms",
      "POST",
      {
        name: "Entrance Platform",
        slug: "entrance-platform",
        owner_principal_id: userPrincipal.subject,
      },
      201
    )
    await user.page.goto("/platform/login")
    await user.page.waitForURL(
      (url) =>
        url.pathname === `/platform/${platform.id}` ||
        url.pathname.startsWith(`/platform/${platform.id}/`)
    )
    assert.equal(
      (await api(user.context, "/me")).subject,
      userPrincipal.subject
    )
    const secondPlatform = await api(
      admin.context,
      "/operating-platforms",
      "POST",
      {
        name: "Entrance Platform Two",
        slug: "entrance-platform-two",
        owner_principal_id: userPrincipal.subject,
      },
      201
    )
    await user.page.goto("/platform/login")
    await user.page
      .getByRole("link", { name: platform.name, exact: true })
      .waitFor()
    await user.page
      .getByRole("link", { name: secondPlatform.name, exact: true })
      .waitFor()
    assert.equal(new URL(user.page.url()).pathname, "/platform/workspaces")
    await user.page.screenshot({
      path: join(directory, "auth-platform-workspaces.png"),
      fullPage: true,
    })
    await user.page
      .getByRole("link", { name: secondPlatform.name, exact: true })
      .click()
    await user.page.waitForURL((url) =>
      url.pathname.startsWith(`/platform/${secondPlatform.id}`)
    )
    const view = await api(
      admin.context,
      "/account-views",
      "POST",
      {
        principal_id: principal.subject,
        scope_type: "tenant",
        scope_id: first.id,
        mode: "read_write",
      },
      201
    )
    try {
      const refused = await admin.context.request.post(
        "/api/tenant-registrations",
        {
          headers: { Origin: webURL, Authorization: `Bearer ${view.token}` },
          data: { name: "View tenant", slug: "view-tenant" },
        }
      )
      assert.equal(
        refused.status(),
        403,
        "writable access views cannot register tenants"
      )
    } finally {
      await api(admin.context, `/account-views/${view.id}`, "DELETE")
    }
    pass(
      "real user signup creates no tenant or administrative identity; system/platform entrances deny unassigned users, then an explicitly assigned platform opens for the same principal"
    )

    for (const [record, entry, email] of [
      [tenant, "tenant", tenantEmail],
      [user, "user", userEmail],
    ]) {
      await record.page.goto(`${roots[entry]}/recover`)
      await record.page.locator('[name="identifier"]').fill(email)
      await record.page.locator('button[type="submit"]').click()
      await eventually(
        () =>
          notifications.some(
            (message) =>
              message.type === "password_recovery" &&
              message.recipient === email
          ),
        "recovery delivered"
      )
      const message = notifications.findLast(
        (message) =>
          message.type === "password_recovery" && message.recipient === email
      )
      assert.equal(
        new URL(message.recovery_url).searchParams.get("entry"),
        entry
      )
      assert.equal(
        new URL(message.recovery_url).pathname,
        `${roots[entry]}/recover`
      )
      await record.page.goto(message.recovery_url)
      await record.page.locator('[name="new_password"]').fill(replacement)
      await record.page.locator('[name="confirm_password"]').fill(replacement)
      await record.page.locator('button[type="submit"]').click()
      await record.page
        .getByText(
          "Password updated. All existing sessions have been signed out.",
          { exact: true }
        )
        .waitFor()
      await api(record.context, "/me", "GET", undefined, 401)
      assert.equal(
        await record.page
          .locator(`a[href="${roots[entry]}/login"]`)
          .getAttribute("href"),
        `${roots[entry]}/login`
      )
      await signIn(record, entry, email, replacement)
    }
    pass(
      "tenant/user recovery uses actual independently delivered single-use credentials, preserves entrance and revokes prior sessions; fresh password login succeeds"
    )
    assert.deepEqual(errors, [])
  } catch (error) {
    for (const [index, page] of pages.entries())
      await page
        .screenshot({
          path: join(directory, `auth-entrance-failure-${index}.png`),
          fullPage: true,
        })
        .catch(() => {})
    throw error
  } finally {
    for (const context of contexts) await context.close()
  }
}
