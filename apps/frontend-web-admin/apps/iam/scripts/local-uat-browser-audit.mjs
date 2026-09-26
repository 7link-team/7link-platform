import assert from "node:assert/strict"
import { mkdirSync, readFileSync, writeFileSync } from "node:fs"
import { resolve, sep } from "node:path"
import { fileURLToPath, pathToFileURL } from "node:url"
import { chromium } from "playwright"
import { createHash, randomBytes } from "node:crypto"

const directory = process.env.IAM_UAT_DIRECTORY
  ? pathToFileURL(resolve(process.env.IAM_UAT_DIRECTORY) + sep)
  : new URL("../../../../backend-api/.temp/iam-uat/", import.meta.url)
const settings = JSON.parse(
  readFileSync(new URL("state.json", directory), "utf8")
)
const tenant = readFileSync(new URL("tenant-id", directory), "utf8").trim()
const baseURL = settings.webURL
assert.equal(
  new URL(baseURL).hostname,
  "127.0.0.1",
  "Only the isolated local UAT is in scope"
)
const screenshots = new URL("browser/", directory)
mkdirSync(screenshots, { recursive: true, mode: 0o700 })
let browser
const results = []
const releaseChecks = process.argv.includes("--release")
const reportPath = new URL(
  releaseChecks ? "release-report.json" : "report.json",
  screenshots
)

