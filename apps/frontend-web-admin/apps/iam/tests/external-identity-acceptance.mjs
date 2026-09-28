import assert from "node:assert/strict"
import { createHash, generateKeyPairSync, randomBytes, sign } from "node:crypto"
import { createServer } from "node:http"
import { join } from "node:path"

// These upstreams issue real signed protocol messages; IAM responses are never mocked.
async function oidcFixture(apiURL) {
  const keys = generateKeyPairSync("rsa", { modulusLength: 2048 })
  const jwk = {
    ...keys.publicKey.export({ format: "jwk" }),
    kid: "fixture",
    alg: "RS256",
    use: "sig",
  }
  const codes = new Map()
  const state = { issuer: "", subject: "", email: "", exchanged: 0, errors: 0 }
  const encode = (value) =>
    Buffer.from(JSON.stringify(value)).toString("base64url")
  const server = createServer(async (request, response) => {
    const json = (body, status = 200) =>
      response
        .writeHead(status, { "Content-Type": "application/json" })
        .end(JSON.stringify(body))
    try {
      const url = new URL(request.url, state.issuer)
      if (url.pathname === "/.well-known/openid-configuration")
        return json({
          issuer: state.issuer,
          authorization_endpoint: state.issuer + "/authorize",
          token_endpoint: state.issuer + "/token",
          jwks_uri: state.issuer + "/jwks",
          response_types_supported: ["code"],
          subject_types_supported: ["public"],
          id_token_signing_alg_values_supported: ["RS256"],
        })
      if (url.pathname === "/jwks") return json({ keys: [jwk] })
      if (url.pathname === "/authorize") {
        const callback = new URL(url.searchParams.get("redirect_uri"))
        assert.equal(callback.origin, apiURL)
        assert.equal(url.searchParams.get("client_id"), "remediation-fixture")
        assert.equal(url.searchParams.get("code_challenge_method"), "S256")
        const code = randomBytes(32).toString("base64url")
        codes.set(code, {
          query: url.searchParams,
          subject: state.subject,
          email: state.email,
          expires: Date.now() + 60000,
        })
        callback.searchParams.set("code", code)
        callback.searchParams.set("state", url.searchParams.get("state"))
        response.writeHead(303, { Location: callback.href }).end()
        return
      }
      if (url.pathname === "/token") {
        let body = ""
        for await (const chunk of request) {
          body += chunk
          assert(body.length < 8192)
        }
        const form = new URLSearchParams(body)
        const item = codes.get(form.get("code"))
        codes.delete(form.get("code"))
        assert(item && item.expires > Date.now())
        assert.equal(form.get("redirect_uri"), item.query.get("redirect_uri"))
        assert.equal(
          createHash("sha256")
            .update(form.get("code_verifier"))
            .digest("base64url"),
          item.query.get("code_challenge")
        )
        const now = Math.floor(Date.now() / 1000)
        const data =
          encode({ alg: "RS256", kid: "fixture" }) +
          "." +
          encode({
            iss: state.issuer,
            aud: "remediation-fixture",
            sub: item.subject,
            email: item.email,
            email_verified: true,
            name: item.subject,
            iat: now,
            exp: now + 120,
            auth_time: now,
            nonce: item.query.get("nonce") ?? "",
          })
        state.exchanged++
        return json({
          id_token:
            data +
            "." +
            sign("RSA-SHA256", Buffer.from(data), keys.privateKey).toString(
              "base64url"
            ),
          token_type: "Bearer",
        })
      }
      json({ error: "not_found" }, 404)
    } catch {
      state.errors++
      json({ error: "invalid_request" }, 400)
    }
  })
  await new Promise((resolve) => server.listen(0, "127.0.0.1", resolve))
  state.issuer = `http://127.0.0.1:${server.address().port}`
  return { state, close: () => new Promise((resolve) => server.close(resolve)) }
}

