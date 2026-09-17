import { readFileSync } from "node:fs"

import { auditPassword } from "./audit-password.mjs"

const port = Number(process.argv[2] ?? 9333)
const baseURL = process.argv[3] ?? "http://localhost:8091"
const adminPassword = auditPassword()
const memberPasswordPath = process.env.IAM_BROWSER_MEMBER_PASSWORD_FILE
const adminLogin = process.env.IAM_BROWSER_LOGIN ?? "admin@example.test"
const memberLogin =
  process.env.IAM_BROWSER_MEMBER_LOGIN ?? "member@example.test"
const tenantID = process.env.IAM_BROWSER_TENANT
const screenshotRoot = new URL(
  "../../../../../.local/screenshots/",
  import.meta.url
)

if (!tenantID) throw new Error("IAM_BROWSER_TENANT is required")
if (!memberPasswordPath)
  throw new Error("IAM_BROWSER_MEMBER_PASSWORD_FILE is required")
const memberPassword = readFileSync(memberPasswordPath, "utf8").trim()

const targets = await fetch(`http://127.0.0.1:${port}/json/list`).then(
  (response) => response.json()
)
const target = targets.find((candidate) => candidate.type === "page")
if (!target?.webSocketDebuggerUrl)
  throw new Error("Chrome page target was not found")

const socket = new WebSocket(target.webSocketDebuggerUrl)
await new Promise((resolve, reject) => {
  socket.addEventListener("open", resolve, { once: true })
  socket.addEventListener("error", reject, { once: true })
})

let sequence = 0
const pending = new Map()
const requestByID = new Map()
const responses = []
const failedRequests = []
const consoleErrors = []

socket.addEventListener("message", (event) => {
  const message = JSON.parse(event.data)
  if (message.id) {
    const request = pending.get(message.id)
    if (!request) return
    pending.delete(message.id)
    if (message.error) request.reject(new Error(message.error.message))
    else request.resolve(message.result)
    return
  }
  if (message.method === "Network.requestWillBeSent")
    requestByID.set(message.params.requestId, {
      method: message.params.request.method,
      url: message.params.request.url,
    })
  if (message.method === "Network.responseReceived") {
    const request = requestByID.get(message.params.requestId)
    responses.push({
      method: request?.method ?? "GET",
      url: message.params.response.url,
      status: message.params.response.status,
    })
  }
  if (message.method === "Network.loadingFailed") {
    const request = requestByID.get(message.params.requestId)
    failedRequests.push({
      method: request?.method ?? "GET",
      url: request?.url ?? "unknown",
      error: message.params.errorText,
      canceled: Boolean(message.params.canceled),
    })
  }
  if (message.method === "Runtime.exceptionThrown")
    consoleErrors.push(message.params.exceptionDetails.text)
  if (
    message.method === "Runtime.consoleAPICalled" &&
    message.params.type === "error"
  )
    consoleErrors.push(
      message.params.args
        .map((argument) => argument.value ?? argument.description)
        .join(" ")
    )
})

function command(method, params = {}) {
  const id = ++sequence
  socket.send(JSON.stringify({ id, method, params }))
  return new Promise((resolve, reject) => pending.set(id, { resolve, reject }))
}

async function evaluate(source) {
  const result = await command("Runtime.evaluate", {
    expression: `(async () => { ${source} })()`,
    awaitPromise: true,
    returnByValue: true,
  })
  if (result.exceptionDetails)
    throw new Error(
      result.exceptionDetails.exception?.description ??
        result.exceptionDetails.text
    )
  return result.result.value
}

async function waitFor(source, timeout = 15000) {
  const deadline = Date.now() + timeout
  while (Date.now() < deadline) {
    if (await evaluate(`return Boolean(${source})`)) return
    await Bun.sleep(100)
  }
  throw new Error(`Timed out waiting for: ${source}`)
}

async function navigate(path) {
  await command("Page.navigate", { url: new URL(path, baseURL).href })
  await waitFor(`document.readyState === "complete"`)
}

