import assert from "node:assert/strict"
import { spawn, spawnSync } from "node:child_process"
import { randomBytes } from "node:crypto"
import { createWriteStream, readFileSync } from "node:fs"
import { mkdir, mkdtemp, writeFile } from "node:fs/promises"
import { createServer } from "node:net"
import { createServer as createHTTPServer } from "node:http"
import { join } from "node:path"
import { fileURLToPath } from "node:url"
import { chromium } from "playwright"
import { externalIdentityAcceptance } from "./external-identity-acceptance.mjs"
import { administrationAccountViewAcceptance } from "./administration-account-view-acceptance.mjs"
import { authEntrancesAcceptance } from "./auth-entrances-acceptance.mjs"

const backend = fileURLToPath(
  new URL("../../../../backend-api/", import.meta.url)
)
const frontend = fileURLToPath(new URL("../", import.meta.url))
const artifactRoot = join(backend, ".temp")
await mkdir(artifactRoot, { recursive: true })
const directory = await mkdtemp(join(artifactRoot, "sqlite-lifecycle-"))
const results = []
const children = []
let browser
const notifications = []
const notificationServer = createHTTPServer(async (request, response) => {
  try {
    let body = ""
    for await (const chunk of request) body += chunk
    notifications.push(JSON.parse(body))
    response.writeHead(204).end()
  } catch {
    response.writeHead(400).end()
  }
})
await new Promise((resolve) =>
  notificationServer.listen(0, "127.0.0.1", resolve)
)
const notificationURL = `http://127.0.0.1:${notificationServer.address().port}`

async function port() {
  const server = createServer()
  await new Promise((resolve, reject) => {
    server.once("error", reject)
    server.listen(0, "127.0.0.1", resolve)
  })
  const value = server.address().port
  await new Promise((resolve) => server.close(resolve))
  return value
}

async function eventually(check, label, timeout = 25_000) {
  const deadline = Date.now() + timeout
  while (Date.now() < deadline) {
    if (await check()) return
    await new Promise((resolve) => setTimeout(resolve, 100))
  }
  throw new Error(`Timed out: ${label}`)
}

function launch(command, args, cwd, name, env = {}) {
  const log = createWriteStream(join(directory, `${name}.log`), { mode: 0o600 })
  const child = spawn(command, args, {
    cwd,
    env: { ...process.env, ...env },
    detached: process.platform !== "win32",
    windowsHide: true,
    stdio: ["ignore", "pipe", "pipe"],
  })
  child.stdout.pipe(log, { end: false })
  child.stderr.pipe(log, { end: false })
  child.once("close", () => log.end())
  children.push(child)
  return child
}

// The services here spawn children of their own, so the whole tree has to go.
// On POSIX that is what the negative pid asks for. Windows has no process
// group for it to address and answers ESRCH, which is why every run used to
// leave its api.exe behind -- and why a piped run produced no output at all,
// since the survivor kept the pipe open.
function terminate(child, force) {
  if (process.platform === "win32") {
    const args = ["/pid", String(child.pid), "/T"]
    if (force) args.push("/F")
    spawnSync("taskkill", args, { stdio: "ignore", windowsHide: true })
    return
  }
  try {
    process.kill(-child.pid, force ? "SIGKILL" : "SIGTERM")
  } catch (error) {
    if (error.code !== "ESRCH") throw error
  }
}

async function run(command, args, cwd, name, expectedStatus = 0) {
  const child = launch(command, args, cwd, name)
  const status = await new Promise((resolve, reject) => {
    child.once("error", reject)
    child.once("exit", resolve)
  })
  assert.equal(status, expectedStatus, `${name} failed; inspect ${name}.log`)
}

function pass(flow) {
  results.push({ flow, status: "PASS" })
  console.log(`PASS ${flow}`)
}

const apiURL = `http://127.0.0.1:${await port()}`
const webPort = await port()
const webURL = `http://127.0.0.1:${webPort}`
// go build writes api.exe on Windows, and spawning "api" there fails with
// ENOENT before a single assertion runs.
const binary = join(directory, process.platform === "win32" ? "api.exe" : "api")
const configPath = join(directory, "config.json")
const adminPassword = randomBytes(24).toString("base64url") + "aA1!"
const accountPassword = randomBytes(24).toString("base64url") + "bB2!"
const adminLogin = "admin@example.test"
const pageErrors = []
let tenant

// Console pages live under the tenant they belong to. Spelling that out at
// every navigation is how these drifted: the routes moved and the bare paths
// kept matching nothing, so the pages never rendered and the failures read as
// missing buttons.
const tenantPage = (suffix) => `/admin/${tenant.id}${suffix}`

async function client(page, method, ...args) {
  return page.evaluate(
    async ({ method, args }) => {
      const { iamApi } = await import("/src/lib/iam-api.ts")
      return iamApi[method](...args)
    },
    { method, args }
  )
}

// The published contract decides whether a resource is addressed under its
// tenant or globally. Guessing from the first path segment is what let these
// calls rot: resources moved to global addresses and the guess kept building
// a tenant-scoped URL that answered 404 somewhere deep in a flow.
const contract = JSON.parse(
  readFileSync(join(backend, "docs", "openapi.json"), "utf8")
)
const contractPaths = Object.keys(contract.paths)
assert(
  !Object.keys(contract.components.schemas).some((name) =>
    /position/i.test(name)
  )
)
assert(!JSON.stringify(contract.components.schemas).includes('"position"'))

function published(candidate) {
  const parts = candidate.split("?")[0].split("/")
  return contractPaths.some((template) => {
    const shape = template.split("/")
    return (
      shape.length === parts.length &&
      shape.every(
        (segment, index) => segment.startsWith("{") || segment === parts[index]
      )
    )
  })
}