export async function externalIdentityAcceptance({
  browser,
  admin,
  apiURL,
  webURL,
  tenant,
  call,
  backend,
  directory,
  run,
  launch,
  port,
  eventually,
  pass,
  notifications,
}) {
  const fixture = await oidcFixture(apiURL)
  const contexts = []
  const stamp = `external-${Date.now()}`
  const current = await call(admin.context, "/me", "GET", undefined, {
    tenant: false,
  })
  const providerInput = {
    name: stamp + "-OIDC",
    provider_type: "oidc",
    issuer: fixture.state.issuer,
    client_id: "remediation-fixture",
    client_secret: "fixture-secret",
    auto_provision: true,
    status: "active",
  }
  const provider = await call(
    admin.context,
    "/identity-providers",
    "POST",
    providerInput
  )
  const principalList = () => call(admin.context, "/principals?limit=200")
  const count = async () => (await principalList()).length
  const login = async (providerID, width = 1440, success = true) => {
    const context = await browser.newContext({
      baseURL: webURL,
      viewport: { width, height: 950 },
    })
    contexts.push(context)
    const page = await context.newPage()
    await context.addCookies([
      { name: "NEXT_LOCALE", value: "en-US", url: webURL },
    ])
    await page.goto(
      `${apiURL}/identity-providers/${providerID}/authorization?${new URLSearchParams({ return_url: webURL + "/user/identities" })}`
    )
    if (success) {
      await page
        .waitForURL((url) => url.origin === webURL)
        .catch(async () => {
          throw new Error(
            `Federation browser failed: ${await page.locator("body").innerText()}`
          )
        })
      const response = await context.request.get("/api/me")
      assert.equal(response.status(), 200)
      await page
        .getByRole("heading", { name: "Linked identities", exact: true })
        .waitFor()
      await page
        .getByRole("heading", {
          name: providerID === provider.id ? provider.name : stamp + "-SAML",
          exact: true,
        })
        .waitFor()
      assert(
        await page.evaluate(
          () => document.documentElement.scrollWidth <= window.innerWidth
        ),
        "linked identities must fit the viewport"
      )
      await page.screenshot({
        path: join(directory, `external-${providerID}-${width}.png`),
      })
      return { context, page, me: (await response.json()).data }
    }
    const response = await context.request.get("/api/me")
    assert.equal(
      response.status(),
      401,
      "email collision must not create a session"
    )
    await page.waitForURL(
      (url) => url.origin === webURL && url.pathname === "/user/login"
    )
    await page
      .getByText(
        "Sign in independently or recover your account before linking this external identity.",
        { exact: true }
      )
      .waitFor()
    await page.locator('a[href="/user/recover"]').waitFor()
    return { context, page }
  }
  try {
    const before = await count()
    fixture.state.subject = stamp + "-new"
    fixture.state.email = stamp + "@example.test"
    const first = await login(provider.id)
    const second = await login(provider.id, 390)
    assert.equal(first.me.subject, second.me.subject)
    assert.equal(await count(), before + 1)
    fixture.state.subject = stamp + "-attacker"
    fixture.state.email = current.email || "admin@example.test"
    await login(provider.id, 1440, false)
    assert.equal(await count(), before + 1)
    pass(
      "signed OIDC desktop/mobile login reuses only its trusted subject; existing email collision creates no account or session"
    )

    await call(admin.context, `/identity-providers/${provider.id}`, "PUT", {
      ...providerInput,
      client_id: "replacement-client",
    })
    assert.equal(
      (await first.context.request.get("/api/me")).status(),
      401,
      "source replacement revokes old sessions"
    )
    pass(
      "OIDC security-source replacement invalidates its established account session"
    )

    const samlPort = await port()
    const samlURL = `http://127.0.0.1:${samlPort}`
    const samlBinary = join(
      directory,
      process.platform === "win32" ? "saml-fixture.exe" : "saml-fixture"
    )
    await run(
      "go",
      ["build", "-o", samlBinary, "./scripts/saml-fixture"],
      backend,
      "saml-fixture-build"
    )
    const child = launch(
      samlBinary,
      ["--port", String(samlPort)],
      backend,
      "saml-fixture"
    )
    await eventually(async () => {
      assert.equal(child.exitCode, null)
      return fetch(samlURL + "/metadata")
        .then((r) => r.ok)
        .catch(() => false)
    }, "signed SAML upstream")
    const samlProvider = await call(
      admin.context,
      "/identity-providers",
      "POST",
      {
        name: stamp + "-SAML",
        provider_type: "saml",
        issuer: samlURL + "/metadata",
        client_id: "remediation-saml",
        auto_provision: true,
        status: "active",
      }
    )
    const configure = async (subject, email) => {
      const result = await fetch(samlURL + "/configure", {
        method: "POST",
        headers: { "Content-Type": "application/json" },
        body: JSON.stringify({
          subject,
          email,
          entity_id: "remediation-saml",
          callback: `${apiURL}/identity-providers/${samlProvider.id}/callback`,
        }),
      })
      assert(result.ok)
    }
    await configure(stamp + "-saml", stamp + "-saml@example.test")
    const samlFirst = await login(samlProvider.id)
    const samlSecond = await login(samlProvider.id, 390)
    assert.equal(samlFirst.me.subject, samlSecond.me.subject)
    const afterSAML = await count()
    await configure(
      stamp + "-saml-attacker",
      current.email || "admin@example.test"
    )
    await login(samlProvider.id, 390, false)
    assert.equal(await count(), afterSAML)
    pass(
      "real signed upstream SAML browser desktop/mobile positive login and existing-email attack denial"
    )
    const recovery = await browser.newContext({
      baseURL: webURL,
      viewport: { width: 390, height: 950 },
    })
    contexts.push(recovery)
    await recovery.addCookies([
      { name: "NEXT_LOCALE", value: "en-US", url: webURL },
    ])
    const recoveryPage = await recovery.newPage()
    await recoveryPage.goto("/user/recover")
    await recoveryPage
      .locator("#recovery-identifier")
      .fill(stamp + "-saml@example.test")
    await recoveryPage
      .getByRole("button", { name: "Send recovery email", exact: true })
      .click()
    await eventually(
      () =>
        notifications.some(
          (item) =>
            item.type === "password_recovery" &&
            item.recipient === stamp + "-saml@example.test"
        ),
      "independent first-credential recovery delivery"
    )
    const delivery = notifications.find(
      (item) =>
        item.type === "password_recovery" &&
        item.recipient === stamp + "-saml@example.test"
    )
    const recoveryToken = new URL(delivery.recovery_url).searchParams.get(
      "token"
    )
    assert(recoveryToken)
    const firstPassword = randomBytes(24).toString("base64url") + "aA1!"
    await recoveryPage.goto(delivery.recovery_url)
    await recoveryPage.locator("#recovery-password").fill(firstPassword)
    await recoveryPage.locator("#recovery-confirm").fill(firstPassword)
    await recoveryPage
      .getByRole("button", { name: "Update password", exact: true })
      .click()
    await recoveryPage
      .getByText(
        "Password updated. All existing sessions have been signed out.",
        { exact: true }
      )
      .waitFor()
    assert.equal((await samlFirst.context.request.get("/api/me")).status(), 401)
    await recoveryPage.goto("/user/login")
    await recoveryPage.locator("#login-name").fill(stamp + "-saml@example.test")
    await recoveryPage.locator("#password").fill(firstPassword)
    await recoveryPage.locator('form button[type="submit"]').click()
    await recoveryPage.waitForURL((url) => url.pathname !== "/user/login")
    assert.equal((await call(recovery, "/me")).subject, samlFirst.me.subject)
    await call(
      recovery,
      "/password-recoveries",
      "POST",
      {
        token: recoveryToken,
        new_password: randomBytes(24).toString("base64url") + "aA1!",
      },
      { tenant: false, status: 400 }
    )
    pass(
      "passwordless SAML account creates its first credential through independently delivered browser recovery; old sessions and replay denied"
    )
    assert.equal(fixture.state.errors, 0)
    assert(fixture.state.exchanged >= 3)
    await scimAcceptance({ browser, admin, apiURL, webURL, call, stamp, pass })
  } finally {
    for (const context of contexts) await context.close()
    await fixture.close()
  }
}