async function setInput(selector, value) {
  await evaluate(`
    const input = document.querySelector(${JSON.stringify(selector)});
    if (!(input instanceof HTMLInputElement)) throw new Error("input not found: " + ${JSON.stringify(selector)});
    const setter = Object.getOwnPropertyDescriptor(HTMLInputElement.prototype, "value").set;
    setter.call(input, ${JSON.stringify(value)});
    input.dispatchEvent(new Event("input", { bubbles: true }));
    input.dispatchEvent(new Event("change", { bubbles: true }));
  `)
}

async function apiRaw(path, options = {}) {
  return evaluate(`
    const response = await fetch(${JSON.stringify(`/api${path}`)}, {
      credentials: "include",
      headers: {
        "Content-Type": "application/json",
        "X-Tenant-Id": ${JSON.stringify(tenantID)},
      },
      ...${JSON.stringify(options)},
    });
    const payload = await response.json().catch(() => null);
    return { status: response.status, payload };
  `)
}

async function api(path, options = {}) {
  const result = await apiRaw(path, options)
  if (result.status < 200 || result.status >= 300)
    throw new Error(
      `${path} failed: ${result.status} ${result.payload?.message ?? ""}`
    )
  return result.payload?.data
}

async function login(loginName, password) {
  await command("Network.setCookie", {
    name: "NEXT_LOCALE",
    value: "zh-CN",
    url: baseURL,
  })
  await navigate("/login")
  await waitFor(`document.querySelector("#login-name")`)
  await evaluate(
    `localStorage.setItem("iam.tenant", ${JSON.stringify(tenantID)})`
  )
  await setInput("#login-name", loginName)
  await setInput("#password", password)
  await evaluate(`document.querySelector("form").requestSubmit()`)
  await waitFor(`location.pathname === "/"`, 20000)
  const session = await api("/authn/sessions/current")
  await evaluate(`
    localStorage.setItem("iam.tenant", ${JSON.stringify(tenantID)});
    localStorage.setItem("iam.tenant:subject", ${JSON.stringify(session.subject)});
  `)
  await command("Page.reload", { ignoreCache: true })
  await waitFor(
    `document.readyState === "complete" && document.querySelector("h1")`
  )
}

async function logout() {
  const result = await apiRaw("/authn/sessions/current", { method: "DELETE" })
  if (result.status < 200 || result.status >= 300)
    throw new Error(`logout failed: ${result.status}`)
  await command("Network.clearBrowserCookies")
}

async function capture(name, width, height) {
  await command("Emulation.setDeviceMetricsOverride", {
    width,
    height,
    deviceScaleFactor: 1,
    mobile: width < 600,
  })
  await Bun.sleep(250)
  const layout = await evaluate(`
    const sheet = document.querySelector("[data-authorization-sources-sheet]");
    const sourceRows = [...document.querySelectorAll("[data-authorization-source]")];
    const sheetRect = sheet?.getBoundingClientRect();
    return {
      width: innerWidth,
      scrollWidth: document.documentElement.scrollWidth,
      horizontalOverflow: document.documentElement.scrollWidth > innerWidth,
      sheetLeft: sheetRect?.left ?? -1,
      sheetRight: sheetRect?.right ?? -1,
      sourceOverflow: sourceRows.some((row) => row.scrollWidth > row.clientWidth),
      sourceCount: sourceRows.length,
    };
  `)
  if (
    layout.horizontalOverflow ||
    layout.sourceOverflow ||
    layout.sheetLeft < 0 ||
    layout.sheetRight > width ||
    layout.sourceCount < 7
  )
    throw new Error(`${name} layout failed: ${JSON.stringify(layout)}`)
  const screenshot = await command("Page.captureScreenshot", {
    format: "png",
    fromSurface: true,
    captureBeyondViewport: false,
  })
  await Bun.write(
    new URL(name, screenshotRoot),
    Buffer.from(screenshot.data, "base64")
  )
  return layout
}

function findSource(snapshot, type, predicate = () => true) {
  return snapshot.sources.find(
    (source) => source.type === type && predicate(source)
  )
}

