import assert from "node:assert/strict"
import { createHash, generateKeyPairSync, randomBytes, sign } from "node:crypto"
import { createServer } from "node:http"
import { mkdirSync, readFileSync, writeFileSync } from "node:fs"
import { fileURLToPath } from "node:url"
import { chromium } from "playwright"

// Real IAM HTTP and browser flows. The upstream is an isolated, signed OIDC
// test provider with one-use codes, PKCE and an explicit fresh-login button.
// No IAM response is intercepted or replaced. SAML browser coverage is not
// claimed here; signed SAML callback/continuation evidence lives in Go tests.
const directory = new URL(
  "../../../../backend-api/.temp/iam-uat/",
  import.meta.url
)
const settings = JSON.parse(
  readFileSync(new URL("state.json", directory), "utf8")
)
assert.equal(
  createHash("sha256")
    .update(readFileSync(new URL("api", directory)))
    .digest("hex"),
  settings.binarySHA256,
  "UAT manifest must describe the actual binary"
)
const localPolicy = JSON.parse(
  readFileSync(new URL("config.yaml", directory), "utf8")
).federation
assert.equal(
  localPolicy.outbound_allow_plaintext,
  true,
  "Local UAT must explicitly permit its HTTP test IdP"
)
assert(
  localPolicy.outbound_allow_networks?.includes("127.0.0.1/32"),
  "Local UAT must explicitly permit its loopback test IdP"
)
const baseURL = settings.webURL
const apiURL = settings.apiURL
for (const value of [baseURL, apiURL])
  assert.equal(
    new URL(value).hostname,
    "127.0.0.1",
    "Only isolated loopback UAT is authorized"
  )
const locales = process.env.IAM_PERSONAL_LOCALES?.split(",") ?? [
  "en-US",
  "zh-CN",
  "ms-MY",
]
const widths = process.env.IAM_PERSONAL_WIDTHS?.split(",").map(Number) ?? [
  1440, 390,
]
assert(locales.every((value) => ["en-US", "zh-CN", "ms-MY"].includes(value)))
assert(widths.every((value) => [1440, 390].includes(value)))
const output = new URL("browser/personal-security/", directory)
mkdirSync(output, { recursive: true, mode: 0o700 })
const report = {
  at: new Date().toISOString(),
  status: "RUNNING",
  binarySHA256: settings.binarySHA256,
  scope:
    "Real password and federation-only personal PAT and identity-link flows; no IAM stubs; no browser SAML claim",
  locales,
  widths,
  results: [],
  cleanup: [],
}
const save = () =>
  writeFileSync(
    new URL("report.json", output),
    JSON.stringify(report, null, 2) + "\n",
    { mode: 0o600 }
  )
const secrets = new Set()
const remember = (value) => {
  if (value) secrets.add(value)
  return value
}
const redact = (error) => {
  let message = String(error?.message ?? error)
  for (const value of secrets) message = message.replaceAll(value, "[redacted]")
  return message
    .replace(/https?:\/\/[^\s"'<>]+/g, (value) => {
      try {
        const url = new URL(value)
        return url.origin + url.pathname
      } catch {
        return "[url]"
      }
    })
    .replace(/\bpat_[A-Za-z0-9_-]+/g, "[redacted]")
}
const stamp = `personal-${Date.now()}`
const password = remember(randomBytes(28).toString("base64url") + "aA1!")
const adminPassword = remember(
  readFileSync(new URL("admin-password", directory), "utf8").trim()
)
const clientID = "personal-security-audit"
const clientSecret = remember(randomBytes(32).toString("base64url"))
const keys = generateKeyPairSync("rsa", { modulusLength: 2048 })
const jwk = {
  ...keys.publicKey.export({ format: "jwk" }),
  kid: "audit-rsa",
  alg: "RS256",
  use: "sig",
}
const tickets = new Map()
const codes = new Map()
const callbacks = new Set()
const idp = {
  issuer: "",
  subject: "",
  fresh: 0,
  exchanged: 0,
  protocolErrors: 0,
}
const encode = (value) =>
  Buffer.from(JSON.stringify(value)).toString("base64url")