async function verifyReleaseFlows(context, page, capture, device) {
  const createdEntities = []
  const createdClients = []
  const resourceContext = await browser.newContext({ baseURL })
  const resourceStatus = async (token) =>
    (
      await resourceContext.request.get("/api/me/tenants", {
        headers: { Authorization: `Bearer ${token}` },
      })
    ).status()
  const call = async (path, method = "GET", body, scoped = true) => {
    const requestPath = scoped ? `/tenants/${tenant}${path}` : path
    const response = await context.request.fetch("/api" + requestPath, {
      method,
      headers: { Origin: baseURL },
      ...(body ? { data: body } : {}),
    })
    assert(
      response.ok(),
      `${method} ${path.split("?")[0]} failed with ${response.status()}`
    )
    return (await response.json()).data
  }
  try {
    const stamp = randomBytes(6).toString("hex")
    const createEntity = async (name, parent_id) => {
      const entity = await call("/entities", "POST", {
        name,
        parent_id,
        type: "company",
        status: "active",
        metadata: {},
      })
      createdEntities.push(entity.id)
      return entity
    }
    const parent = await createEntity(`UAT parent ${stamp}`)
    const child = await createEntity(`UAT child ${stamp}`, parent.id)
    const descendant = await createEntity(`UAT descendant ${stamp}`, child.id)
    await page.goto(`/tenants/${tenant}/entities/${child.id}/manage`)
    const rows = page.locator("tbody tr")
    await rows.first().waitFor()
    assert.equal(
      await rows.count(),
      1,
      "Scoped settings show only the selected entity"
    )
    const rowText = await rows.first().innerText()
    assert(rowText.includes(child.name))
    assert(rowText.includes(parent.name), "The real parent must be preserved")
    assert(!rowText.includes(descendant.name))
    await capture("child-settings")
    results.push({
      persona: "admin",
      device,
      flow: "real-child-settings",
      status: "PASS",
    })

    const callback = baseURL + "/me/applications"
    const createClient = async (name) => {
      const application = await call("/oauth-clients", "POST", {
        name,
        redirect_uris: [callback],
        grant_types: ["authorization_code", "refresh_token"],
        scopes: ["openid", "profile"],
        public_client: false,
        consent_mode: "explicit",
      })
      createdClients.push(application.client.id)
      return application
    }
    const client = await createClient(`UAT consent ${stamp}`)
    const otherClient = await createClient(`UAT other consent ${stamp}`)
    const clientID = client.client.id
    const grantTokens = async (application) => {
      const verifier = randomBytes(32).toString("base64url")
      const challenge = createHash("sha256")
        .update(verifier)
        .digest("base64url")
      const query = new URLSearchParams({
        client_id: application.client.id,
        redirect_uri: callback,
        response_type: "code",
        scope: "openid profile",
        code_challenge: challenge,
        code_challenge_method: "S256",
        state: stamp,
        prompt: "consent",
      })
      const authorization = await context.request.get(
        "/api/oauth/authorize?" + query,
        { maxRedirects: 0 }
      )
      assert.equal(
        authorization.status(),
        302,
        "Authorization must require consent"
      )
      const consentLocation = new URL(authorization.headers().location, baseURL)
      const parkedChallenge =
        consentLocation.searchParams.get("consent_challenge")
      assert(
        Boolean(parkedChallenge),
        "A real one-shot consent challenge is required"
      )
      const decision = await context.request.post("/api/oauth/consent", {
        headers: { Origin: baseURL },
        form: { consent_challenge: parkedChallenge, decision: "allow" },
        maxRedirects: 0,
      })
      assert.equal(
        decision.status(),
        302,
        "Consent approval must issue an authorization code"
      )
      const redirect = new URL(decision.headers().location, baseURL)
      assert.equal(redirect.searchParams.get("state"), stamp)
      const code = redirect.searchParams.get("code")
      assert(Boolean(code), "Consent must produce a real authorization code")
      const basic =
        "Basic " +
        Buffer.from(
          `${application.client.id}:${application.client_secret}`
        ).toString("base64")
      const exchange = await context.request.post("/api/oauth/token", {
        headers: { Authorization: basic },
        form: {
          grant_type: "authorization_code",
          code,
          redirect_uri: callback,
          code_verifier: verifier,
        },
      })
      assert.equal(
        exchange.status(),
        200,
        "Authorization code exchange must succeed"
      )
      const tokens = await exchange.json()
      assert(Boolean(tokens.access_token))
      assert(Boolean(tokens.refresh_token))
      return { ...tokens, basic }
    }
    const tokens = await grantTokens(client)
    const otherTokens = await grantTokens(otherClient)
    const basic = tokens.basic
    const introspect = async (issued = tokens) => {
      const response = await context.request.post("/api/oauth/introspect", {
        headers: { Authorization: issued.basic },
        form: { token: issued.access_token },
      })
      assert.equal(response.status(), 200)
      return response.json()
    }
    assert.equal((await introspect()).active, true)
    assert.equal(
      await resourceStatus(tokens.access_token),
      200,
      "The issued bearer token must access a real IAM resource without cookies"
    )
    assert.equal(await resourceStatus(otherTokens.access_token), 200)
    const grants = await call("/me/oauth-consents", "GET", undefined, false)
    assert(grants.some((grant) => grant.client_id === clientID))
    const denied = await context.request.delete(
      `/api/me/oauth-consents/${clientID}`,
      { headers: { Origin: "https://untrusted.example.test" } }
    )
    assert.equal(
      denied.status(),
      403,
      "Cookie-authenticated cross-origin revocation must be refused"
    )
    await page.goto("/me/applications")
    await page
      .getByRole("heading", { name: client.client.name, exact: true })
      .waitFor()
    await capture("authorized-applications")
    await page
      .getByRole("button", { name: `撤销 ${client.client.name}`, exact: true })
      .click()
    await page
      .getByRole("dialog")
      .getByRole("button", { name: "确认", exact: true })
      .click()
    await page.getByRole("dialog").waitFor({ state: "hidden" })
    await page
      .getByRole("heading", { name: client.client.name, exact: true })
      .waitFor({ state: "hidden" })
    assert(
      !(await call("/me/oauth-consents", "GET", undefined, false)).some(
        (grant) => grant.client_id === clientID
      )
    )
    const accessActive = (await introspect()).active
    const revokedResourceStatus = await resourceStatus(tokens.access_token)
    const refresh = await context.request.post("/api/oauth/token", {
      headers: { Authorization: basic },
      form: {
        grant_type: "refresh_token",
        refresh_token: tokens.refresh_token,
      },
    })
    const refreshError = (await refresh.json()).error
    await capture("consent-revoked")
    results.push({
      persona: "admin",
      device,
      flow: "real-consent-code-token-ui-revoke-readback",
      csrfCrossOrigin: "403",
      accessTokenAfterRevoke: accessActive ? "active" : "inactive",
      resourceAfterRevoke: revokedResourceStatus,
      refreshTokenAfterRevoke:
        refreshError === "invalid_grant" ? "invalid_grant" : "unexpected",
      status:
        !accessActive &&
        revokedResourceStatus === 401 &&
        refresh.status() === 400 &&
        refreshError === "invalid_grant"
          ? "PASS"
          : "FAIL",
    })
    assert.equal(
      accessActive,
      false,
      "UI revocation must invalidate the issued access token"
    )
    assert.equal(refresh.status(), 400, "The revoked grant must not refresh")
    assert.equal(refreshError, "invalid_grant")
    assert.equal(
      revokedResourceStatus,
      401,
      "Real resources must reject the revoked bearer independently of introspection"
    )
    assert.equal(
      (await introspect(otherTokens)).active,
      true,
      "Revoking one application's grant must not revoke another application"
    )
    assert.equal(await resourceStatus(otherTokens.access_token), 200)
    const renewed = await grantTokens(client)
    assert.equal((await introspect(renewed)).active, true)
    assert.equal(await resourceStatus(renewed.access_token), 200)
    assert.equal(
      (await introspect()).active,
      false,
      "New consent must not revive an old access token"
    )
    assert.equal(await resourceStatus(tokens.access_token), 401)
    assert.equal((await introspect(otherTokens)).active, true)
    assert.equal(await resourceStatus(otherTokens.access_token), 200)
    results.push({
      persona: "admin",
      device,
      flow: "real-consent-regrant-and-other-application-isolation",
      newTokenResource: 200,
      oldTokenResource: 401,
      otherApplicationResource: 200,
      oldTokenIntrospection: "inactive",
      status: "PASS",
    })
  } finally {
    // Delete only IDs created by this invocation, children before parents.
    const cleanup = []
    for (const id of createdClients.reverse())
      cleanup.push(`/oauth-clients/${id}`)
    for (const id of createdEntities.reverse())
      cleanup.push(`/entities/${id}`)
    for (const path of cleanup) {
      const response = await context.request.delete(`/api/tenants/${tenant}${path}`, {
        headers: { Origin: baseURL },
      })
      assert(response.ok(), "UAT-owned fixture cleanup failed")
    }
    await resourceContext.close()
  }
}
writeFileSync(
  reportPath,
  JSON.stringify({ status: "RUNNING", at: new Date().toISOString() }) + "\n",
  { mode: 0o600 }
)