function requireSource(snapshot, type, predicate, description) {
  const source = findSource(snapshot, type, predicate)
  if (!source)
    throw new Error(
      `missing ${description}: ${JSON.stringify(snapshot.sources)}`
    )
  return source
}

const artifacts = {
  accessRequest: null,
  department: null,
  position: null,
  groups: [],
  roles: [],
  entities: [],
  roleMembers: [],
  groupMembers: [],
  positionMembers: [],
  directoryBindings: [],
  entityBindings: [],
  relationship: null,
  memberID: "",
}

async function cleanup() {
  const errors = []
  const attempt = async (operation) => {
    try {
      await operation()
    } catch (error) {
      errors.push(error.message)
    }
  }
  if (artifacts.accessRequest?.status === "approved")
    await attempt(async () => {
      await api(
        `/iam/access-requests/${encodeURIComponent(artifacts.accessRequest.id)}/revoke`,
        {
          method: "POST",
          body: JSON.stringify({ reason: "Acceptance cleanup" }),
        }
      )
    })
  if (artifacts.relationship)
    await attempt(async () => {
      const query = new URLSearchParams(artifacts.relationship)
      await api(`/iam/relationships?${query}`, { method: "DELETE" })
    })
  for (const binding of artifacts.entityBindings)
    await attempt(async () => {
      await api(
        `/iam/entities/${encodeURIComponent(binding.entityID)}/role-bindings/${encodeURIComponent(binding.roleID)}/${encodeURIComponent(artifacts.memberID)}`,
        { method: "DELETE" }
      )
    })
  for (const roleID of artifacts.roleMembers)
    await attempt(async () => {
      await api(
        `/iam/roles/${encodeURIComponent(roleID)}/members/${encodeURIComponent(artifacts.memberID)}`,
        { method: "DELETE" }
      )
    })
  for (const membership of artifacts.groupMembers)
    await attempt(async () => {
      await api(
        `/iam/groups/${encodeURIComponent(membership.groupID)}/members/${encodeURIComponent(artifacts.memberID)}`,
        { method: "DELETE" }
      )
    })
  for (const membership of artifacts.positionMembers)
    await attempt(async () => {
      await api(
        `/iam/positions/${encodeURIComponent(membership.positionID)}/members/${encodeURIComponent(artifacts.memberID)}`,
        { method: "DELETE" }
      )
    })
  for (const binding of artifacts.directoryBindings)
    await attempt(async () => {
      await api(
        `/iam/roles/${encodeURIComponent(binding.roleID)}/directory-bindings/${binding.type}/${encodeURIComponent(binding.assigneeID)}`,
        { method: "DELETE" }
      )
    })
  for (const entity of [...artifacts.entities].reverse())
    await attempt(async () => {
      await api(`/iam/entities/${encodeURIComponent(entity.id)}`, {
        method: "DELETE",
      })
    })
  for (const role of artifacts.roles)
    await attempt(async () => {
      await api(`/iam/roles/${encodeURIComponent(role.id)}`, {
        method: "DELETE",
      })
    })
  for (const group of artifacts.groups)
    await attempt(async () => {
      const current = await api(`/iam/groups/${encodeURIComponent(group.id)}`)
      await api(
        `/iam/groups/${encodeURIComponent(group.id)}?version=${current.version}`,
        { method: "DELETE" }
      )
    })
  if (artifacts.position)
    await attempt(async () => {
      const current = await api(
        `/iam/positions/${encodeURIComponent(artifacts.position.id)}`
      )
      await api(
        `/iam/positions/${encodeURIComponent(artifacts.position.id)}?version=${current.version}`,
        { method: "DELETE" }
      )
    })
  if (artifacts.department)
    await attempt(async () => {
      const current = await api(
        `/iam/departments/${encodeURIComponent(artifacts.department.id)}`
      )
      await api(
        `/iam/departments/${encodeURIComponent(artifacts.department.id)}?version=${current.version}`,
        { method: "DELETE" }
      )
    })
  if (errors.length)
    throw new Error(`cleanup failed: ${JSON.stringify(errors)}`)
}

