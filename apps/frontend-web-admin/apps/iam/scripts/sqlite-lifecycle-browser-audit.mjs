import assert from "node:assert/strict"
import { spawn } from "node:child_process"
import { randomBytes } from "node:crypto"
import { createWriteStream } from "node:fs"
import { mkdir, mkdtemp, writeFile } from "node:fs/promises"
import { createServer } from "node:net"
import { join } from "node:path"
import { fileURLToPath } from "node:url"
import { chromium } from "playwright"

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
    detached: true,
    stdio: ["ignore", "pipe", "pipe"],
  })
  child.stdout.pipe(log, { end: false })
  child.stderr.pipe(log, { end: false })
  child.once("close", () => log.end())
  children.push(child)
  return child
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
const binary = join(directory, "api")
const configPath = join(directory, "config.json")
const adminPassword = randomBytes(24).toString("base64url") + "aA1!"
const accountPassword = randomBytes(24).toString("base64url") + "bB2!"
const adminLogin = "admin@example.test"
const pageErrors = []
let tenant

async function client(page, method, ...args) {
  return page.evaluate(
    async ({ method, args }) => {
      const { iamApi } = await import("/src/lib/iam-api.ts")
      return iamApi[method](...args)
    },
    { method, args }
  )
}

async function call(context, path, method = "GET", body, options = {}) {
  const response = await context.request.fetch(`/api${path}`, {
    method,
    headers: {
      Origin: webURL,
      ...(tenant && options.tenant !== false
        ? { "X-Tenant-Id": options.tenant ?? tenant.id }
        : {}),
      ...(options.entity ? { "X-Entity-Id": options.entity } : {}),
    },
    ...(body === undefined ? {} : { data: body }),
  })
  if (options.status) {
    assert.equal(response.status(), options.status, `${method} ${path}`)
    return
  }
  assert(response.ok(), `${method} ${path}: HTTP ${response.status()}`)
  return (await response.json()).data
}

async function login(loginName, password, width = 1440) {
  const context = await browser.newContext({
    baseURL: webURL,
    viewport: { width, height: 1000 },
    locale: "en-US",
  })
  await context.addCookies([
    { name: "NEXT_LOCALE", value: "en-US", url: webURL },
  ])
  const page = await context.newPage()
  page.on("pageerror", (error) => pageErrors.push(error.message))
  await page.goto("/login")
  await page.locator("#login-name").fill(loginName)
  await page.locator("#password").fill(password)
  await page.locator('form button[type="submit"]').click()
  await page.waitForURL((url) => url.pathname !== "/login")
  return { context, page }
}

async function submit(page, form) {
  await page.locator(`button[form="${form}"][type="submit"]`).click()
  await page.locator(`#${form}`).waitFor({ state: "hidden" })
}