async function call(context, path, method = "GET", body, options = {}) {
  if (path.includes("{principal_id}")) {
    const identity = await context.request.get("/api/me")
    const subject = identity.ok() ? (await identity.json()).data.subject : "1"
    path = path.replace("{principal_id}", subject)
  }
  const tenantID = options.tenant ?? tenant?.id
  let requestPath = path
  if (options.entity) {
    const scoped = `/tenants/${tenantID}/entities/${options.entity}${path}`
    const global = `/entities/${options.entity}${path}`
    requestPath = published(scoped) ? scoped : global
  } else if (tenantID && options.tenant !== false) {
    const scoped = `/tenants/${tenantID}${path}`
    requestPath = published(scoped) ? scoped : path
  }
  assert(
    published(requestPath),
    `${method} ${requestPath} is not in the published contract`
  )
  const response = await context.request.fetch(`/api${requestPath}`, {
    method,
    headers: { Origin: webURL },
    ...(body === undefined ? {} : { data: body }),
  })
  if (options.status) {
    assert.equal(response.status(), options.status, `${method} ${path}`)
    return
  }
  assert(response.ok(), `${method} ${path}: HTTP ${response.status()}`)
  return (await response.json()).data
}

async function login(loginName, password, width = 1440, entry = "user") {
  const context = await browser.newContext({
    baseURL: webURL,
    viewport: { width, height: 1000 },
    locale: "en-US",
  })
  await context.addCookies([
    { name: "NEXT_LOCALE", value: "en-US", url: webURL },
  ])
  context.on("page", (page) =>
    page.on("pageerror", (error) => pageErrors.push(error.message))
  )
  const page = await context.newPage()
  await page.goto(`/${entry}/login`)
  await page.locator("#login-name").fill(loginName)
  await page.locator("#password").fill(password)
  await page.locator('form button[type="submit"]').click()
  await page.waitForURL((url) =>
    entry === "user"
      ? url.pathname === "/user/files"
      : url.pathname.startsWith(`/${entry}/`) &&
        !["login", "workspaces"].includes(url.pathname.split("/")[2])
  )
  return { context, page }
}

async function submit(page, form) {
  await page.locator(`button[form="${form}"][type="submit"]`).click()
  await page.locator(`#${form}`).waitFor({ state: "hidden" })
}

async function createDepartment(page, name, entity, parent) {
  await page.goto(tenantPage("/departments"))
  await page
    .getByRole("button", { name: "Create department", exact: true })
    .click()
  // The console keeps this form's submit disabled until an entity is chosen,
  // and choosing one resets the parent, so it goes first.
  await page.locator("#department-entity").click()
  await page.getByRole("option", { name: entity.name, exact: true }).click()
  await page.locator("#department-name").fill(name)
  if (parent) {
    await page.locator("#department-parent").click()
    await page.getByRole("option", { name: parent.name, exact: true }).click()
  }
  await submit(page, "department-form")
  const item = (await client(page, "departments")).find(
    (item) => item.name === name
  )
  assert(item)
  return item
}

async function createEntity(page, name, parent) {
  await page.goto(tenantPage("/entities"))
  await page.getByRole("button", { name: "Create entity", exact: true }).click()
  await page.locator("#entity-name").fill(name)
  await page.locator("#entity-type").fill("unit")
  if (parent) {
    await page.locator("#entity-parent").click()
    await page.getByRole("option", { name: parent.name, exact: true }).click()
  }
  await submit(page, "entity-form")
  const item = (await client(page, "entities")).find(
    (item) => item.name === name
  )
  assert(item)
  assert.equal(item.parent_id ?? "", parent?.id ?? "")
  return item
}

async function createAccount(page, name, department) {
  await page.goto(tenantPage("/users"))
  await page
    .getByRole("button", { name: "Create account", exact: true })
    .click()
  await page
    .locator('#principal-form [name="login_name"]')
    .fill(`${name.toLowerCase()}@example.test`)
  await page.locator('#principal-form [name="display_name"]').fill(name)
  await page
    .locator('#principal-form [name="email"]')
    .fill(`${name.toLowerCase()}@example.test`)
  await page.locator('#principal-form [name="password"]').fill(accountPassword)
  await submit(page, "principal-form")
  const item = (await client(page, "principals")).items.find(
    (item) => item.display_name === name
  )
  assert(item)
  await client(page, "updateMember", item.id, { department_id: department.id })
  return item
}

async function createRole(page, name, codes, members = []) {
  await page.goto(tenantPage("/roles"))
  await page.getByRole("button", { name: "Create role", exact: true }).click()
  await page.locator("#role-name").fill(name)
  await submit(page, "role-form")
  const role = (await client(page, "roles")).find((item) => item.name === name)
  assert(role)
  for (const code of codes) {
    await page
      .locator(`[data-permission-code="${code}"] [role="checkbox"]`)
      .click()
    await eventually(
      async () =>
        (await client(page, "rolePermissionGrants", role.id)).some(
          (grant) => grant.permission_code === code
        ),
      `grant ${code}`
    )
  }
  if (members.length) {
    await page.locator('[data-role-view="assignments"]').click()
    for (const member of members) {
      await page.locator(`[data-role-member-id="${member.id}"]`).click()
      await eventually(
        async () =>
          (await client(page, "roleMembers", role.id)).includes(member.id),
        "role membership"
      )
    }
  }
  return role
}

async function capture(page, name, width) {
  await page.setViewportSize({ width, height: width < 600 ? 844 : 1000 })
  if (width < 600) {
    await page
      .getByRole("button", { name: "Open main navigation", exact: true })
      .click()
    await page.getByRole("dialog").waitFor()
  }
  await page.screenshot({
    path: join(directory, `${name}-${width}.png`),
    fullPage: true,
  })
  assert(
    await page.evaluate(
      () => document.documentElement.scrollWidth <= innerWidth
    ),
    `${name}: horizontal overflow`
  )
  if (width < 600) await page.keyboard.press("Escape")
}