await Promise.all([
  command("Network.enable"),
  command("Page.enable"),
  command("Runtime.enable"),
])
await command("Runtime.discardConsoleEntries")
await command("Network.clearBrowserCookies")
await command("Network.setCookie", {
  name: "NEXT_LOCALE",
  value: "zh-CN",
  url: baseURL,
})
await command("Emulation.setDeviceMetricsOverride", {
  width: 1440,
  height: 1000,
  deviceScaleFactor: 1,
  mobile: false,
})

let acceptanceError
let result
let acceptanceDiagnostics
try {
  await login(adminLogin, adminPassword)
  const principals = await api(
    `/iam/principals?limit=200&search=${encodeURIComponent(memberLogin)}`
  )
  const principal = principals.items.find(
    (item) => item.login_name === memberLogin
  )
  if (!principal) throw new Error("acceptance member was not found")
  artifacts.memberID = principal.id

  const suffix = Date.now().toString(36)
  artifacts.department = await api("/iam/departments", {
    method: "POST",
    body: JSON.stringify({
      name: `Acceptance Authorization Department ${suffix}`,
      status: "active",
      sort_order: 10,
    }),
  })
  artifacts.position = await api("/iam/positions", {
    method: "POST",
    body: JSON.stringify({
      code: `acceptance.authorization-${suffix}`,
      name: `Acceptance Authorization Position ${suffix}`,
      status: "active",
      sort_order: 10,
    }),
  })
  const staticGroup = await api("/iam/groups", {
    method: "POST",
    body: JSON.stringify({
      name: `Acceptance Authorization Static ${suffix}`,
      type: "static",
      description: "Authorization source acceptance",
      status: "active",
      sort_order: 10,
    }),
  })
  const dynamicGroup = await api("/iam/groups", {
    method: "POST",
    body: JSON.stringify({
      name: `Acceptance Authorization Dynamic ${suffix}`,
      type: "dynamic",
      membership_rule: {
        version: 1,
        match: "all",
        conditions: [
          {
            field: "member.subject",
            operator: "in",
            values: [principal.id],
          },
        ],
      },
      description: "Authorization source acceptance",
      status: "active",
      sort_order: 10,
    }),
  })
  artifacts.groups.push(staticGroup, dynamicGroup)

  const createRole = async (label, permission) => {
    const role = await api("/iam/roles", {
      method: "POST",
      body: JSON.stringify({
        name: `Acceptance Authorization ${label} ${suffix}`,
        description: "Authorization source acceptance",
      }),
    })
    artifacts.roles.push(role)
    await api(
      `/iam/roles/${encodeURIComponent(role.id)}/permissions/${encodeURIComponent(permission)}`,
      { method: "PUT" }
    )
    return role
  }
  const sharedRole = await createRole("Shared", "menu_view")
  const temporaryRole = await createRole("Temporary", "audit_event_view")
  const dynamicRole = await createRole("Dynamic", "position_view")
  const positionRole = await createRole("Position", "dept_view")
  const entityRole = await createRole("Entity", "store_view")

  await api(`/iam/roles/${encodeURIComponent(sharedRole.id)}/data-scope`, {
    method: "PUT",
    body: JSON.stringify({
      scope: "selected_departments",
      department_ids: [artifacts.department.id],
    }),
  })
  await api(
    `/iam/roles/${encodeURIComponent(sharedRole.id)}/members/${encodeURIComponent(principal.id)}`,
    { method: "PUT" }
  )
  artifacts.roleMembers.push(sharedRole.id)
  const staticEnd = new Date(Date.now() + 3 * 60 * 60 * 1000).toISOString()
  await api(
    `/iam/groups/${encodeURIComponent(staticGroup.id)}/members/${encodeURIComponent(principal.id)}`,
    { method: "PUT", body: JSON.stringify({ ends_at: staticEnd }) }
  )
  artifacts.groupMembers.push({ groupID: staticGroup.id })
  await api(
    `/iam/roles/${encodeURIComponent(sharedRole.id)}/directory-bindings/group/${encodeURIComponent(staticGroup.id)}`,
    { method: "PUT" }
  )
  artifacts.directoryBindings.push({
    roleID: sharedRole.id,
    type: "group",
    assigneeID: staticGroup.id,
  })
  await api(
    `/iam/roles/${encodeURIComponent(dynamicRole.id)}/directory-bindings/group/${encodeURIComponent(dynamicGroup.id)}`,
    { method: "PUT" }
  )
  artifacts.directoryBindings.push({
    roleID: dynamicRole.id,
    type: "group",
    assigneeID: dynamicGroup.id,
  })
  const positionEnd = new Date(Date.now() + 4 * 60 * 60 * 1000).toISOString()
  await api(
    `/iam/positions/${encodeURIComponent(artifacts.position.id)}/members/${encodeURIComponent(principal.id)}`,
    { method: "PUT", body: JSON.stringify({ ends_at: positionEnd }) }
  )
  artifacts.positionMembers.push({ positionID: artifacts.position.id })
  await api(
    `/iam/roles/${encodeURIComponent(positionRole.id)}/directory-bindings/position/${encodeURIComponent(artifacts.position.id)}`,
    { method: "PUT" }
  )
  artifacts.directoryBindings.push({
    roleID: positionRole.id,
    type: "position",
    assigneeID: artifacts.position.id,
  })

  const rootEntity = await api("/iam/entities", {
    method: "POST",
    body: JSON.stringify({
      type: "company",
      name: `Acceptance Authorization Root ${suffix}`,
    }),
  })
  const childEntity = await api("/iam/entities", {
    method: "POST",
    body: JSON.stringify({
      parent_id: rootEntity.id,
      type: "store",
      name: `Acceptance Authorization Child ${suffix}`,
    }),
  })
  artifacts.entities.push(rootEntity, childEntity)
  const entityEnd = new Date(Date.now() + 5 * 60 * 60 * 1000).toISOString()
  await api(
    `/iam/entities/${encodeURIComponent(rootEntity.id)}/role-bindings/${encodeURIComponent(entityRole.id)}/${encodeURIComponent(principal.id)}`,
    {
      method: "PUT",
      body: JSON.stringify({ effect: "allow", expires_at: entityEnd }),
    }
  )
  artifacts.entityBindings.push({
    entityID: rootEntity.id,
    roleID: entityRole.id,
  })
  await api(
    `/iam/entities/${encodeURIComponent(childEntity.id)}/role-bindings/${encodeURIComponent(entityRole.id)}/${encodeURIComponent(principal.id)}`,
    { method: "PUT", body: JSON.stringify({ effect: "deny" }) }
  )
  artifacts.entityBindings.push({
    entityID: childEntity.id,
    roleID: entityRole.id,
  })
  const businessResourceID = String(Date.now())
  artifacts.relationship = {
    entity_id: rootEntity.id,
    subject_type: "principal",
    subject_id: principal.id,
    relation: "viewer",
    resource_type: "store",
    resource_id: businessResourceID,
  }
  await api("/iam/relationships", {
    method: "POST",
    body: JSON.stringify(artifacts.relationship),
  })

  await logout()
  await login(memberLogin, memberPassword)
  artifacts.accessRequest = await api("/iam/access-requests", {
    method: "POST",
    body: JSON.stringify({
      role_id: temporaryRole.id,
      reason: `Authorization source acceptance ${suffix}`,
      access_expires_at: new Date(
        Date.now() + 2 * 60 * 60 * 1000
      ).toISOString(),
    }),
  })
  await logout()
  await login(adminLogin, adminPassword)
  artifacts.accessRequest = await api(
    `/iam/access-requests/${encodeURIComponent(artifacts.accessRequest.id)}/approve`,
    { method: "POST", body: JSON.stringify({ note: "Acceptance approval" }) }
  )

  const snapshot = await api(
    `/iam/members/${encodeURIComponent(principal.id)}/authorization-sources`
  )
  const permanentSource = requireSource(
    snapshot,
    "permanent_role",
    (source) => source.role_id === sharedRole.id,
    "direct permanent role"
  )
  const staticSource = requireSource(
    snapshot,
    "static_group",
    (source) =>
      source.role_id === sharedRole.id && source.source_id === staticGroup.id,
    "static group role"
  )
  requireSource(
    snapshot,
    "temporary_role",
    (source) =>
      source.role_id === temporaryRole.id && Boolean(source.expires_at),
    "temporary role with expiry"
  )
  requireSource(
    snapshot,
    "dynamic_group",
    (source) =>
      source.role_id === dynamicRole.id && source.source_id === dynamicGroup.id,
    "dynamic group role"
  )
  requireSource(
    snapshot,
    "position",
    (source) =>
      source.role_id === positionRole.id &&
      source.source_id === artifacts.position.id &&
      Boolean(source.expires_at),
    "position role with expiry"
  )
  requireSource(
    snapshot,
    "entity_binding",
    (source) =>
      source.role_id === entityRole.id &&
      source.scope_id === rootEntity.id &&
      source.effect === "allow" &&
      !source.inherited,
    "direct entity allow"
  )
  requireSource(
    snapshot,
    "entity_binding",
    (source) =>
      source.role_id === entityRole.id &&
      source.scope_id === childEntity.id &&
      source.effect === "allow" &&
      source.inherited,
    "inherited entity allow"
  )
  requireSource(
    snapshot,
    "entity_binding",
    (source) =>
      source.role_id === entityRole.id &&
      source.scope_id === childEntity.id &&
      source.effect === "deny" &&
      !source.inherited,
    "direct entity deny"
  )
  const relationshipSource = requireSource(
    snapshot,
    "relationship",
    (source) =>
      source.scope_id === businessResourceID &&
      source.relation === "viewer" &&
      source.relationship_path.some(
        (step) => step.resource_id === businessResourceID
      ),
    "business resource relationship path"
  )
  if (
    permanentSource.data_scope !== "selected_departments" ||
    !permanentSource.department_ids.includes(artifacts.department.id) ||
    !staticSource.expires_at ||
    !relationshipSource.permission_codes.includes("store_view")
  )
    throw new Error("authorization source details are incomplete")

  responses.length = 0
  failedRequests.length = 0
  consoleErrors.length = 0
  await command("Runtime.discardConsoleEntries")
  await navigate("/iam/users")
  await waitFor(`document.querySelector("h1")`)
  const inspectSelector = `[data-view-authorization-sources=${JSON.stringify(principal.id)}]`
  await waitFor(`document.querySelector(${JSON.stringify(inspectSelector)})`)
  await evaluate(
    `document.querySelector(${JSON.stringify(inspectSelector)}).click()`
  )
  await waitFor(
    `document.querySelector("[data-authorization-sources-sheet]") && document.querySelectorAll("[data-authorization-source]").length >= 9`
  )
  const rendered = await evaluate(`
    const sheet = document.querySelector("[data-authorization-sources-sheet]");
    return {
      text: sheet?.textContent ?? "",
      types: [...sheet.querySelectorAll("[data-authorization-source]")].map((row) => row.dataset.authorizationSource),
      expiryCount: [...sheet.querySelectorAll("[data-authorization-source]")].filter((row) => row.textContent.includes("到期时间")).length,
    };
  `)
  for (const expected of [
    sharedRole.name,
    staticGroup.name,
    dynamicGroup.name,
    artifacts.position.name,
    rootEntity.name,
    childEntity.name,
    businessResourceID,
    "允许",
    "拒绝",
    "viewer",
    "本次可信请求上下文",
  ])
    if (!rendered.text.includes(expected))
      throw new Error(`authorization sheet is missing ${expected}`)
  if (rendered.expiryCount < 3)
    throw new Error("authorization sheet does not show source expiries")

  const layouts = [
    await capture("member-authorization-desktop.png", 1440, 1000),
    await capture("member-authorization-mobile.png", 390, 844),
  ]
  await command("Emulation.setDeviceMetricsOverride", {
    width: 1440,
    height: 1000,
    deviceScaleFactor: 1,
    mobile: false,
  })
  await api(
    `/iam/roles/${encodeURIComponent(sharedRole.id)}/members/${encodeURIComponent(principal.id)}`,
    { method: "DELETE" }
  )
  artifacts.roleMembers = artifacts.roleMembers.filter(
    (roleID) => roleID !== sharedRole.id
  )
  await command("Input.dispatchKeyEvent", { type: "keyDown", key: "Escape" })
  await command("Input.dispatchKeyEvent", { type: "keyUp", key: "Escape" })
  await waitFor(`!document.querySelector("[data-authorization-sources-sheet]")`)
  await evaluate(
    `document.querySelector(${JSON.stringify(inspectSelector)}).click()`
  )
  await waitFor(`document.querySelector("[data-authorization-sources-sheet]")`)
  const refreshed = await api(
    `/iam/members/${encodeURIComponent(principal.id)}/authorization-sources`
  )
  if (
    findSource(
      refreshed,
      "permanent_role",
      (source) => source.role_id === sharedRole.id
    ) ||
    !findSource(
      refreshed,
      "static_group",
      (source) =>
        source.role_id === sharedRole.id && source.source_id === staticGroup.id
    )
  )
    throw new Error(
      "single-source removal changed the wrong authorization paths"
    )
  const refreshedRows = await evaluate(`
    return [...document.querySelectorAll("[data-authorization-source]")].map((row) => ({
      type: row.dataset.authorizationSource,
      role: row.dataset.authorizationRole,
      source: row.dataset.authorizationSourceId,
    }));
  `)
  if (
    refreshedRows.some(
      (row) => row.type === "permanent_role" && row.role === sharedRole.id
    ) ||
    !refreshedRows.some(
      (row) =>
        row.type === "static_group" &&
        row.role === sharedRole.id &&
        row.source === staticGroup.id
    )
  )
    throw new Error(
      "refreshed authorization sheet did not preserve other sources"
    )

  result = {
    sourceTypes: [...new Set(snapshot.sources.map((source) => source.type))],
    sourceCount: snapshot.sources.length,
    policyRevision: snapshot.revision,
    singleSourceRemovalPreservedAlternatives: true,
    layouts,
  }
  acceptanceDiagnostics = {
    responses: [...responses],
    failedRequests: [...failedRequests],
    consoleErrors: [...consoleErrors],
  }
} catch (error) {
  acceptanceError = error
}