async function createDepartment(page, name, parent) {
  await page.goto("/iam/departments")
  await page
    .getByRole("button", { name: "Create department", exact: true })
    .click()
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
  await page.goto("/iam/entities")
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
  await page.goto("/iam/users")
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
  await page.goto("/iam/roles")
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
        (await client(page, "rolePermissions", role.id)).includes(code),
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
  for (const name of ["signing-key", "mfa-key", "provisioning-key"]) {
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
        authn: {
          enabled: true,
          issuer: apiURL,
          signing_key_file: join(directory, "signing-key"),
          mfa: { encryption_key_file: join(directory, "mfa-key") },
          web: {
            enabled: true,
            cookie_name: "lifecycle_session",
            cookie_secure: false,
            post_login_url: webURL + "/",
            post_logout_url: webURL + "/login",
            allowed_return_urls: [webURL + "/"],
            allowed_origins: [webURL],
          },
        },
        provisioning: {
          encryption_key_file: join(directory, "provisioning-key"),
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
  await call(anonymous, "/iam/me/permissions", "GET", undefined, {
    tenant: false,
    status: 401,
  })
  await anonymous.close()
  const admin = await login(adminLogin, adminPassword)
  assert.deepEqual(
    await call(admin.context, "/iam/tenants", "GET", undefined, {
      tenant: false,
    }),
    []
  )
  const platformCodes = await call(
    admin.context,
    "/iam/me/permissions",
    "GET",
    undefined,
    { tenant: false }
  )
  assert(platformCodes.includes("tenant_create"))
  assert(!platformCodes.includes("user_create"))
  await admin.page.goto("/iam/tenants")
  await admin.page
    .getByRole("button", { name: "Create tenant", exact: true })
    .click()
  await admin.page.locator("#tenant-name").fill("First Business")
  await admin.page.locator("#tenant-slug").fill("first-business")
  await submit(admin.page, "tenant-form")
  tenant = (
    await call(admin.context, "/iam/tenants", "GET", undefined, {
      tenant: false,
    })
  )[0]
  assert.equal(tenant.name, "First Business")
  await eventually(
    async () =>
      (await admin.page.evaluate(() => localStorage.getItem("iam.tenant"))) ===
      tenant.id,
    "first tenant adoption"
  )
  const defaultMenus = await call(admin.context, "/iam/menus")
  assert.equal(defaultMenus.length, 15)
  const administrator = (await call(admin.context, "/iam/roles")).find(
    (role) => role.name === "Administrator"
  )
  assert(administrator)
  assert.deepEqual(
    await call(admin.context, `/iam/roles/${administrator.id}/permissions`),
    ["tenant_administer"]
  )
  await capture(admin.page, "first-tenant", 1440)
  pass(
    "browser creates the first real tenant without a pre-existing membership; owner, role and menus initialized"
  )

  const operations = await createDepartment(admin.page, "Operations")
  const operationsChild = await createDepartment(
    admin.page,
    "Branch operations",
    operations
  )
  const finance = await createDepartment(admin.page, "Finance")
  const company = await createEntity(admin.page, "Company")
  const east = await createEntity(admin.page, "East Branch", company)
  const west = await createEntity(admin.page, "West Branch", company)
  const alice = await createAccount(admin.page, "Alice", operations)
  const bob = await createAccount(admin.page, "Bob", finance)
  const charlie = await createAccount(admin.page, "Charlie", operationsChild)
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
    await call(aliceSession.context, "/iam/me/menus")
  )
  assert(aliceMenus.includes("/iam/users"))
  assert(!aliceMenus.includes("/iam/roles"))
  await aliceSession.page.goto("/iam/users")
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
  await bobSession.page.goto("/iam/users")
  await bobSession.page
    .getByRole("button", { name: "Create account", exact: true })
    .waitFor()
  await call(
    aliceSession.context,
    "/iam/principals",
    "POST",
    { login_name: "forbidden@example.test", password: accountPassword },
    { status: 403 }
  )
  await call(scopedSession.context, "/iam/roles", "GET", undefined, {
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
    !(await call(scopedSession.context, "/iam/me/permissions")).includes(
      "todo_view"
    ),
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
  const position = await client(admin.page, "createPosition", {
    name: "Branch operator",
    code: "branch-operator",
    status: "active",
    sort_order: 1,
  })
  await client(admin.page, "putGroupMember", group.id, charlie.id, {})
  await client(admin.page, "putPositionMember", position.id, charlie.id, {})
  for (const [type, item] of [
    ["group", group],
    ["position", position],
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
    (await call(scopedSession.context, "/iam/me/permissions")).includes(
      "user_view"
    )
  )
  await client(
    admin.page,
    "removeRoleDirectoryBinding",
    viewer.id,
    "group",
    group.id
  )
  assert(
    (await call(scopedSession.context, "/iam/me/permissions")).includes(
      "user_view"
    ),
    "remaining position retains authority"
  )
  await client(
    admin.page,
    "removeRoleDirectoryBinding",
    viewer.id,
    "position",
    position.id
  )
  assert(
    !(await call(scopedSession.context, "/iam/me/permissions")).includes(
      "user_view"
    )
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
    (await call(scopedSession.context, "/iam/me/permissions")).includes(
      "user_view"
    )
  )
  await client(admin.page, "updateMember", charlie.id, {
    department_id: finance.id,
  })
  assert(
    !(await call(scopedSession.context, "/iam/me/permissions")).includes(
      "user_view"
    ),
    "department move removes dynamic group authority"
  )
  await client(admin.page, "updateMember", charlie.id, {
    department_id: operationsChild.id,
  })
  assert(
    (await call(scopedSession.context, "/iam/me/permissions")).includes(
      "user_view"
    )
  )
  await client(
    admin.page,
    "removeRoleDirectoryBinding",
    viewer.id,
    "group",
    dynamic.id
  )
  pass(
    "group/position assignments combine without accidental revocation; department moves update dynamic group authority"
  )

  const parent = await client(admin.page, "createMenu", {
    label: "Restricted parent",
    route: "/iam/roles?view=business",
    permission_code: "role_view",
    status: "active",
    sort_order: 100,
    icon: "",
  })
  const custom = await client(admin.page, "createMenu", {
    label: "Business accounts",
    route: "/iam/users?view=business",
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
      route: `/iam/roles?configuration=${suffix}`,
      permission_code: code,
      status: "active",
      sort_order: 102,
      icon: "",
    })
    assert(
      !(await call(aliceSession.context, "/iam/me/menus")).some(
        (menu) => menu.id === unsafe.id
      ),
      "custom configuration cannot weaken built-in page permissions"
    )
  }
  const tree = await call(aliceSession.context, "/iam/me/menus")
  const directoryNode = tree.find((menu) => menu.id === parent.id)
  assert(directoryNode)
  assert(!directoryNode.path)
  assert.equal(directoryNode.children[0].id, custom.id)
  await aliceSession.page.reload()
  await aliceSession.page
    .getByRole("button", { name: "Restricted parent", exact: true })
    .waitFor()
  assert.equal(
    await aliceSession.page.locator('aside a[href="/iam/roles"]').count(),
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
    !(await call(aliceSession.context, "/iam/me/menus")).some(
      (menu) => menu.id === parent.id
    ),
    "disabled last child removes inaccessible parent"
  )
  await client(admin.page, "updateMenu", custom.id, { status: "active" })
  const customizedTree = await call(aliceSession.context, "/iam/me/menus")
  assert.equal(
    customizedTree.find((menu) => menu.id === parent.id).children[0].label,
    "Customized business accounts"
  )
  pass(
    "custom menus retain authorized children without linking unauthorized parents; desktop/mobile render"
  )

  await admin.page.goto(`/iam/roles?role=${viewer.id}`)
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
    "/iam/principals",
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

  await aliceSession.page.route("**/api/iam/me/permissions", (route) =>
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
  await aliceSession.page.unroute("**/api/iam/me/permissions")
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
    await call(admin.context, "/authn/sessions/current", "GET", undefined, {
      tenant: false,
    })
  ).subject
  const otherTenant = await call(
    admin.context,
    "/iam/tenants",
    "POST",
    {
      slug: "other-business",
      name: "Other Business",
      owner_principal_id: adminSubject,
    },
    { tenant: false }
  )
  await call(aliceSession.context, "/iam/me/menus", "GET", undefined, {
    tenant: otherTenant.id,
    status: 403,
  })
  await call(aliceSession.context, "/todos", "GET", undefined, {
    tenant: otherTenant.id,
    entity: east.id,
    status: 403,
  })
  await call(
    aliceSession.context,
    "/iam/tenants",
    "POST",
    {
      slug: "forbidden-business",
      name: "Forbidden",
      owner_principal_id: alice.id,
    },
    { tenant: false, status: 403 }
  )
  await call(admin.context, `/iam/principals/${bob.id}/suspension`, "PUT")
  await call(bobSession.context, "/iam/principals", "GET", undefined, {
    status: 401,
  })
  await call(admin.context, `/iam/principals/${bob.id}/suspension`, "DELETE")
  const restored = await login(bob.login_name, accountPassword, 390)
  await restored.page.goto("/iam/users")
  await restored.page
    .getByRole("button", { name: "Create account", exact: true })
    .waitFor()
  await capture(restored.page, "restored-editor", 390)
  pass(
    "cross-tenant access and non-platform tenant creation denied; suspension revokes sessions and restoration permits fresh login"
  )

  const audit = await call(admin.context, "/iam/audit-events?limit=200")
  for (const event of [
    "tenant_bootstrapped",
    "role_permission_granted",
    "role_permission_revoked",
    "entity_role_binding_deleted",
  ]) {
    assert(
      audit.some((row) => row.event_type === event),
      `missing audit event ${event}`
    )
  }
  assert.deepEqual(pageErrors, [], "uncaught browser errors")
  pass("lifecycle modifications are audited; no uncaught browser errors")
} catch (error) {
  results.push({ flow: "failure", status: "FAIL", message: error.message })
  throw error
} finally {
  await browser?.close()
  for (const child of children.reverse()) {
    if (child.exitCode !== null || child.signalCode !== null) continue
    try {
      process.kill(-child.pid, "SIGTERM")
    } catch (error) {
      if (error.code !== "ESRCH") throw error
    }
    await eventually(
      async () => child.exitCode !== null || child.signalCode !== null,
      "owned service shutdown",
      10_000
    ).catch(() => {
      try {
        process.kill(-child.pid, "SIGKILL")
      } catch (error) {
        if (error.code !== "ESRCH") throw error
      }
    })
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
