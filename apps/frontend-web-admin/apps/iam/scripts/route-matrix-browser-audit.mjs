import assert from "node:assert/strict"
import { readFileSync, mkdirSync, writeFileSync } from "node:fs"
import { randomBytes, createHash } from "node:crypto"
import { fileURLToPath } from "node:url"
import { chromium } from "playwright"

// Real API fixtures and real pages. Network-failure injection belongs to the
// existing release-regression audit; this matrix never stubs HTTP responses.
const routes = [
  ["dashboard", "/", "identityControl", "/authn/sessions", null],
  ["users", "/iam/users", "principalMembers", "/iam/principals", "principal"],
  [
    "invitations",
    "/iam/invitations",
    "memberInvitations",
    "/iam/invitations",
    "invitation",
  ],
  [
    "service-accounts",
    "/iam/service-accounts",
    "serviceAccounts",
    "/iam/service-accounts",
    "machine",
  ],
  ["tenants", "/iam/tenants", "tenantManagement", "/iam/tenants", "tenant"],
  ["entities", "/iam/entities", "entityManagement", "/iam/entities", "entity"],
  [
    "departments",
    "/iam/departments",
    "departmentManagement",
    "/iam/departments",
    "department",
  ],
  [
    "positions",
    "/iam/positions",
    "positionManagement",
    "/iam/positions",
    "position",
  ],
  ["groups", "/iam/groups", "groupManagement", "/iam/groups", "group"],
  ["roles", "/iam/roles", "rolePermissions", "/iam/roles", "role"],
  [
    "field-policies",
    "/iam/field-policies",
    "fieldPermissions",
    "/iam/field-policies",
    "field",
  ],
  [
    "access-requests",
    "/iam/access-requests",
    "accessRequests",
    "/iam/my/access-requests",
    "request",
  ],
  [
    "access-reviews",
    "/iam/access-reviews",
    "accessReviews",
    "/iam/access-reviews",
    "review",
  ],
  ["menus", "/iam/menus", "menuManagement", "/iam/menus", "menu"],
  [
    "oauth-clients",
    "/iam/oauth-clients",
    "oauthClients",
    "/iam/oauth-clients",
    "client",
  ],
  [
    "scim-directories",
    "/iam/scim-directories",
    "scimDirectories",
    "/iam/scim/directories",
    "directory",
  ],
  [
    "audit-events",
    "/iam/audit-events",
    "auditLog",
    "/iam/audit-events",
    "event",
  ],
  [
    "audit-governance",
    "/iam/audit-governance",
    "auditGovernance",
    "/iam/audit/governance",
    "governance",
  ],
  ...["overview", "roles", "resources", "manage"].map((view) => [
    `entity-${view}`,
    `/iam/entities/:entityID/${view}`,
    {
      overview: "entityOverview",
      roles: "entityRoles",
      resources: "entityResources",
      manage: "entitySettings",
    }[view],
    view === "manage"
      ? "/iam/entities"
      : view === "resources"
        ? "/iam/relationships"
        : view === "roles"
          ? "/iam/entities/:entityID/role-bindings"
          : "/iam/entities/:entityID",
    view === "roles" ? "role" : view === "resources" ? "resource" : "entity",
  ]),
].map(([id, path, title, api, fixture]) => ({ id, path, title, api, fixture }))

// Controls that exist only for a caller who holds the matching write verb. The
// reader persona is granted every tenant view permission and nothing else, so
// each of these must be absent for it and present for the administrator -- a
// guard bound to the wrong permission code fails one side or the other.
const writeControls = {
  users: ["createPrincipal"],
  departments: ["createDepartment"],
  positions: ["createPosition"],
  groups: ["createGroup"],
  menus: ["createMenu"],
  roles: ["createRole"],
  invitations: ["createInvitation"],
  "oauth-clients": ["createClient"],
  "service-accounts": ["createServiceAccount"],
  "access-reviews": ["newReview"],
  "audit-events": ["exportResults"],
  "audit-governance": ["updateRetentionPolicy"],
  entities: ["createEntity"],
}