try {
  await command("Emulation.setDeviceMetricsOverride", {
    width: 1440,
    height: 1000,
    deviceScaleFactor: 1,
    mobile: false,
  })
  if (!(await evaluate(`return location.pathname === "/login"`)))
    await apiRaw("/authn/sessions/current", { method: "DELETE" })
  await command("Network.clearBrowserCookies")
  await login(adminLogin, adminPassword)
  await cleanup()
} catch (error) {
  acceptanceError ??= error
}

const diagnostics = acceptanceDiagnostics ?? {
  responses,
  failedRequests,
  consoleErrors,
}
const unexpectedResponses = diagnostics.responses.filter(
  (response) =>
    response.status >= 400 &&
    !(
      response.status === 401 &&
      (response.method === "GET" || response.method === "DELETE") &&
      response.url.endsWith("/api/authn/sessions/current")
    ) &&
    !(
      response.status === 403 &&
      response.url.includes("/api/iam/tenants?include_deleted=false")
    )
)
const unexpectedFailures = diagnostics.failedRequests.filter(
  (failure) => !failure.canceled && failure.error !== "net::ERR_ABORTED"
)
socket.close()

if (acceptanceError) throw acceptanceError
if (diagnostics.consoleErrors.length)
  throw new Error(
    `console errors: ${JSON.stringify(diagnostics.consoleErrors)}`
  )
if (unexpectedFailures.length)
  throw new Error(`failed requests: ${JSON.stringify(unexpectedFailures)}`)
if (unexpectedResponses.length)
  throw new Error(
    `unexpected responses: ${JSON.stringify(unexpectedResponses)}`
  )

console.log(JSON.stringify(result))