try {
  browser = await chromium.launch({ channel: "chrome", headless: true })
  for (const persona of ["admin", "member"]) {
    for (const [device, viewport] of [
      ["desktop", { width: 1440, height: 1000 }],
      ["mobile", { width: 390, height: 844 }],
    ]) {
      const context = await browser.newContext({ baseURL, viewport })
      await context.addCookies([
        { name: "NEXT_LOCALE", value: "zh-CN", url: baseURL },
      ])
      const page = await context.newPage()
      const errors = []
      page.on("pageerror", (error) => errors.push(error.message))
      page.on("console", (message) => {
        if (
          message.type() === "error" &&
          message.text().includes("Maximum update depth")
        )
          errors.push(message.text())
      })
      const capture = async (name) => {
        assert(
          await page.evaluate(
            () => document.documentElement.scrollWidth <= innerWidth
          ),
          "Page must not overflow horizontally"
        )
        await page.screenshot({
          path: fileURLToPath(
            new URL(`${persona}-${device}-${name}.png`, screenshots)
          ),
          fullPage: true,
          animations: "disabled",
        })
      }
      try {
        await page.goto("/login")
        await page.locator('input[name="login_name"]').waitFor()
        await capture("login")
        await page
          .locator('input[name="login_name"]')
          .fill(`${persona}@example.test`)
        await page
          .locator('input[name="password"]')
          .fill(
            readFileSync(
              new URL(`${persona}-password`, directory),
              "utf8"
            ).trim()
          )
        await page.locator('button[type="submit"]').click()
        await page
          .getByRole("button", { name: "切换工作空间", exact: true })
          .waitFor()
        const memberships = await context.request.get("/api/me/tenants")
        assert(memberships.ok(), "Tenant discovery must succeed")
        const targetTenant = (await memberships.json()).data.find(
          (item) => item.id === tenant
        )
        assert(targetTenant, "The configured UAT tenant must be accessible")
        await page
          .getByRole("button", { name: "切换工作空间", exact: true })
          .click()
        const switcher = page.getByRole("dialog", { name: "切换工作空间" })
        await switcher
          .getByRole("button")
          .filter({ has: page.getByText(targetTenant.slug, { exact: true }) })
          .click()
        await switcher
          .getByRole("button", { name: "租户视角", exact: true })
          .click()
        await page
          .locator('[aria-label="当前工作区"]')
          .getByText(tenant, { exact: true })
          .waitFor()
        await page.locator("main article").first().waitFor()
        await capture("dashboard")
        if (device === "mobile") {
          await page.getByRole("button", { name: "打开主导航" }).click()
          await page.getByRole("dialog").waitFor()
        }
        const navigation = page
          .getByRole("navigation", { name: "主导航" })
          .filter({ visible: true })
        await navigation.waitFor()
        if (persona === "admin") {
          await navigation
            .getByRole("link", { name: "账号与成员", exact: true })
            .waitFor()
          await capture("navigation")
          await navigation
            .getByRole("link", { name: "账号与成员", exact: true })
            .click()
          if (device === "mobile") await page.keyboard.press("Escape")
          await page
            .getByRole("button", {
              name: "UAT Member的数据保留与擦除",
              exact: true,
            })
            .waitFor()
          await capture("users")
          await page
            .getByRole("button", {
              name: "UAT Member的数据保留与擦除",
              exact: true,
            })
            .click()
          await page.getByRole("dialog").waitFor()
          const erase = page.getByRole("button", {
            name: "永久擦除",
            exact: true,
          })
          await erase.waitFor()
          await page
            .getByRole("dialog")
            .getByText("启用中", { exact: true })
            .waitFor()
          await page
            .getByRole("dialog")
            .getByText(/账号仍可登录/)
            .waitFor()
          assert(
            await erase.isDisabled(),
            "An active unconfirmed fixture must not be erasable"
          )
          await capture("lifecycle")
          await page.keyboard.press("Escape")
        } else {
          assert.equal(
            await navigation
              .getByRole("link", { name: "账号与成员", exact: true })
              .count(),
            0
          )
          assert.equal(
            await navigation
              .getByRole("link", { name: "租户管理", exact: true })
              .count(),
            0
          )
          await capture("navigation")
          if (device === "mobile") await page.keyboard.press("Escape")
          const denied = page.waitForResponse(
            (response) =>
              response.url().includes("/api/principals?") &&
              response.status() === 403
          )
          await page.goto(`/tenants/${tenant}/users`)
          await denied
          await page.getByRole("alert").filter({ hasText: "禁止" }).waitFor()
          assert.equal(
            await page.getByText("暂无本地账号", { exact: true }).count(),
            0
          )
          assert.equal(await page.locator("tbody tr").count(), 0)
          assert.equal(
            await page
              .getByRole("button", { name: /的数据保留与擦除$/ })
              .count(),
            0
          )
          await capture("denied")
          for (const [path, empty, screenshot] of [
            ["/platform/tenants", "暂无租户", "tenants-denied"],
            [
              `/tenants/${tenant}/oauth-clients`,
              "尚未登记 OAuth 客户端",
              "oauth-clients-denied",
            ],
          ]) {
            await page.goto(path)
            await page.getByRole("alert").filter({ hasText: "禁止" }).waitFor()
            assert.equal(
              await page.getByText(empty, { exact: true }).count(),
              0
            )
            assert.equal(
              await page.getByText("0 个客户端", { exact: true }).count(),
              0
            )
            await page
              .getByRole("button", { name: "立即重试", exact: true })
              .click()
            await page.getByRole("alert").filter({ hasText: "禁止" }).waitFor()
            assert.equal(
              await page.getByText(empty, { exact: true }).count(),
              0
            )
            await capture(screenshot)
          }
        }
        if (releaseChecks && persona === "admin")
          await verifyReleaseFlows(context, page, capture, device)
        await page.locator('header button[aria-haspopup="menu"]').last().click()
        await page.getByRole("menuitem", { name: "退出登录" }).click()
        await page.locator('input[name="login_name"]').waitFor()
        assert.equal(
          (await context.request.get("/api/me")).status(),
          401
        )
        await page.goto(`/tenants/${tenant}/users`)
        await page.locator('input[name="login_name"]').waitFor()
        assert.equal(new URL(page.url()).pathname, "/login")
        assert.deepEqual(errors, [])
        results.push({ persona, device, status: "PASS" })
      } catch (error) {
        await page
          .screenshot({
            path: fileURLToPath(
              new URL(`${persona}-${device}-failure.png`, screenshots)
            ),
            fullPage: true,
          })
          .catch(() => {})
        throw error
      } finally {
        await context.request
          .delete("/api/sessions/current", {
            headers: { Origin: baseURL },
          })
          .catch(() => {})
        await context.close()
      }
    }
  }
  const report = {
    status: "PASS",
    at: new Date().toISOString(),
    browser: browser.version(),
    binarySHA256: settings.binarySHA256,
    results,
    scope:
      "UI login, tenant choice, navigation, read-only lifecycle guard, member denial, logout and protected-route redirect" +
      (releaseChecks
        ? "; real child settings, OAuth consent/code/token creation, cross-origin rejection, UI revocation and token/list readback"
        : ""),
    destructiveLifecycle: "NOT_RUN",
    productionAcceptance: "NOT_READY",
  }
  writeFileSync(reportPath, JSON.stringify(report, null, 2) + "\n", {
    mode: 0o600,
  })
  console.log(JSON.stringify(report, null, 2))
} catch (error) {
  writeFileSync(
    reportPath,
    JSON.stringify(
      { status: "FAIL", at: new Date().toISOString(), results },
      null,
      2
    ) + "\n",
    { mode: 0o600 }
  )
  throw error
} finally {
  await browser?.close()
}