const personalAndPublic = [
  "/account/profile",
  "/account/security",
  "/account/mfa",
  "/account/applications",
  "/login",
  "/register",
  "/recover",
  "/verify-email",
  "/accept-invitation",
  "/security",
]
if (process.argv.includes("--list")) {
  console.log(
    JSON.stringify(
      {
        business: routes,
        personalAndPublic,
        locales: ["en-US", "zh-CN", "ms-MY"],
        widths: [1440, 390],
        personas: ["admin", "reader", "denied"],
        tenants: 2,
      },
      null,
      2
    )
  )
  process.exit(0)
}
const directory = new URL(
  "../../../../backend-api/.temp/iam-uat/",
  import.meta.url
)
const settings = JSON.parse(
  readFileSync(new URL("state.json", directory), "utf8")
)
const baseURL = settings.webURL
assert.equal(
  new URL(baseURL).hostname,
  "127.0.0.1",
  "Only isolated local UAT is permitted"
)
const output = new URL("browser/route-matrix/", directory)
mkdirSync(output, { recursive: true, mode: 0o700 })
const filter = process.env.IAM_MATRIX_ROUTES?.split(",")
if (filter)
  for (const id of filter)
    assert(
      routes.some((route) => route.id === id),
      `Unknown route ${id}`
    )
const selectedRoutes = routes.filter(
  (route) => !filter || filter.includes(route.id)
)
const locales = process.env.IAM_MATRIX_LOCALES?.split(",") ?? [
  "en-US",
  "zh-CN",
  "ms-MY",
]
const widths = process.env.IAM_MATRIX_WIDTHS?.split(",").map(Number) ?? [
  1440, 390,
]
const personas = process.env.IAM_MATRIX_PERSONAS?.split(",") ?? [
  "admin",
  "reader",
  "denied",
]
for (const locale of locales)
  assert(["en-US", "zh-CN", "ms-MY"].includes(locale))
for (const width of widths) assert([1440, 390].includes(width))
for (const persona of personas)
  assert(["admin", "reader", "denied"].includes(persona))
const report = {
  at: new Date().toISOString(),
  status: "RUNNING",
  routes,
  selectedRoutes: selectedRoutes.map((r) => r.id),
  locales,
  widths,
  personas,
  results: [],
  cleanup: [],
  scope:
    "Real populated business routes; tenant ownership, permission denial, three languages and desktop/mobile. Detailed mutation workflows are covered by the existing specialist audits.",
}
const save = () =>
  writeFileSync(
    new URL("report.json", output),
    JSON.stringify(report, null, 2) + "\n",
    { mode: 0o600 }
  )