async function scimAcceptance({
  browser,
  admin,
  apiURL,
  webURL,
  call,
  stamp,
  pass,
}) {
  const received = []
  const receiver = createServer(async (request, response) => {
    let raw = ""
    for await (const chunk of request) raw += chunk
    const body = raw ? JSON.parse(raw) : undefined
    received.push({ method: request.method, body })
    response
      .writeHead(200, { "Content-Type": "application/scim+json" })
      .end(JSON.stringify({ id: `remote-${body?.externalId ?? "deleted"}` }))
  })
  await new Promise((resolve) => receiver.listen(0, "127.0.0.1", resolve))
  let operator
  try {
    const roles = await call(admin.context, "/roles")
    const administrator = roles.find((role) => role.name === "Administrator")
    const service = await call(admin.context, "/service-accounts", "POST", {
      login_name: stamp + "-scim-service",
      display_name: "SCIM acceptance actor",
    })
    await call(
      admin.context,
      `/roles/${administrator.id}/members/${service.id}`,
      "PUT",
      {}
    )
    const directory = await call(admin.context, "/scim-directories", "POST", {
      name: stamp,
      service_account_id: service.id,
    })
    const secret = await call(
      admin.context,
      `/scim-directories/${directory.id}/credentials`,
      "POST",
      { name: "Acceptance credential" }
    )
    const scim = async (path, method = "GET", body, expected = 200) => {
      const response = await fetch(apiURL + "/scim/v2" + path, {
        method,
        headers: {
          Authorization: "Bearer " + secret.bearer_token,
          "Content-Type": "application/scim+json",
        },
        ...(body === undefined ? {} : { body: JSON.stringify(body) }),
      })
      assert.equal(response.status, expected, `${method} SCIM ${path}`)
      return response.status === 204 ? undefined : response.json()
    }
    const schema = "urn:ietf:params:scim:schemas:core:2.0:User"
    const user = await scim(
      "/Users",
      "POST",
      {
        schemas: [schema],
        userName: stamp + "-scim-user",
        displayName: "Original local",
        emails: [{ value: stamp + "@example.test", primary: true }],
        active: true,
      },
      201
    )
    const globalBefore = await call(admin.context, `/principals/${user.id}`)
    const targetSecret = await call(admin.context, "/scim-targets", "POST", {
      name: stamp + "-target",
      base_url: `http://127.0.0.1:${receiver.address().port}/scim/v2`,
      bearer_token: "fixture-token",
      service_account_id: service.id,
    })
    const push = () =>
      call(
        admin.context,
        `/scim-targets/${targetSecret.target.id}/provisioning-jobs`,
        "POST",
        { resource_type: "User", resource_id: user.id }
      )
    await push()
    assert.equal(received.at(-1).body.active, true)
    await scim(`/Users/${user.id}`, "PUT", {
      schemas: [schema],
      userName: user.userName,
      displayName: "Tenant local",
      emails: [{ value: stamp + "-local@example.test", primary: true }],
      active: false,
    })
    const local = await scim(`/Users/${user.id}`)
    assert.equal(local.active, false)
    assert.equal(local.displayName, "Tenant local")
    assert.equal(local.emails[0].value, stamp + "-local@example.test")
    const globalAfter = await call(admin.context, `/principals/${user.id}`)
    assert.equal(globalAfter.status, "active")
    assert.equal(globalAfter.email, globalBefore.email)
    assert.equal(globalAfter.display_name, globalBefore.display_name)
    assert.equal(globalAfter.email_verified, globalBefore.email_verified)
    await push()
    assert.equal(received.at(-1).body.active, false)
    assert.equal(received.at(-1).body.displayName, "Tenant local")
    const mutation = await scim(
      `/Users/${user.id}`,
      "PUT",
      { schemas: [schema], userName: stamp + "-renamed", active: true },
      400
    )
    assert.equal(mutation.scimType, "mutability")
    await scim(
      `/Users/${user.id}`,
      "PATCH",
      {
        schemas: ["urn:ietf:params:scim:api:messages:2.0:PatchOp"],
        Operations: [
          { op: "replace", path: "displayName", value: "Must roll back" },
          { op: "replace", path: "userName", value: "forbidden" },
        ],
      },
      400
    )
    assert.equal((await scim(`/Users/${user.id}`)).displayName, "Tenant local")
    await scim(
      `/Users/${user.id}`,
      "PUT",
      { schemas: [schema], active: true },
      400
    )
    await scim(`/Users/${user.id}`, "PUT", {
      schemas: [schema],
      userName: user.userName,
      active: true,
    })
    const cleared = await scim(`/Users/${user.id}`)
    assert.equal(cleared.displayName, user.userName)
    assert.equal(cleared.emails?.length ?? 0, 0)
    const schemas = await scim("/Schemas")
    assert.equal(
      schemas.Resources.find((item) => item.id === schema).attributes.find(
        (item) => item.name === "userName"
      ).mutability,
      "immutable"
    )
    pass(
      "real inbound/outbound SCIM tenant disable exports false without global profile changes; immutable username and atomic PATCH rollback"
    )

    const password = randomBytes(24).toString("base64url") + "aA1!"
    const actor = await call(admin.context, "/principals", "POST", {
      login_name: stamp + "-tenant-admin",
      password,
      display_name: "Tenant operator",
    })
    await call(
      admin.context,
      `/roles/${administrator.id}/members/${actor.id}`,
      "PUT",
      {}
    )
    operator = await browser.newContext({ baseURL: webURL })
    const login = await operator.request.post("/api/login", {
      headers: { Origin: webURL },
      data: {
        login_name: actor.login_name,
        password,
        return_url: webURL + "/",
      },
    })
    assert(login.ok())
    await call(
      operator,
      `/principals/${user.id}`,
      "PATCH",
      { display_name: "Unauthorized global" },
      { status: 403 }
    )
    await call(operator, `/members/${user.id}`, "PATCH", {
      display_name: "Human local",
      email: stamp + "-human@example.test",
    })
    assert.equal((await scim(`/Users/${user.id}`)).displayName, "Human local")
    await call(admin.context, `/principals/${user.id}`, "PATCH", {
      display_name: "Platform global",
    })
    assert.equal(
      (await scim(`/Users/${user.id}`)).displayName,
      "Human local",
      "global edits must not mirror into membership"
    )
    const metadataTenant = await call(
      admin.context,
      "/tenants",
      "POST",
      {
        slug: stamp + "-metadata",
        name: "Metadata acceptance tenant",
        owner_principal_id: actor.id,
      },
      { tenant: false }
    )
    const certificates = async (tenantID) => {
      const response = await admin.context.request.get(
        `/api/saml/${tenantID}/metadata`
      )
      assert(response.ok())
      const result = await admin.page.evaluate(
        (xml) => {
          const document = new DOMParser().parseFromString(
            xml,
            "application/xml"
          )
          if (document.getElementsByTagName("parsererror").length)
            throw new Error("Invalid SAML metadata")
          return Array.from(
            document.getElementsByTagNameNS(
              "http://www.w3.org/2000/09/xmldsig#",
              "X509Certificate"
            ),
            (node) => node.textContent.replace(/\s/g, "")
          )
        },
        await response.text()
      )
      assert(result.length > 0)
      return result
    }
    const beforeRotation = await certificates(directory.tenant_id)
    assert.deepEqual(await certificates(metadataTenant.id), beforeRotation)
    await call(
      operator,
      "/system/saml-signing-keys",
      "POST",
      {},
      { tenant: false, status: 403 }
    )
    const retired = await operator.request.post(
      `/api/tenants/${directory.tenant_id}/saml-signing-keys`,
      { headers: { Origin: webURL }, data: {} }
    )
    assert.equal(retired.status(), 404)
    assert.deepEqual(await certificates(directory.tenant_id), beforeRotation)
    assert.deepEqual(await certificates(metadataTenant.id), beforeRotation)
    await call(
      admin.context,
      "/system/saml-signing-keys",
      "POST",
      {},
      { tenant: false }
    )
    const afterRotation = await certificates(directory.tenant_id)
    assert.notDeepEqual(afterRotation, beforeRotation)
    assert.deepEqual(await certificates(metadataTenant.id), afterRotation)
    pass(
      "tenant administrator edits local member but cannot alter global profile or shared signing key; platform rotation succeeds"
    )
  } finally {
    await operator?.close()
    await new Promise((resolve) => receiver.close(resolve))
  }
}