try {
  for (const name of [
    "signing-key",
    "mfa-key",
    "provisioning-key",
    "federation-key",
  ]) {
    await writeFile(join(directory, name), randomBytes(32).toString("base64"), {
      mode: 0o600,
    })
  }
  await writeFile(join(directory, "admin-password"), adminPassword, {
    mode: 0o600,
  })
  await writeFile(
    configPath,
    JSON.stringify(
      {
        name: "IAM Lifecycle Acceptance",
        table_prefix: "e2e_",
        http_server: {
          address: "127.0.0.1",
          port: Number(new URL(apiURL).port),
        },
        grpc_server: { address: "127.0.0.1", port: await port() },
        database: {
          main: {
            type: "sqlite",
            dsn: join(directory, "lifecycle.sqlite"),
            writable: true,
            readable: true,
          },
        },
        iam: { tenant_registration_enabled: true },
        authn: {
          enabled: true,
          issuer: apiURL,
          signing_key_file: join(directory, "signing-key"),
          mfa: { encryption_key_file: join(directory, "mfa-key") },
          registration: { enabled: true, captcha: { test_answer: "12345678" } },
          notification: { url: notificationURL, poll_interval: "100ms" },
          email_verification: {
            enabled: true,
            verify_url: webURL + "/user/verify-email",
          },
          recovery: { enabled: true, reset_url: webURL + "/user/recover" },
          web: {
            enabled: true,
            cookie_name: "lifecycle_session",
            cookie_secure: false,
            post_login_url: webURL + "/",
            post_logout_url: webURL + "/user/login",
            allowed_return_urls: [webURL + "/"],
            allowed_origins: [webURL],
          },
        },
        provisioning: {
          encryption_key_file: join(directory, "provisioning-key"),
          outbound_allow_plaintext: true,
          outbound_allow_networks: ["127.0.0.1/32"],
        },
        federation: {
          enabled: true,
          encryption_key_file: join(directory, "federation-key"),
          outbound_allow_plaintext: true,
          outbound_allow_networks: ["127.0.0.1/32"],
          saml: { enabled: true },
        },
      },
      null,
      2
    ),
    { mode: 0o600 }
  )
  console.log(`SQLite acceptance artifacts: ${directory}`)
  await run("go", ["build", "-o", binary, "./cmd/7link-api"], backend, "build")
  await run(
    binary,
    ["migrate", "auto", "--config", configPath],
    backend,
    "migration"
  )
  const bootstrap = [
    "iam",
    "bootstrap-platform-admin",
    "--config",
    configPath,
    "--login",
    adminLogin,
    "--display-name",
    "Lifecycle Administrator",
    "--password-file",
    join(directory, "admin-password"),
  ]
  await run(binary, bootstrap, backend, "bootstrap")
  await run(binary, bootstrap, backend, "bootstrap-rerun", 3)
  const inventory = spawnSync(
    binary,
    ["iam", "external-identity-inventory", "--config", configPath],
    { cwd: backend, encoding: "utf8", windowsHide: true }
  )
  assert.equal(inventory.status, 0, "empty legacy inventory release gate")
  assert.deepEqual(
    JSON.parse(inventory.stdout),
    [],
    "inventory stdout must contain only JSON"
  )
  pass("SQLite migrations and create-only platform bootstrap")
  const apiProcess = launch(
    binary,
    ["serve", "--config", configPath],
    backend,
    "api"
  )
  await eventually(
    async () => {
      assert.equal(apiProcess.exitCode, null, "API exited during startup")
      return fetch(apiURL + "/readyz")
        .then((response) => response.ok)
        .catch(() => false)
    },
    "API readiness",
    60_000
  )
  const webProcess = launch(
    "bun",
    [
      "run",
      "dev",
      "--host",
      "127.0.0.1",
      "--port",
      String(webPort),
      "--strictPort",
    ],
    frontend,
    "web",
    { VITE_API_PROXY_TARGET: apiURL }
  )
  await eventually(
    async () => {
      assert.equal(webProcess.exitCode, null, "Web exited during startup")
      return fetch(webURL)
        .then((response) => response.ok)
        .catch(() => false)
    },
    "web readiness",
    60_000
  )
  browser = await chromium.launch({
    channel: process.env.IAM_BROWSER_CHANNEL ?? "chrome",
    headless: true,
  })
  const anonymous = await browser.newContext({ baseURL: webURL })
  await call(
    anonymous,
    "/principals/{principal_id}/permissions",
    "GET",
    undefined,
    {
      tenant: false,
      status: 401,
    }
  )
  await anonymous.close()
  const admin = await login(adminLogin, adminPassword)
  if (process.env.IAM_AUTH_ENTRANCES_ONLY === "1") {
    await authEntrancesAcceptance({ admin, browser, webURL, directory, notifications, eventually, pass, adminLogin, adminPassword })
  } else {
  assert.deepEqual(
    await call(admin.context, "/tenants", "GET", undefined, {
      tenant: false,
    }),
    []
  )
  const platformCodes = await call(
    admin.context,
    "/principals/{principal_id}/permissions",
    "GET",
    undefined,
    { tenant: false }
  )
  assert(platformCodes.includes("tenant_create"))
  assert(!platformCodes.includes("user_create"))
  await admin.page.goto("/system/tenants")
  await admin.page
    .getByRole("button", { name: "Create tenant", exact: true })
    .click()
  await admin.page.locator("#tenant-name").fill("First Business")
  await admin.page.locator("#tenant-slug").fill("first-business")
  await submit(admin.page, "tenant-form")
  tenant = (
    await call(admin.context, "/tenants", "GET", undefined, {
      tenant: false,
    })
  )[0]
  assert.equal(tenant.name, "First Business")
  await admin.page
    .getByRole("button", { name: "Open First Business", exact: true })
    .click()
  const [firstWorkspace] = await Promise.all([
    admin.context.waitForEvent("page"),
    admin.page
      .getByRole("dialog")
      .getByRole("button", { name: "Open view", exact: true })
      .click(),
  ])
  await firstWorkspace.waitForURL(
    (url) => url.pathname === `/admin/${tenant.id}`
  )
  await firstWorkspace.close()
  await admin.page.goto(`/admin/${tenant.id}`)
  await eventually(
    async () =>
      (await admin.page.evaluate(() => sessionStorage.getItem("iam.tenant"))) ===
      tenant.id,
    "first tenant adoption"
  )
  const defaultMenus = await call(admin.context, "/menus")
  assert.equal(defaultMenus.length, 14)
  assert(!JSON.stringify(defaultMenus).includes("position"))
  const catalog = await client(admin.page, "permissions")
  assert(!catalog.some((permission) => permission.code.startsWith("position_")))
  const administrator = (await call(admin.context, "/roles")).find(
    (role) => role.name === "Administrator"
  )
  assert(administrator)
  await call(
    admin.context,
    `/roles/${administrator.id}/directory-bindings/position/1`,
    "PUT",
    {},
    { status: 422 }
  )
  assert.deepEqual(
    (
      await call(admin.context, `/roles/${administrator.id}/permission-grants`)
    ).map((grant) => grant.permission_code),
    ["tenant_administer"]
  )
  await capture(admin.page, "first-tenant", 1440)
  pass(
    "browser creates the first real tenant without a pre-existing membership; owner, role and menus initialized"
  )
  if (process.env.IAM_ACCOUNT_VIEW_ONLY !== "1") {
  await externalIdentityAcceptance({
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
  })

  // Entities first: a department is created against one.
  const company = await createEntity(admin.page, "Company")
  await call(
    admin.context,
    "/authorization-relationships",
    "POST",
    {
      subject_type: "position",
      subject_id: "1",
      subject_relation: "member",
      relation: "viewer",
      resource_type: "company",
      resource_id: company.id,
    },
    { status: 422 }
  )
  const east = await createEntity(admin.page, "East Branch", company)
  const west = await createEntity(admin.page, "West Branch", company)
  const operations = await createDepartment(admin.page, "Operations", company)
  const operationsChild = await createDepartment(
    admin.page,
    "Branch operations",
    company,
    operations
  )
  const finance = await createDepartment(admin.page, "Finance", company)
  const alice = await createAccount(admin.page, "Alice", operations)
  const bob = await createAccount(admin.page, "Bob", finance)
  const charlie = await createAccount(admin.page, "Charlie", operationsChild)
  const emailAccount = await login(alice.login_name, accountPassword)
  assert.equal((await call(emailAccount.context, "/me")).email_verified, false)
  assert.equal(
    (await client(emailAccount.page, "startEmailVerification")).accepted,
    true
  )
  await eventually(
    () =>
      notifications.some(
        (n) => n.type === "email_verification" && n.recipient === alice.email
      ),
    "real email verification notification"
  )
  const verification = notifications.find(
    (n) => n.type === "email_verification" && n.recipient === alice.email
  )
  const verificationToken = new URL(
    verification.verification_url
  ).searchParams.get("token")
  assert(verificationToken)
  assert.equal(
    (
      await client(
        emailAccount.page,
        "completeEmailVerification",
        verificationToken
      )
    ).verified,
    true
  )
  assert.equal((await call(emailAccount.context, "/me")).email_verified, true)
  await call(
    emailAccount.context,
    "/email-verifications",
    "POST",
    { token: verificationToken },
    { tenant: false, status: 400 }
  )
  await emailAccount.context.close()
  pass(
    "email verification request, real outbox webhook delivery, consumption and replay rejection"
  )
  const viewer = await createRole(
    admin.page,
    "Account viewers",
    ["user_view"],
    [alice]
  )
  await createRole(
    admin.page,
    "Account editors",
    ["user_view", "user_create", "user_update"],
    [bob]
  )
  const opsTasks = await createRole(
    admin.page,
    "Operations tasks",
    ["todo_view", "todo_create"],
    [alice]
  )
  const financeTasks = await createRole(
    admin.page,
    "Finance tasks",
    ["todo_view", "todo_create"],
    [bob]
  )
  for (const role of [opsTasks, financeTasks]) {
    await client(admin.page, "setRoleDataScope", role.id, {
      scope: "department",
      department_ids: [],
    })
  }
  const branchTasks = await createRole(admin.page, "Scoped branch tasks", [
    "todo_view",
    "todo_create",
  ])
  await client(
    admin.page,
    "putEntityRoleBinding",
    company.id,
    branchTasks.id,
    charlie.id,
    { effect: "allow" }
  )
  await client(
    admin.page,
    "putEntityRoleBinding",
    west.id,
    branchTasks.id,
    charlie.id,
    { effect: "deny" }
  )
  pass(
    "browser creates department/entity hierarchies, child accounts and distinct roles; assigns departments and scopes"
  )

  const aliceSession = await login(alice.login_name, accountPassword)
  const bobSession = await login(bob.login_name, accountPassword)
  const scopedSession = await login(charlie.login_name, accountPassword)
  const menuPaths = (menus) =>
    menus.flatMap((menu) => [menu.path, ...menuPaths(menu.children ?? [])])
  const aliceMenus = menuPaths(
    await call(aliceSession.context, "/principals/{principal_id}/menus")
  )
  assert(aliceMenus.includes("/users"))
  assert(!aliceMenus.includes("/roles"))
  await aliceSession.page.goto(tenantPage("/users"))
  await aliceSession.page.getByRole("heading", { level: 1 }).waitFor()
  await eventually(
    async () =>
      (await aliceSession.page
        .locator(`[data-member-department="${alice.id}"]`)
        .textContent()) === operations.id,
    "assigned department remains visible without department directory permission"
  )
  assert.equal(
    await aliceSession.page
      .getByRole("button", { name: "Create account", exact: true })
      .count(),
    0
  )
  await bobSession.page.goto(tenantPage("/users"))
  await bobSession.page
    .getByRole("button", { name: "Create account", exact: true })
    .waitFor()
  await call(
    aliceSession.context,
    "/principals",
    "POST",
    { login_name: "forbidden@example.test", password: accountPassword },
    { status: 403 }
  )
  await call(scopedSession.context, "/roles", "GET", undefined, {
    status: 403,
  })
  pass(
    "read-only and editor accounts have different real menus/actions; forbidden mutations stay denied"
  )

  const aliceTask = await call(
    aliceSession.context,
    "/todos",
    "POST",
    { title: "Operations task" },
    { entity: east.id }
  )
  const bobTask = await call(
    bobSession.context,
    "/todos",
    "POST",
    { title: "Finance task" },
    { entity: east.id }
  )
  const aliceRows = await call(
    aliceSession.context,
    "/todos",
    "GET",
    undefined,
    { entity: east.id }
  )
  const bobRows = await call(bobSession.context, "/todos", "GET", undefined, {
    entity: east.id,
  })
  assert(aliceRows.some((row) => row.id === aliceTask.id))
  assert(!aliceRows.some((row) => row.id === bobTask.id))
  assert(bobRows.some((row) => row.id === bobTask.id))
  assert(!bobRows.some((row) => row.id === aliceTask.id))
  await client(admin.page, "setRoleDataScope", opsTasks.id, {
    scope: "selected_departments",
    department_ids: [finance.id],
  })
  const reassignedRows = await call(
    aliceSession.context,
    "/todos",
    "GET",
    undefined,
    { entity: east.id }
  )
  assert(reassignedRows.some((row) => row.id === bobTask.id))
  assert(!reassignedRows.some((row) => row.id === aliceTask.id))
  await client(admin.page, "setRoleDataScope", opsTasks.id, {
    scope: "department",
    department_ids: [],
  })
  await call(scopedSession.context, "/todos", "GET", undefined, {
    entity: east.id,
  })
  const childTask = await call(
    scopedSession.context,
    "/todos",
    "POST",
    { title: "Branch operations task" },
    { entity: east.id }
  )
  assert(
    !(
      await call(aliceSession.context, "/todos", "GET", undefined, {
        entity: east.id,
      })
    ).some((row) => row.id === childTask.id)
  )
  await client(admin.page, "setRoleDataScope", opsTasks.id, {
    scope: "department_and_descendants",
    department_ids: [],
  })
  assert(
    (
      await call(aliceSession.context, "/todos", "GET", undefined, {
        entity: east.id,
      })
    ).some((row) => row.id === childTask.id)
  )
  await client(admin.page, "setRoleDataScope", opsTasks.id, {
    scope: "department",
    department_ids: [],
  })
  await call(scopedSession.context, "/todos", "GET", undefined, {
    entity: west.id,
    status: 403,
  })
  assert(
    !(
      await call(
        scopedSession.context,
        "/principals/{principal_id}/permissions"
      )
    ).includes("todo_view"),
    "entity grants must not become tenant-wide"
  )
  pass(
    "real business requests enforce department row isolation, inherited entity grants and child deny overrides"
  )

  const group = await client(admin.page, "createGroup", {
    name: "Branch viewers",
    type: "static",
    description: "Lifecycle group",
    status: "active",
    sort_order: 1,
  })
  const secondGroup = await client(admin.page, "createGroup", {
    name: "Branch operator",
    type: "static",
    status: "active",
    sort_order: 1,
  })
  await client(admin.page, "putGroupMember", group.id, charlie.id, {})
  await client(admin.page, "putGroupMember", secondGroup.id, charlie.id, {})
  for (const [type, item] of [
    ["group", group],
    ["group", secondGroup],
  ]) {
    await client(
      admin.page,
      "addRoleDirectoryBinding",
      viewer.id,
      type,
      item.id
    )
  }
  assert(
    (
      await call(
        scopedSession.context,
        "/principals/{principal_id}/permissions"
      )
    ).includes("user_view")
  )
  await client(
    admin.page,
    "removeRoleDirectoryBinding",
    viewer.id,
    "group",
    group.id
  )
  assert(
    (
      await call(
        scopedSession.context,
        "/principals/{principal_id}/permissions"
      )
    ).includes("user_view"),
    "remaining group retains authority"
  )
  await client(
    admin.page,
    "removeRoleDirectoryBinding",
    viewer.id,
    "group",
    secondGroup.id
  )
  assert(
    !(
      await call(
        scopedSession.context,
        "/principals/{principal_id}/permissions"
      )
    ).includes("user_view")
  )
  const dynamic = await client(admin.page, "createGroup", {
    name: "Operations viewers",
    type: "dynamic",
    description: "Department-driven membership",
    status: "active",
    sort_order: 2,
    membership_rule: {
      version: 1,
      match: "all",
      conditions: [
        {
          field: "member.department_id",
          operator: "in",
          values: [operations.id, operationsChild.id],
        },
      ],
    },
  })
  await client(
    admin.page,
    "addRoleDirectoryBinding",
    viewer.id,
    "group",
    dynamic.id
  )
  assert(
    (
      await call(
        scopedSession.context,
        "/principals/{principal_id}/permissions"
      )
    ).includes("user_view")
  )
  await client(admin.page, "updateMember", charlie.id, {
    department_id: finance.id,
  })
  assert(
    !(
      await call(
        scopedSession.context,
        "/principals/{principal_id}/permissions"
      )
    ).includes("user_view"),
    "department move removes dynamic group authority"
  )
  await client(admin.page, "updateMember", charlie.id, {
    department_id: operationsChild.id,
  })
  assert(
    (
      await call(
        scopedSession.context,
        "/principals/{principal_id}/permissions"
      )
    ).includes("user_view")
  )
  await client(
    admin.page,
    "removeRoleDirectoryBinding",
    viewer.id,
    "group",
    dynamic.id
  )
  pass(
    "independent group assignments combine without accidental revocation; department moves update dynamic group authority"
  )

  const parent = await client(admin.page, "createMenu", {
    label: "Restricted parent",
    route: "/roles?view=business",
    permission_code: "role_view",
    status: "active",
    sort_order: 100,
    icon: "",
  })
  const custom = await client(admin.page, "createMenu", {
    label: "Business accounts",
    route: "/users?view=business",
    permission_code: "user_view",
    parent_id: parent.id,
    status: "active",
    sort_order: 1,
    icon: "",
  })
  for (const [suffix, code] of [
    ["missing", ""],
    ["wrong", "user_view"],
  ]) {
    const unsafe = await client(admin.page, "createMenu", {
      label: `Misconfigured ${suffix}`,
      route: `/roles?configuration=${suffix}`,
      permission_code: code,
      status: "active",
      sort_order: 102,
      icon: "",
    })
    assert(
      !(
        await call(aliceSession.context, "/principals/{principal_id}/menus")
      ).some((menu) => menu.id === unsafe.id),
      "custom configuration cannot weaken built-in page permissions"
    )
  }
  const tree = await call(
    aliceSession.context,
    "/principals/{principal_id}/menus"
  )
  const directoryNode = tree.find((menu) => menu.id === parent.id)
  assert(directoryNode)
  assert(!directoryNode.path)
  assert.equal(directoryNode.children[0].id, custom.id)
  await aliceSession.page.reload()
  await aliceSession.page
    .getByRole("button", { name: "Restricted parent", exact: true })
    .waitFor()
  assert.equal(
    await aliceSession.page
      .locator(`aside a[href="${tenantPage("/roles")}"]`)
      .count(),
    0
  )
  await capture(aliceSession.page, "viewer-custom-menu", 1440)
  await capture(aliceSession.page, "viewer-custom-menu", 390)
  await aliceSession.page.setViewportSize({ width: 1440, height: 1000 })
  await client(admin.page, "updateMenu", custom.id, {
    label: "Customized business accounts",
    status: "disabled",
  })
  assert(
    !(
      await call(aliceSession.context, "/principals/{principal_id}/menus")
    ).some((menu) => menu.id === parent.id),
    "disabled last child removes inaccessible parent"
  )
  await client(admin.page, "updateMenu", custom.id, { status: "active" })
  const customizedTree = await call(
    aliceSession.context,
    "/principals/{principal_id}/menus"
  )
  assert.equal(
    customizedTree.find((menu) => menu.id === parent.id).children[0].label,
    "Customized business accounts"
  )
  pass(
    "custom menus retain authorized children without linking unauthorized parents; desktop/mobile render"
  )

  await admin.page.goto(tenantPage(`/roles?role=${viewer.id}`))
  await admin.page
    .locator('[data-permission-code="user_create"] [role="checkbox"]')
    .click()
  await aliceSession.page
    .getByRole("button", { name: "Create account", exact: true })
    .waitFor({ timeout: 25_000 })
  await admin.page
    .locator('[data-permission-code="user_create"] [role="checkbox"]')
    .click()
  await aliceSession.page
    .getByRole("button", { name: "Create account", exact: true })
    .waitFor({ state: "hidden", timeout: 25_000 })
  await call(
    aliceSession.context,
    "/principals",
    "POST",
    { login_name: "still-forbidden@example.test", password: accountPassword },
    { status: 403 }
  )
  await client(
    admin.page,
    "deleteEntityRoleBinding",
    company.id,
    branchTasks.id,
    charlie.id
  )
  await call(scopedSession.context, "/todos", "GET", undefined, {
    entity: east.id,
    status: 403,
  })
  pass(
    "live grant/revoke updates another signed-in browser without reload; scoped revocation immediately denies business API"
  )

  await aliceSession.page.route("**/principals/*/permissions", (route) =>
    route.fulfill({
      status: 503,
      contentType: "application/json",
      body: JSON.stringify({ message: "authorization_unavailable" }),
    })
  )
  await aliceSession.page.evaluate(() =>
    window.dispatchEvent(new Event("menu-catalog-change"))
  )
  await aliceSession.page
    .getByRole("button", { name: "Retry now", exact: true })
    .waitFor()
  assert.equal(
    await aliceSession.page
      .getByRole("button", { name: "Create account", exact: true })
      .count(),
    0
  )
  await aliceSession.page.unroute("**/principals/*/permissions")
  await aliceSession.page
    .getByRole("button", { name: "Retry now", exact: true })
    .click()
  await eventually(
    async () =>
      (await aliceSession.page
        .getByRole("button", { name: "Retry now", exact: true })
        .count()) === 0,
    "authorization retry"
  )
  pass(
    "authorization read failure keeps protected actions unavailable and recovers through retry"
  )

  const adminSubject = (
    await call(admin.context, "/me", "GET", undefined, {
      tenant: false,
    })
  ).subject
  const otherTenant = await call(
    admin.context,
    "/tenants",
    "POST",
    {
      slug: "other-business",
      name: "Other Business",
      owner_principal_id: adminSubject,
    },
    { tenant: false }
  )
  await call(
    aliceSession.context,
    "/principals/{principal_id}/menus",
    "GET",
    undefined,
    {
      tenant: otherTenant.id,
      status: 403,
    }
  )
  // Business rows hang off an entity, and the entity now carries its own
  // tenant, so naming another tenant beside one of this tenant's entities no
  // longer describes a cross-tenant read -- it just reads East Branch, which
  // Alice may do. The denial this step is here to prove needs an entity that
  // actually belongs to the other tenant.
  const otherEntity = await call(
    admin.context,
    "/entities",
    "POST",
    { name: "Other Branch", type: "unit" },
    { tenant: otherTenant.id }
  )
  // An entity outside the caller's reach is modeled as not found rather than
  // forbidden, so the other tenant's existence does not leak; the backend
  // tests fix that (ErrEntityNotFound -> 404). Reading the same URI as a
  // member of that tenant is what makes Alice's 404 isolation instead of a
  // mistyped identifier.
  await call(admin.context, "/todos", "GET", undefined, {
    entity: otherEntity.id,
  })
  await call(aliceSession.context, "/todos", "GET", undefined, {
    entity: otherEntity.id,
    status: 404,
  })
  await call(
    aliceSession.context,
    "/tenants",
    "POST",
    {
      slug: "forbidden-business",
      name: "Forbidden",
      owner_principal_id: alice.id,
    },
    { tenant: false, status: 403 }
  )
  await call(admin.context, `/principals/${bob.id}/suspension`, "PUT")
  await call(bobSession.context, "/principals", "GET", undefined, {
    status: 401,
  })
  await call(admin.context, `/principals/${bob.id}/suspension`, "DELETE")
  const restored = await login(bob.login_name, accountPassword, 390)
  await restored.page.goto(tenantPage("/users"))
  await restored.page
    .getByRole("button", { name: "Create account", exact: true })
    .waitFor()
  await capture(restored.page, "restored-editor", 390)
  pass(
    "cross-tenant access and non-platform tenant creation denied; suspension revokes sessions and restoration permits fresh login"
  )

  const devices = await login(alice.login_name, accountPassword)
  const secondDevice = await login(alice.login_name, accountPassword, 390)
  const identity = await client(devices.page, "session")
  assert.equal(identity.subject, alice.id)
  const current = await client(devices.page, "currentBrowserSession")
  assert.match(current.id, /^[1-9][0-9]*$/)
  assert.equal(current.current, true)
  for (const field of [
    "created_at",
    "last_seen_at",
    "expires_at",
    "absolute_expires_at",
  ]) {
    assert(Number.isFinite(Date.parse(current[field])), field)
  }
  assert.equal(current.subject, undefined)
  assert.equal(current.email, undefined)
  const cookies = await devices.context.cookies()
  const cookieSecret = cookies.find(
    (cookie) => cookie.name === "lifecycle_session"
  )?.value
  assert(cookieSecret, "browser session credential exists")
  assert(
    !JSON.stringify(current).includes(cookieSecret),
    "public session must not expose Cookie credential"
  )
  const item = await client(devices.page, "browserSession", current.id)
  assert.equal(item.id, current.id)
  assert.equal(item.created_at, current.created_at)
  assert.equal(item.absolute_expires_at, current.absolute_expires_at)
  const active = await client(devices.page, "sessions")
  assert(active.some((row) => row.id === current.id && row.current))
  const foreign = await client(admin.page, "currentBrowserSession")
  await call(devices.context, `/sessions/${foreign.id}`, "GET", undefined, {
    status: 404,
  })
  // Sessions are addressed by their public snowflake ID; an unknown one is as
  // absent as a foreign one.
  await call(devices.context, "/sessions/999999999999999999", "GET", undefined, {
    status: 404,
  })
  const mixed = await devices.context.request.get("/api/sessions/current", {
    headers: { Authorization: "Bearer invalid" },
  })
  assert.equal(
    mixed.status(),
    401,
    "Bearer cannot select current Cookie session"
  )
  const secondCurrent = await client(secondDevice.page, "currentBrowserSession")
  const csrf = await devices.context.request.delete(
    `/api/sessions/${secondCurrent.id}`,
    {
      headers: { Origin: "https://evil.invalid" },
    }
  )
  assert.equal(csrf.status(), 403)
  await call(secondDevice.context, "/sessions/current")
  await client(devices.page, "revokeSession", secondCurrent.id)
  await call(secondDevice.context, "/me", "GET", undefined, { status: 401 })
  await call(
    devices.context,
    `/sessions/${secondCurrent.id}`,
    "GET",
    undefined,
    { status: 404 }
  )
  await call(admin.context, "/sessions/current")
  for (const old of ["/me/session", `/me/sessions/${current.id}`]) {
    assert(!published(old), `${old} must be absent from OpenAPI`)
    assert.equal(
      (await devices.context.request.get(`/api${old}`)).status(),
      404
    )
  }
  pass(
    "real Session discovery/item metadata, secret exclusion, owner isolation, CSRF and device revocation"
  )

  const accountRoot = await client(devices.page, "createAccountFileRoot")
  const ownSubject = (await call(devices.context, "/me")).subject
  const foreignSubject = (await call(admin.context, "/me")).subject
  for (const suffix of [
    "files",
    "sessions",
    "identity-links",
    "avatar",
    "closure-request",
    "personal-tokens",
  ]) {
    await call(
      devices.context,
      `/principals/${foreignSubject}/${suffix}`,
      "GET",
      undefined,
      { tenant: false, status: 404 }
    )
  }
  for (const [suffix, method, body] of [
    ["avatar", "PUT", { style: "robot" }],
    ["sessions", "DELETE", undefined],
    ["mfa/totp-enrollments", "POST", { current_password: accountPassword }],
  ]) {
    await call(
      devices.context,
      `/principals/${foreignSubject}/${suffix}`,
      method,
      body,
      { tenant: false, status: 404 }
    )
  }
  for (const id of ["0", "01", "bad", "9999999999999999999"]) {
    await call(devices.context, `/principals/${id}/files`, "GET", undefined, {
      tenant: false,
      status: 422,
    })
  }
  for (const path of [
    "/me/files",
    "/me/file-operations",
    "/me/avatar",
    "/me/identity-links",
    `/tenants/${tenant.id}/me/menus`,
  ]) {
    assert(!published(path))
    assert.equal(
      (await devices.context.request.get(`/api${path}`)).status(),
      404,
      path
    )
  }
  const avatar = await client(devices.page, "setMyAvatar", { style: "robot" })
  assert.equal(avatar.url, `/api/principals/${ownSubject}/avatar/content`)
  const settings = await call(
    devices.context,
    `/principals/${ownSubject}/avatar`,
    "GET",
    undefined,
    { tenant: false }
  )
  assert.equal(settings.url, avatar.url)
  const image = await devices.context.request.get(avatar.url)
  assert.equal(image.status(), 200)
  assert.match(image.headers()["content-type"], /image\/png/)
  assert((await image.body()).length > 0)
  pass(
    "explicit principal read/write isolation, malformed selectors, retired addresses and separate avatar content"
  )
  assert.equal(accountRoot.scope, "account")
  assert(
    (await client(devices.page, "files", null)).items.some(
      (row) => row.id === accountRoot.id
    )
  )
  const invalidRoot = await devices.context.request.post(
    `/api/principals/${(await call(devices.context, "/me")).subject}/files`,
    {
      headers: { Origin: webURL },
      multipart: { kind: "directory", scope: "personal" },
    }
  )
  assert.equal(invalidRoot.status(), 422)
  pass(
    "real account-root creation/listing and rejected personal multipart scope"
  )

  const thirdDevice = await login(alice.login_name, accountPassword, 390)
  await devices.page.goto("/user/security")
  await devices.page
    .getByRole("button", { name: "Sign out all devices", exact: true })
    .waitFor()
  await devices.page
    .getByRole("button", { name: "Sign out all devices", exact: true })
    .click()
  await devices.page.waitForURL((url) => url.pathname === "/user/login")
  await call(thirdDevice.context, "/me", "GET", undefined, { status: 401 })
  assert(
    !(await devices.context.cookies()).some(
      (cookie) => cookie.name === "lifecycle_session"
    )
  )
  await call(admin.context, "/sessions/current")
  const logoutDevice = await login(alice.login_name, accountPassword)
  await logoutDevice.page
    .getByRole("button", { name: "Account menu", exact: true })
    .click()
  await logoutDevice.page
    .getByRole("menuitem", { name: "Sign out", exact: true })
    .click()
  await logoutDevice.page.waitForURL((url) => url.pathname === "/user/login")
  await call(logoutDevice.context, "/me", "GET", undefined, { status: 401 })
  const repeatLogout = await logoutDevice.context.request.delete(
    "/api/sessions/current",
    {
      headers: { Origin: webURL },
    }
  )
  assert.equal(repeatLogout.status(), 200)
  pass(
    "desktop security UI logout-all revokes mobile Cookie, clears browser Cookie; current logout is idempotent"
  )

  const issued = await client(admin.page, "createInvitation", {
    email: "invitee@example.test",
  })
  const rotated = await client(
    admin.page,
    "resendInvitation",
    issued.invitation.id
  )
  assert.notEqual(rotated.token, issued.token)
  const invitee = await browser.newContext({ baseURL: webURL })
  const inviteePage = await invitee.newPage()
  await inviteePage.goto("/user/login")
  const acceptBody = {
    token: issued.token,
    login_name: "invitee@example.test",
    password: accountPassword,
    display_name: "Invitee",
  }
  await call(
    invitee,
    "/membership-invitations/acceptances",
    "POST",
    acceptBody,
    { tenant: false, status: 400 }
  )
  const accepted = await client(inviteePage, "acceptInvitation", {
    ...acceptBody,
    token: rotated.token,
  })
  assert.equal(accepted.tenant_id, tenant.id)
  assert.equal(accepted.invitation_id, issued.invitation.id)
  assert(
    (await client(admin.page, "members")).some(
      (m) => m.subject === accepted.principal_id
    )
  )
  const accessRole = await createRole(admin.page, "Requested access", [
    "user_view",
  ])
  const requester = await login(acceptBody.login_name, accountPassword)
  const requested = await call(
    requester.context,
    "/role-access-requests",
    "POST",
    {
      role_id: accessRole.id,
      reason: "Verify renamed role access workflow",
      access_expires_at: new Date(Date.now() + 3600000).toISOString(),
    }
  )
  assert.equal(
    (await client(admin.page, "approveAccessRequest", requested.id)).status,
    "approved"
  )
  assert(
    (
      await call(requester.context, "/principals/{principal_id}/permissions")
    ).includes("user_view")
  )
  const review = await client(admin.page, "createAccessReview", {
    name: "Renamed role access review",
    due_at: new Date(Date.now() + 86400000).toISOString(),
  })
  const reviewDetails = await client(admin.page, "accessReview", review.id)
  const reviewItem = reviewDetails.items.find(
    (item) =>
      item.principal_id === accepted.principal_id &&
      item.role_id === accessRole.id
  )
  assert(reviewItem)
  await client(
    admin.page,
    "decideAccessReviewItem",
    review.id,
    reviewItem.id,
    "keep",
    "Verified"
  )
  await client(admin.page, "cancelAccessReview", review.id)
  await client(
    admin.page,
    "revokeAccessRequest",
    requested.id,
    "Acceptance complete"
  )
  assert(
    !(
      await call(requester.context, "/principals/{principal_id}/permissions")
    ).includes("user_view")
  )
  await requester.context.close()
  pass(
    "role access request, approval, review snapshot/decision and grant revocation"
  )
  const revoke = await client(admin.page, "createInvitation", {
    email: "revoked@example.test",
  })
  await client(admin.page, "revokeInvitation", revoke.invitation.id)
  await call(
    invitee,
    "/membership-invitations/acceptances",
    "POST",
    { ...acceptBody, token: revoke.token, login_name: "revoked@example.test" },
    { tenant: false, status: 400 }
  )
  for (const [method, path] of [
    ["GET", "/groups/1"],
    ["GET", "/positions/1"],
    ["GET", "/job-positions/1"],
    ["GET", `/tenants/${tenant.id}/job-positions`],
    ["DELETE", "/invitations/1"],
    ["POST", "/invitation-acceptances"],
    ["POST", "/email-verification-requests"],
    ["GET", "/access-reviews/1"],
    ["PUT", "/access-requests/1/decision"],
    ["GET", "/scope-model"],
    ["GET", `/tenants/${tenant.id}/relationships`],
  ]) {
    assert(!published(path), path)
    assert.equal(
      (
        await admin.context.request.fetch(`/api${path}`, {
          method,
          headers: { Origin: webURL },
        })
      ).status(),
      404,
      path
    )
  }
  await invitee.close()
  pass(
    "membership invitation rotation, anonymous acceptance, revoked credentials and retired resource addresses"
  )

  }
  await administrationAccountViewAcceptance({ admin, browser, tenant, webURL, directory, login, eventually, pass, notifications })
  await authEntrancesAcceptance({ admin, browser, webURL, directory, notifications, eventually, pass, adminLogin, adminPassword })
  const audit = await call(admin.context, "/audit/events?limit=200")
  for (const event of (process.env.IAM_ACCOUNT_VIEW_ONLY === "1" ? ["tenant_bootstrapped"] : [
    "tenant_bootstrapped",
    "role_permission_granted",
    "role_permission_revoked",
    "entity_role_binding_deleted",
  ])) {
    assert(
      audit.some((row) => row.event_type === event),
      `missing audit event ${event}`
    )
  }
  assert.deepEqual(pageErrors, [], "uncaught browser errors")
  pass("lifecycle modifications are audited; no uncaught browser errors")
  }
} catch (error) {
  results.push({ flow: "failure", status: "FAIL", message: error.message })
  throw error
} finally {
  await browser?.close()
  await new Promise((resolve) => notificationServer.close(resolve))
  for (const child of children.reverse()) {
    if (child.exitCode !== null || child.signalCode !== null) continue
    terminate(child, false)
    await eventually(
      async () => child.exitCode !== null || child.signalCode !== null,
      "owned service shutdown",
      10_000
    ).catch(() => terminate(child, true))
  }
  await writeFile(
    join(directory, "report.json"),
    JSON.stringify(
      {
        database: "sqlite",
        mockedBusinessResponses: 0,
        tenantID: tenant?.id,
        results,
        pageErrors,
      },
      null,
      2
    ),
    { mode: 0o600 }
  )
  console.log(`Report: ${join(directory, "report.json")}`)
}