async function formBody(request) {
  let body = ""
  for await (const chunk of request) {
    body += chunk
    assert(body.length < 8192, "OIDC input limit")
  }
  return new URLSearchParams(body)
}
const server = createServer(async (request, response) => {
  const json = (body, status = 200) => {
    response.writeHead(status, {
      "Content-Type": "application/json",
      "Cache-Control": "no-store",
    })
    response.end(JSON.stringify(body))
  }
  try {
    const url = new URL(request.url, idp.issuer)
    if (url.pathname === "/.well-known/openid-configuration")
      return json({
        issuer: idp.issuer,
        authorization_endpoint: idp.issuer + "/authorize",
        token_endpoint: idp.issuer + "/token",
        jwks_uri: idp.issuer + "/jwks",
        response_types_supported: ["code"],
        subject_types_supported: ["public"],
        id_token_signing_alg_values_supported: ["RS256"],
      })
    if (url.pathname === "/jwks") return json({ keys: [jwk] })
    if (url.pathname === "/authorize" && request.method === "GET") {
      assert.equal(url.searchParams.get("client_id"), clientID)
      assert.equal(url.searchParams.get("response_type"), "code")
      assert.equal(url.searchParams.get("code_challenge_method"), "S256")
      assert(callbacks.has(url.searchParams.get("redirect_uri")))
      const state = remember(url.searchParams.get("state"))
      assert(state)
      const fresh = state.startsWith("account.")
      if (fresh) {
        assert.equal(url.searchParams.get("nonce"), state)
        assert.equal(url.searchParams.get("prompt"), "login")
        assert.equal(url.searchParams.get("max_age"), "0")
      }
      const ticket = remember(randomBytes(24).toString("base64url"))
      tickets.set(ticket, {
        query: url.searchParams,
        subject: idp.subject,
        fresh,
        expires: Date.now() + 60_000,
      })
      response.writeHead(200, {
        "Content-Type": "text/html; charset=utf-8",
        "Cache-Control": "no-store",
        "Content-Security-Policy": `default-src 'none'; form-action 'self' ${new URL(apiURL).origin} ${new URL(baseURL).origin}; style-src 'unsafe-inline'`,
      })
      response.end(
        `<!doctype html><html lang="en"><title>Local OIDC verification</title><body><main><h1>Local OIDC verification</h1><p>This isolated test provider requires a new authentication for this request.</p><form method="post" action="/authenticate"><input type="hidden" name="ticket" value="${ticket}"><button type="submit">Authenticate test identity</button></form></main></body></html>`
      )
      return
    }
    if (url.pathname === "/authenticate" && request.method === "POST") {
      const form = await formBody(request)
      const ticket = tickets.get(form.get("ticket"))
      tickets.delete(form.get("ticket"))
      assert(ticket && ticket.expires > Date.now())
      const code = remember(randomBytes(32).toString("base64url"))
      codes.set(code, { ...ticket, authTime: Math.floor(Date.now() / 1000) })
      if (ticket.fresh) idp.fresh++
      const target = new URL(ticket.query.get("redirect_uri"))
      target.searchParams.set("code", code)
      target.searchParams.set("state", ticket.query.get("state"))
      response.writeHead(303, {
        Location: target.href,
        "Cache-Control": "no-store",
      })
      response.end()
      return
    }
    if (url.pathname === "/token" && request.method === "POST") {
      const form = await formBody(request)
      assert.equal(
        request.headers.authorization,
        "Basic " + Buffer.from(`${clientID}:${clientSecret}`).toString("base64")
      )
      assert.equal(form.get("grant_type"), "authorization_code")
      const item = codes.get(form.get("code"))
      codes.delete(form.get("code"))
      assert(item && item.expires > Date.now())
      assert.equal(form.get("client_id"), clientID)
      assert.equal(form.get("redirect_uri"), item.query.get("redirect_uri"))
      assert.equal(
        createHash("sha256")
          .update(form.get("code_verifier") ?? "")
          .digest("base64url"),
        item.query.get("code_challenge")
      )
      const now = Math.floor(Date.now() / 1000)
      const data =
        encode({ alg: "RS256", kid: jwk.kid, typ: "JWT" }) +
        "." +
        encode({
          iss: idp.issuer,
          aud: clientID,
          sub: item.subject,
          email: `${item.subject}@example.test`,
          email_verified: true,
          name: item.subject,
          iat: now,
          exp: now + 120,
          auth_time: item.authTime,
          nonce: item.query.get("nonce") ?? "",
        })
      const token = remember(
        data +
          "." +
          sign("RSA-SHA256", Buffer.from(data), keys.privateKey).toString(
            "base64url"
          )
      )
      idp.exchanged++
      return json({ id_token: token, token_type: "Bearer" })
    }
    json({ error: "not_found" }, 404)
  } catch {
    idp.protocolErrors++
    json({ error: "invalid_request" }, 400)
  }
})
await new Promise((resolve) => server.listen(0, "127.0.0.1", resolve))
idp.issuer = `http://127.0.0.1:${server.address().port}`
const browser = await chromium.launch({
  channel: process.env.IAM_BROWSER_CHANNEL ?? "chrome",
  headless: true,
})
const admin = await browser.newContext({ baseURL })
const contexts = []
const principals = new Set()
const issued = []
let tenant, otherTenant, provider
async function response(context, path, method = "GET", data, tenantID) {
  if (path.includes("{principal_id}")) {
    const identity = await context.request.get("/api/me")
    assert(identity.ok())
    path = path.replace("{principal_id}", (await identity.json()).data.subject)
  }
  const requestPath = tenantID ? `/tenants/${tenantID}${path}` : path
  return context.request.fetch("/api" + requestPath, {
    method,
    ...(data === undefined ? {} : { data }),
    headers: { Origin: baseURL },
  })
}
async function ok(context, path, method = "GET", data, tenantID) {
  const result = await response(context, path, method, data, tenantID)
  assert(
    result.ok(),
    `${method} ${path.split("?")[0]}: HTTP ${result.status()}`
  )
  return (await result.json()).data
}
async function sessionCookie(context) {
  const cookies = await context.cookies(apiURL)
  const found = cookies.find((cookie) => cookie.name.startsWith("uat_"))
  assert(found, "Original browser session cookie exists")
  remember(found.value)
  return found.value
}
async function finishUpstream(page) {
  await page
    .getByRole("heading", { name: "Local OIDC verification", exact: true })
    .waitFor()
  await page
    .getByRole("button", { name: "Authenticate test identity", exact: true })
    .click()
  try {
    await page.waitForURL(
      (url) =>
        url.origin === new URL(baseURL).origin &&
        url.pathname.startsWith("/user/")
    )
  } catch (error) {
    throw new Error(
      `${redact(error)}; returned page: ${redact(page.url())}; ${redact(await page.locator("body").innerText()).slice(0, 1500)}`
    )
  }
}
async function confirmIdentity(page, messages, method = "password") {
  const dialog = page.getByRole("dialog")
  await dialog
    .getByRole("heading", { name: messages.confirmIdentity, exact: true })
    .waitFor()
  await page.locator("#reauth-method").waitFor()
  if (method === "password") {
    await page.locator("#reauth-method").selectOption("password")
    await page.locator("#reauth-password").fill(password)
  } else {
    assert.equal(
      await page.locator('#reauth-method option[value="password"]').count(),
      0,
      "Pure federation account has no password method"
    )
    assert(
      (await page.locator("#reauth-method").inputValue()).startsWith(
        "federation:"
      )
    )
  }
  await dialog
    .getByRole("button", { name: messages.continue, exact: true })
    .click()
  if (method === "federation") await finishUpstream(page)
}
async function createAndRevokeToken(context, page, messages, label, method) {
  await page.goto("/user/tokens")
  await page
    .getByRole("heading", { name: messages.personalTokens, exact: true })
    .waitFor()
  await page
    .getByRole("button", { name: messages.createPersonalToken, exact: true })
    .click()
  await page.locator("#token-name").fill(label)
  await page.locator("#token-tenant").selectOption(tenant.id)
  await page.getByRole("checkbox", { name: "role_view", exact: true }).check()
  const expires = new Date(Date.now() + 60 * 60_000)
  const local = new Date(
    expires.getTime() - expires.getTimezoneOffset() * 60_000
  )
    .toISOString()
    .slice(0, 16)
  await page.locator('input[name="expires_at"]').fill(local)
  await page
    .getByRole("dialog")
    .getByRole("button", { name: messages.continue, exact: true })
    .click()
  const originalSession = await sessionCookie(context)
  const originalSubject = (await ok(context, "/me")).subject
  const beforeFresh = idp.fresh
  await confirmIdentity(page, messages, method)
  const secretNode = page.getByTestId("personal-token-secret")
  await secretNode.waitFor()
  const secret = remember(await secretNode.innerText())
  assert(secret.startsWith("pat_"), "Creation displays the one-time secret")
  assert.equal(
    await sessionCookie(context),
    originalSession,
    "Reauthentication must not replace the browser session"
  )
  assert.equal((await ok(context, "/me")).subject, originalSubject)
  if (method === "federation") assert.equal(idp.fresh, beforeFresh + 1)
  const listing = await ok(context, "/personal-tokens")
  const token = listing.find((item) => item.name === label)
  assert(token, "Created PAT is listed")
  issued.push({ context, id: token.id })
  assert.deepEqual(token.permissions, ["role_view"])
  assert.equal(token.tenant_id, tenant.id)
  assert(!JSON.stringify(listing).includes(secret))
  assert(!Object.hasOwn(token, "token") && !Object.hasOwn(token, "token_hash"))
  const bearer = async (path, tenantID = tenant.id) =>
    context.request.get(`/api/tenants/${tenantID}${path}`, {
      headers: { Authorization: `Bearer ${secret}` },
    })
  assert.equal(
    (await bearer("/roles")).status(),
    200,
    "Allowed tenant permission remains usable"
  )
  assert.equal(
    (await bearer("/principal-groups")).status(),
    401,
    "Missing token permission is denied"
  )
  assert.equal(
    (await bearer("/roles", otherTenant.id)).status(),
    401,
    "Cross-tenant token use is denied"
  )
  assert.equal(
    (await bearer("/me")).status(),
    401,
    "PAT cannot substitute for a browser session"
  )
  const closeButton = page
    .getByRole("dialog")
    .getByRole("button", { name: messages.close, exact: true })
    .and(page.locator('[data-slot="button"]'))
  const descriptionBox = await page
    .getByText(messages.personalTokenOnce, { exact: true })
    .boundingBox()
  const secretBox = await secretNode.boundingBox()
  const closeBox = await closeButton.boundingBox()
  assert(descriptionBox && secretBox && closeBox)
  assert(
    descriptionBox.y + descriptionBox.height <= secretBox.y,
    "One-time-secret description must not overlap the token"
  )
  assert(
    secretBox.y + secretBox.height <= closeBox.y,
    "One-time secret must not overlap the dialog actions"
  )
  await page.screenshot({
    path: fileURLToPath(new URL(`${label}-created.png`, output)),
    fullPage: true,
    mask: [secretNode],
  })
  await closeButton.click()
  await page.reload()
  await page.getByRole("heading", { name: label, exact: true }).waitFor()
  assert.equal(
    await page.getByTestId("personal-token-secret").count(),
    0,
    "Reload cannot retrieve the secret"
  )
  assert(!(await page.locator("body").innerText()).includes(secret))
  assert.equal(
    await page.evaluate(
      (value) =>
        [...Object.values(localStorage), ...Object.values(sessionStorage)].some(
          (item) => item.includes(value)
        ),
      secret
    ),
    false,
    "Secret must not be retained in browser storage"
  )
  const item = page
    .locator("li")
    .filter({ has: page.getByRole("heading", { name: label, exact: true }) })
  await item.getByRole("button", { name: messages.revoke, exact: true }).click()
  await page
    .getByRole("dialog")
    .getByRole("button", { name: messages.revoke, exact: true })
    .click()
  await item
    .getByRole("button", { name: messages.revoked, exact: true })
    .waitFor()
  assert.equal(
    (await bearer("/roles")).status(),
    401,
    "Revocation immediately rejects the real bearer"
  )
  assert(
    await page.evaluate(
      () => document.documentElement.scrollWidth <= innerWidth
    )
  )
  await page.screenshot({
    path: fileURLToPath(new URL(`${label}-revoked.png`, output)),
    fullPage: true,
  })
}
async function passwordLink(context, page, messages) {
  await page.goto("/user/identities")
  await page
    .getByRole("heading", { name: messages.linkedIdentities, exact: true })
    .waitFor()
  await page.locator("#identity-provider").selectOption(provider.id)
  const oldCookie = await sessionCookie(context)
  const oldSession = await ok(context, "/me")
  const membersBefore = await ok(
    admin,
    "/principals",
    "GET",
    undefined,
    tenant.id
  )
  const roleMemberships = async () => {
    const roles = await ok(admin, "/roles", "GET", undefined, tenant.id)
    return Promise.all(
      roles.map(async (role) => ({
        id: role.id,
        members: (
          await ok(
            admin,
            `/roles/${role.id}/members`,
            "GET",
            undefined,
            tenant.id
          )
        ).sort(),
      }))
    )
  }
  const rolesBefore = await roleMemberships()
  await page
    .locator("form")
    .getByRole("button", { name: messages.linkIdentity, exact: true })
    .click()
  await confirmIdentity(page, messages)
  await finishUpstream(page)
  await page
    .getByRole("heading", { name: provider.name, exact: true })
    .waitFor()
  assert.equal(await sessionCookie(context), oldCookie)
  assert.equal((await ok(context, "/me")).subject, oldSession.subject)
  assert.deepEqual(
    await ok(admin, "/principals", "GET", undefined, tenant.id),
    membersBefore,
    "Personal linking must not JIT-provision a principal or membership"
  )
  assert.deepEqual(
    await roleMemberships(),
    rolesBefore,
    "Personal linking must not grant roles"
  )
  const links = await ok(context, "/identity-links")
  assert.equal(
    links.filter((link) => link.provider_id === provider.id).length,
    1
  )
  await page
    .locator("li")
    .filter({
      has: page.getByRole("heading", { name: provider.name, exact: true }),
    })
    .getByRole("button", { name: messages.unlinkIdentity, exact: true })
    .click()
  await confirmIdentity(page, messages)
  await page.getByText(messages.noLinkedIdentities, { exact: true }).waitFor()
  assert.equal((await ok(context, "/identity-links")).length, 0)
}
try {
  save()
  await ok(admin, "/login", "POST", {
    login_name: "admin@example.test",
    password: adminPassword,
    return_url: baseURL + "/",
  })
  await sessionCookie(admin)
  const owner = (await ok(admin, "/me")).subject
  tenant = await ok(admin, "/tenants", "POST", {
    name: stamp,
    slug: stamp,
    owner_principal_id: owner,
  })
  otherTenant = await ok(admin, "/tenants", "POST", {
    name: stamp + "-other",
    slug: stamp + "-other",
    owner_principal_id: owner,
  })
  const actor = await ok(
    admin,
    "/principals",
    "POST",
    {
      login_name: stamp + "-password",
      password,
      display_name: stamp + "-password",
    },
    tenant.id
  )
  principals.add(actor.id)
  const role = await ok(
    admin,
    "/roles",
    "POST",
    {
      name: stamp + "-reader",
      description: "Isolated personal security audit",
    },
    tenant.id
  )
  await ok(
    admin,
    `/roles/${role.id}/permissions/role_view`,
    "PUT",
    {},
    tenant.id
  )
  await ok(admin, `/roles/${role.id}/members/${actor.id}`, "PUT", {}, tenant.id)
  provider = await ok(
    admin,
    "/identity-providers",
    "POST",
    {
      name: stamp + "-OIDC",
      provider_type: "oidc",
      issuer: idp.issuer,
      client_id: clientID,
      client_secret: clientSecret,
      scopes: "openid profile email",
      auto_provision: true,
      default_role_id: role.id,
      status: "active",
    },
    tenant.id
  )
  callbacks.add(`${apiURL}/federation/${provider.id}/callback`)
  for (const locale of locales)
    for (const width of widths) {
      const messages = JSON.parse(
        readFileSync(
          new URL(`../src/messages/${locale}.json`, import.meta.url),
          "utf8"
        )
      )
      for (const method of ["password", "federation"]) {
        const context = await browser.newContext({
          baseURL,
          viewport: { width, height: 950 },
        })
        contexts.push(context)
        await context.addCookies([
          { name: "NEXT_LOCALE", value: locale, url: baseURL },
        ])
        const page = await context.newPage()
        page.setDefaultTimeout(15_000)
        const errors = []
        page.on("pageerror", (error) => errors.push(redact(error)))
        page.on("response", async (result) => {
          if (
            /^\/api\/principals\/[^/]+\/reauthentication\/challenges$/.test(
              new URL(result.url()).pathname
            ) &&
            result.ok()
          ) {
            try {
              remember((await result.json()).data?.proof)
            } catch {
              /* Navigation can dispose an already-consumed response. */
            }
          }
        })
        const label = `${stamp}-${locale}-${width}-${method}`
        idp.subject = label
        if (method === "password") {
          await page.goto("/user/tokens")
          await page.locator('input[name="login_name"]').fill(actor.login_name)
          await page.locator('input[name="password"]').fill(password)
          await page.locator('button[type="submit"]').click()
          await page
            .getByRole("heading", {
              name: messages.personalTokens,
              exact: true,
            })
            .waitFor()
        } else {
          await page.goto(
            `${apiURL}/federation/${provider.id}/start?${new URLSearchParams({ return_url: baseURL + "/user/tokens" })}`
          )
          await finishUpstream(page)
          const session = await ok(context, "/me")
          principals.add(session.subject)
          const methods = await ok(
            context,
            "/principals/{principal_id}/reauthentication/methods"
          )
          assert.deepEqual(
            methods.methods,
            [],
            "Initial OIDC login creates a genuinely passwordless fixture"
          )
        }
        await createAndRevokeToken(context, page, messages, label, method)
        if (method === "password") await passwordLink(context, page, messages)
        else {
          await page.goto("/user/identities")
          await page
            .getByRole("heading", { name: provider.name, exact: true })
            .waitFor()
          await page
            .getByRole("button", { name: messages.unlinkIdentity, exact: true })
            .click()
          const rejected = page.waitForResponse(
            (result) =>
              new URL(result.url()).pathname.startsWith(
                "/api/identity-links/"
              ) && result.request().method() === "DELETE"
          )
          await confirmIdentity(page, messages, "federation")
          assert.equal(
            (await rejected).status(),
            409,
            "The server rejects removal of the last login method"
          )
          await page.getByRole("dialog").getByRole("alert").waitFor()
          assert.equal(
            (await ok(context, "/identity-links")).length,
            1,
            "Last usable federation identity cannot be unlinked"
          )
        }
        assert.deepEqual(errors, [])
        report.results.push({
          locale,
          width,
          method,
          status: "PASS",
          checks: [
            "fresh operation proof",
            "scope",
            "one-time secret",
            "revocation",
            method === "password"
              ? "identity link/unlink without JIT or session replacement"
              : "pure federation reauth and last-method protection",
          ],
        })
        save()
        console.log(`PASS personal security ${locale} ${width} ${method}`)
      }
    }
  assert.equal(idp.protocolErrors, 0)
  report.oidc = {
    freshAuthentications: idp.fresh,
    signedCodeExchanges: idp.exchanged,
  }
  report.status = "PASS"
} catch (error) {
  report.status = "FAIL"
  report.error = redact(error)
} finally {
  for (const token of issued) {
    try {
      await ok(token.context, `/personal-tokens/${token.id}`, "DELETE")
    } catch (error) {
      report.cleanup.push({
        token: token.id,
        status: "FAIL",
        error: redact(error),
      })
    }
  }
  if (tenant) {
    try {
      const discovered = await ok(
        admin,
        `/principals?search=${encodeURIComponent(stamp)}&limit=200`,
        "GET",
        undefined,
        tenant.id
      )
      for (const principal of discovered.items) {
        if (principal.display_name?.startsWith(stamp))
          principals.add(principal.id)
      }
    } catch (error) {
      report.cleanup.push({
        action: "discover any interrupted synthetic JIT fixture",
        status: "FAIL",
        error: redact(error),
      })
    }
  }
  for (const principal of principals) {
    try {
      await ok(
        admin,
        `/principals/${principal}/suspension`,
        "PUT",
        undefined,
        tenant.id
      )
      report.cleanup.push({
        principal,
        status: "PASS",
        action: "synthetic principal suspended",
      })
    } catch (error) {
      report.cleanup.push({ principal, status: "FAIL", error: redact(error) })
    }
  }
  if (provider) {
    try {
      await ok(
        admin,
        `/identity-providers/${provider.id}`,
        "DELETE",
        undefined,
        tenant.id
      )
      report.cleanup.push({ provider: provider.id, status: "PASS" })
    } catch (error) {
      report.cleanup.push({
        provider: provider.id,
        status: "FAIL",
        error: redact(error),
      })
    }
  }
  for (const owned of [otherTenant, tenant].filter(Boolean)) {
    try {
      const current = (await ok(admin, "/tenants")).find(
        (item) => item.id === owned.id
      )
      assert(current)
      await ok(
        admin,
        `/tenants/${owned.id}?version=${current.version}`,
        "DELETE"
      )
      report.cleanup.push({ tenant: owned.id, status: "PASS" })
    } catch (error) {
      report.cleanup.push({
        tenant: owned.id,
        status: "FAIL",
        error: redact(error),
      })
    }
  }
  if (report.cleanup.some((item) => item.status === "FAIL"))
    report.status = "FAIL"
  save()
  await Promise.all(contexts.map((context) => context.close()))
  await admin.close()
  await browser.close()
  await new Promise((resolve) => server.close(resolve))
}
if (report.status !== "PASS")
  throw new Error(
    "Personal security audit failed; inspect its sanitized report.json"
  )
console.log(
  `PASS ${report.results.length} personal security scenarios; fixture cleanup complete`
)