const browser = await chromium.launch({
  channel: process.env.IAM_BROWSER_CHANNEL ?? "chrome",
  headless: true,
})
const admin = await browser.newContext({ baseURL })
const adminPassword = readFileSync(
  new URL("admin-password", directory),
  "utf8"
).trim()
const fixturePassword = randomBytes(32).toString("base64url")
const stamp = `matrix-${Date.now()}`
const fixtures = []
const actors = {}
async function call(context, path, method = "GET", data, tenant) {
  return context.request.fetch("/api" + path, {
    method,
    ...(data === undefined ? {} : { data }),
    headers: { Origin: baseURL, ...(tenant ? { "X-Tenant-Id": tenant } : {}) },
  })
}
async function ok(context, path, method = "GET", data, tenant) {
  const response = await call(context, path, method, data, tenant)
  assert(response.ok(), `${method} ${path}: HTTP ${response.status()}`)
  return (await response.json()).data
}
async function signIn(context, persona) {
  const login =
    persona === "admin" ? "admin@example.test" : actors[persona].login_name
  await ok(context, "/authn/sessions", "POST", {
    login_name: login,
    password: persona === "admin" ? adminPassword : fixturePassword,
    return_url: baseURL + "/",
  })
  return ok(context, "/authn/sessions/current")
}
async function setup() {
  const me = await signIn(admin, "admin")
  for (const suffix of ["a", "b"]) {
    const tenant = await ok(admin, "/iam/tenants", "POST", {
      slug: `${stamp}-${suffix}`,
      name: `${stamp}-${suffix}`,
      owner_principal_id: me.subject,
    })
    const f = { tenant, marker: `${stamp}-${suffix}`, labels: {}, requests: {} }
    fixtures.push(f)
    const create = async (key, path, body) => {
      const result = await ok(admin, path, "POST", body, tenant.id)
      f[key] = result
      f.requests[key] = { path, body }
      f.labels[key] = body.name ?? body.display_name ?? body.label ?? body.email
      return result
    }
    for (const persona of ["reader", "denied"]) {
      if (!actors[persona])
        actors[persona] = await create(persona, "/iam/principals", {
          login_name: `${stamp}-${persona}`,
          password: fixturePassword,
          display_name: `${stamp}-${persona}`,
        })
      else
        await ok(
          admin,
          "/iam/members",
          "POST",
          { subject: actors[persona].id, display_name: `${stamp}-${persona}` },
          tenant.id
        )
    }
    const readerRole = await create("readerRole", "/iam/roles", {
      name: `${f.marker}-reader`,
      description: "Route matrix read-only persona",
    })
    const catalog = await ok(
      admin,
      "/iam/permission-catalog",
      "GET",
      undefined,
      tenant.id
    )
    const readPermissions = catalog.filter(
      (permission) =>
        permission.verb === "view" && permission.scope === "tenant"
    )
    assert(
      readPermissions.length > 10,
      "Read-only permission fixture must be populated"
    )
    for (const permission of readPermissions)
      await ok(
        admin,
        `/iam/roles/${readerRole.id}/permissions/${permission.code}`,
        "PUT",
        {},
        tenant.id
      )
    await ok(
      admin,
      `/iam/roles/${readerRole.id}/members/${actors.reader.id}`,
      "PUT",
      {},
      tenant.id
    )
    await create("principal", "/iam/principals", {
      login_name: `${f.marker}-person`,
      password: fixturePassword,
      display_name: `${f.marker}-person`,
    })
    await create("department", "/iam/departments", {
      name: `${f.marker}-department`,
      status: "active",
      sort_order: 1,
    })
    await create("position", "/iam/positions", {
      name: `${f.marker}-position`,
      code: `${f.marker}-position`,
      status: "active",
      sort_order: 1,
    })
    await create("group", "/iam/groups", {
      name: `${f.marker}-group`,
      type: "static",
      status: "active",
      sort_order: 1,
    })
    await create("role", "/iam/roles", {
      name: `${f.marker}-role`,
      description: "Explicit entity and resource source",
    })
    await create("entity", "/iam/entities", {
      name: `${f.marker}-entity`,
      type: "unit",
      status: "active",
      metadata: { audit: f.marker },
    })
    await create("otherEntity", "/iam/entities", {
      name: `${f.marker}-other-entity`,
      type: "unit",
      parent_id: f.entity.id,
      status: "active",
      metadata: {},
    })
    await ok(
      admin,
      `/iam/entities/${f.entity.id}/role-bindings/${f.role.id}/${f.principal.id}`,
      "PUT",
      { effect: "allow" },
      tenant.id
    )
    await create("resource", "/iam/relationships", {
      entity_id: f.entity.id,
      subject_type: "principal",
      subject_id: f.principal.id,
      relation: "viewer",
      resource_type: "store",
      resource_id: f.entity.id,
    })
    f.labels.resource = f.entity.id
    await create("menu", "/iam/menus", {
      label: `${f.marker}-menu`,
      route: `/matrix/${suffix}`,
      sort_order: 1,
      status: "active",
      translations: {
        "en-US": `${f.marker}-menu`,
        "zh-CN": `${f.marker}-menu`,
        "ms-MY": `${f.marker}-menu`,
      },
    })
    await create("machine", "/iam/service-accounts", {
      login_name: `${f.marker}-machine`,
      display_name: `${f.marker}-machine`,
    })
    await create("directory", "/iam/scim/directories", {
      name: `${f.marker}-directory`,
    })
    await create("client", "/iam/oauth-clients", {
      name: `${f.marker}-client`,
      grant_types: ["client_credentials"],
      scopes: ["openid"],
      redirect_uris: [],
      public_client: false,
    })
    await create("invitation", "/iam/invitations", {
      email: `${f.marker}@example.test`,
      role_ids: [],
      expires_in_hours: 24,
    })
    await create("review", "/iam/access-reviews", {
      name: `${f.marker}-review`,
      due_at: new Date(Date.now() + 86400000).toISOString(),
    })
    await ok(
      admin,
      "/iam/field-policies/user_profile/display_name",
      "PUT",
      {
        version: 0,
        policy: {
          read: { default: "allow", allow_roles: [], deny_roles: [] },
          write: { default: "allow", allow_roles: [], deny_roles: [] },
          mask: "omit",
        },
      },
      tenant.id
    )
    f.labels.field = "display_name"
    f.labels.tenant = tenant.name
    f.labels.event = f.principal.id
    for (const persona of ["admin", "reader", "denied"]) {
      const context = await browser.newContext({ baseURL })
      try {
        await signIn(context, persona)
        await ok(
          context,
          "/iam/access-requests",
          "POST",
          {
            role_id: f.role.id,
            reason: `${f.marker}-${persona}-request`,
            access_expires_at: new Date(Date.now() + 3600000).toISOString(),
          },
          tenant.id
        )
      } finally {
        await context.close()
      }
    }
    f.labels.request = f.labels.role
  }
  assert.equal(fixtures.length, 2)
  report.fixtureTenants = fixtures.map((f) => ({
    id: f.tenant.id,
    name: f.tenant.name,
  }))
  report.binarySHA256 = createHash("sha256")
    .update(readFileSync(new URL("api", directory)))
    .digest("hex")
}
async function cell(
  context,
  page,
  f,
  other,
  route,
  persona,
  locale,
  width,
  messages
) {
  const path = route.path.replace(":entityID", f.entity.id)
  // The workspace shell rejects inaccessible entity catalogs before mounting
  // the nested view, so its child requests must never be expected on denial.
  const api =
    persona === "denied" && route.id.startsWith("entity-")
      ? "/iam/entities"
      : route.api.replace(":entityID", f.entity.id)
  const denied =
    persona !== "admin" &&
    (["tenants", "scim-directories"].includes(route.id) ||
      (persona === "denied" &&
        !["dashboard", "access-requests"].includes(route.id)))
  const result = {
    route: route.id,
    path,
    locale,
    width,
    persona,
    tenant: f.tenant.id,
    expectation: denied ? "denied" : "populated",
    status: "RUNNING",
  }
  report.results.push(result)
  const errors = []
  const onError = (e) => errors.push(e.message)
  page.on("pageerror", onError)
  try {
    const responsePromise = page.waitForResponse(
      (r) =>
        new URL(r.url()).pathname === "/api" + api &&
        r.request().method() === "GET",
      { timeout: 15000 }
    )
    await page.goto(path)
    const response = await responsePromise
    result.httpStatus = response.status()
    assert.equal(
      response.status(),
      denied ? 403 : 200,
      `${route.id} ${persona} read status`
    )
    if (denied) {
      await page
        .getByRole("alert")
        .filter({ hasText: messages.forbidden })
        .first()
        .waitFor()
      assert.equal(
        await page
          .locator("main")
          .getByText(f.labels[route.fixture] ?? f.marker, { exact: false })
          .count(),
        0,
        "Denied view must not reveal fixture data"
      )
      for (const key of [
        "noDepartments",
        "noPositions",
        "noGroups",
        "noMenus",
        "noInvitations",
        "noReviews",
        "noServiceAccounts",
        "noScimDirectories",
        "noAuditEvents",
        "noPrincipals",
        "noTenants",
        "noOAuthClients",
      ])
        assert.equal(
          await page.getByText(messages[key], { exact: true }).count(),
          0,
          `Failure must not show ${key}`
        )
    } else {
      assert(messages[route.title], `Missing heading key ${route.title}`)
      await page
        .getByRole("heading", { name: messages[route.title], exact: true })
        .first()
        .waitFor()
      for (const key of writeControls[route.id] ?? []) {
        assert(messages[key], `Missing write control key ${key}`)
        // Exact and visible: a shorter label is a substring of a longer one in
        // some locales, and a closed dialog keeps its own submit button in the
        // DOM, so a loose match answers about the wrong control.
        const control = page
          .getByRole("button", { name: messages[key], exact: true })
          .filter({ visible: true })
        if (persona === "admin") await control.first().waitFor()
        assert.equal(
          (await control.count()) > 0,
          persona === "admin",
          `${route.id} ${persona} must ${persona === "admin" ? "" : "not "}be offered ${key}`
        )
      }
      if (route.fixture && !["event", "governance"].includes(route.fixture)) {
        assert(f.labels[route.fixture], `${route.id} has no populated fixture`)
        await page
          .locator("main")
          .getByText(f.labels[route.fixture], { exact: false })
          .filter({ visible: true })
          .first()
          .waitFor()
      }
      if (route.fixture === "event")
        assert(
          (await response.json()).data.length > 0,
          "Audit events must contain real records"
        )
      if (route.fixture === "governance") {
        const data = (await response.json()).data
        assert(
          data.total_events > 0,
          "Governance must describe nonempty audit data"
        )
        await page.getByText(messages.totalEvents, { exact: true }).waitFor()
      }
      if (route.id !== "tenants")
        assert.equal(
          await page
            .locator("main")
            .getByText(other.marker, { exact: false })
            .count(),
          0,
          "Other tenant fixture leaked"
        )
      if (route.id.startsWith("entity-"))
        assert.equal(
          await page
            .locator("tbody")
            .getByText(f.labels.otherEntity, { exact: true })
            .count(),
          0,
          "Sibling/child entity leaked into scoped view"
        )
      if (persona === "reader" && f.requests[route.fixture]) {
        const mutation = f.requests[route.fixture]
        const deniedWrite = await call(
          context,
          mutation.path,
          "POST",
          mutation.body,
          f.tenant.id
        )
        assert.equal(
          deniedWrite.status(),
          403,
          `${route.id} reader write must be denied`
        )
      }
    }
    assert.deepEqual(errors, [], "No uncaught page errors")
    assert(
      await page.evaluate(
        () => document.documentElement.scrollWidth <= innerWidth
      ),
      "No horizontal page overflow"
    )
    const screenshot = `${route.id}-${locale}-${width}-${persona}-${f.tenant.id}.png`
    await page.screenshot({
      // fileURLToPath, not .pathname: on Windows the latter yields "/D:/..."
      // and the screenshot write then fails with a doubled drive letter.
      path: fileURLToPath(new URL(screenshot, output)),
      fullPage: true,
      animations: "disabled",
    })
    result.screenshot = screenshot
    result.status = "PASS"
  } catch (error) {
    result.status = "FAIL"
    result.error = error.message
    console.error(
      `FAIL ${route.id} ${locale} ${width} ${persona}: ${error.message}`
    )
  } finally {
    page.off("pageerror", onError)
    save()
  }
}
try {
  save()
  await setup()
  for (const locale of locales)
    for (const width of widths)
      for (const persona of personas)
        for (const f of fixtures) {
          const messages = JSON.parse(
            readFileSync(
              new URL(`../src/messages/${locale}.json`, import.meta.url),
              "utf8"
            )
          )
          const context = await browser.newContext({
            baseURL,
            viewport: { width, height: 900 },
          })
          try {
            const session = await signIn(context, persona)
            await context.addCookies([
              { name: "NEXT_LOCALE", value: locale, url: baseURL },
            ])
            await context.addInitScript(
              ({ tenant, subject }) => {
                localStorage.setItem("iam.tenant", tenant)
                localStorage.setItem("iam.tenant:subject", subject)
              },
              { tenant: f.tenant.id, subject: session.subject }
            )
            const page = await context.newPage()
            page.setDefaultTimeout(15000)
            for (const route of selectedRoutes)
              await cell(
                context,
                page,
                f,
                fixtures.find((other) => other !== f),
                route,
                persona,
                locale,
                width,
                messages
              )
          } finally {
            await context.close()
          }
        }
  const expected =
    selectedRoutes.length * locales.length * widths.length * personas.length * 2
  assert.equal(
    report.results.length,
    expected,
    "No missing or silently skipped cells"
  )
  report.status = report.results.every((r) => r.status === "PASS")
    ? "PASS"
    : "FAIL"
} catch (error) {
  report.status = "FAIL"
  report.error = error.message
  console.error(error.message)
} finally {
  for (const f of fixtures) {
    try {
      for (const principal of [
        f.principal,
        ...(f === fixtures[0] ? Object.values(actors) : []),
      ].filter(Boolean))
        await ok(
          admin,
          `/iam/principals/${principal.id}/suspension`,
          "PUT",
          undefined,
          f.tenant.id
        )
      const tenants = await ok(admin, "/iam/tenants")
      const current = tenants.find((tenant) => tenant.id === f.tenant.id)
      assert(current, "Own fixture tenant must exist for cleanup")
      await ok(
        admin,
        `/iam/tenants/${current.id}?version=${current.version}`,
        "DELETE"
      )
      report.cleanup.push({
        tenant: current.id,
        status: "PASS",
        principals:
          "Synthetic global principals disabled; tenant fixtures deleted",
      })
    } catch (error) {
      report.cleanup.push({
        tenant: f.tenant.id,
        status: "FAIL",
        error: error.message,
      })
      report.status = "FAIL"
    }
  }
  save()
  await browser.close()
}
console.log(
  JSON.stringify({
    status: report.status,
    cells: report.results.length,
    report: new URL("report.json", output).pathname,
  })
)
if (report.status !== "PASS") process.exitCode = 1
