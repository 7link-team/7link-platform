import { auditPassword } from "./audit-password.mjs"

const port = Number(process.argv[2] ?? 9333)
const baseURL = process.argv[3] ?? "http://localhost:8091"
const password = auditPassword()
const adminLogin = process.env.IAM_BROWSER_LOGIN ?? "admin@example.test"
const tenantID = process.env.IAM_BROWSER_TENANT
const screenshotRoot = new URL(
  "../../../../../.local/screenshots/",
  import.meta.url
)

if (!tenantID) throw new Error("IAM_BROWSER_TENANT is required")

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

async function waitForResponse(method, path, fromIndex, timeout = 15000) {
  const deadline = Date.now() + timeout
  while (Date.now() < deadline) {
    const response = responses
      .slice(fromIndex)
      .find(
        (candidate) =>
          candidate.method === method &&
          new URL(candidate.url).pathname === `/api${path}`
      )
    if (response) {
      if (response.status < 200 || response.status >= 300)
        throw new Error(`${method} ${path} failed with ${response.status}`)
      return
    }
    await Bun.sleep(100)
  }
  throw new Error(`Timed out waiting for: ${method} ${path}`)
}

async function navigate(path) {
  await command("Page.navigate", { url: `${baseURL}${path}` })
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

async function click(selector) {
  await clickElement(`document.querySelector(${JSON.stringify(selector)})`)
}

async function clickElement(expression) {
  await waitFor(
    `(${expression}) && !(${expression}).matches(':disabled, [aria-disabled="true"]')`
  )
  const point = await evaluate(`
    const element = ${expression};
    element.scrollIntoView({ block: "center" });
    const rect = element.getBoundingClientRect();
    return { x: rect.left + rect.width / 2, y: rect.top + rect.height / 2 };
  `)
  await command("Input.dispatchMouseEvent", { type: "mouseMoved", ...point })
  await command("Input.dispatchMouseEvent", {
    type: "mousePressed",
    ...point,
    button: "left",
    clickCount: 1,
  })
  await command("Input.dispatchMouseEvent", {
    type: "mouseReleased",
    ...point,
    button: "left",
    clickCount: 1,
  })
}

async function apiRaw(path, options = {}) {
  return evaluate(`
    const response = await fetch(${JSON.stringify(`/api${path}`)}, {
      credentials: "include",
      headers: {
        "Content-Type": "application/json",
      },
      ...${JSON.stringify(options)},
    });
    const payload = await response.json();
    return { status: response.status, payload };
  `)
}

async function api(path, options = {}) {
  const result = await apiRaw(path, options)
  if (result.status < 200 || result.status >= 300)
    throw new Error(
      `${path} failed: ${result.status} ${result.payload?.message ?? ""}`
    )
  return result.payload.data
}

async function cleanup() {
  for (const role of (await api("/roles")).filter((item) =>
    item.name.startsWith("Acceptance Directory Role ")
  )) {
    for (const binding of await api(
      `/roles/${encodeURIComponent(role.id)}/directory-bindings`
    ))
      await api(
        `/roles/${encodeURIComponent(role.id)}/directory-bindings/${binding.assignee_type}/${encodeURIComponent(binding.assignee_id)}`,
        { method: "DELETE" }
      )
    for (const grant of await api(
      `/roles/${encodeURIComponent(role.id)}/permission-grants`
    ))
      await api(
        `/roles/${encodeURIComponent(role.id)}/permissions/${encodeURIComponent(grant.permission_code)}`,
        { method: "DELETE" }
      )
    for (const subject of await api(
      `/roles/${encodeURIComponent(role.id)}/members`
    ))
      await api(
        `/roles/${encodeURIComponent(role.id)}/members/${encodeURIComponent(subject)}`,
        { method: "DELETE" }
      )
    await api(`/roles/${encodeURIComponent(role.id)}`, {
      method: "DELETE",
    })
  }
  for (const group of (await api("/principal-groups")).filter((item) =>
    item.name.startsWith("Acceptance Role Group ")
  ))
    await api(
      `/principal-groups/${encodeURIComponent(group.id)}?version=${group.version}`,
      {
        method: "DELETE",
      }
    )
  for (const department of (await api("/departments")).filter((item) =>
    item.name.startsWith("Acceptance Role Department ")
  ))
    await api(
      `/departments/${encodeURIComponent(department.id)}?version=${department.version}`,
      { method: "DELETE" }
    )
}

async function selectRoleView(view) {
  await click(`[data-role-view="${view}"]`)
  await waitFor(
    `document.querySelector('[data-role-view="${view}"]')?.hasAttribute("data-active")`
  )
  await waitFor(
    view === "permissions"
      ? `!new URLSearchParams(location.search).has("view")`
      : `new URLSearchParams(location.search).get("view") === ${JSON.stringify(view)}`
  )
}

async function captureRoleWorkspace(
  name,
  width,
  height,
  view,
  roleID,
  focusSelector = ""
) {
  await waitFor(
    `document.querySelector('[data-role-count="permissions"]')?.textContent.trim() === "1" && document.querySelector('[data-role-count="assignments"]')?.textContent.trim() === "3"`
  )
  await command("Emulation.setDeviceMetricsOverride", {
    width,
    height,
    deviceScaleFactor: 1,
    mobile: width < 600,
  })
  if (focusSelector)
    await evaluate(`
      const target = document.querySelector(${JSON.stringify(focusSelector)});
      if (!(target instanceof HTMLElement)) throw new Error("capture target not found");
      target.scrollIntoView({ block: "start" });
    `)
  await Bun.sleep(250)
  const layout = await evaluate(`
    const tab = document.querySelector('[data-role-view=${JSON.stringify(view)}]');
    const panel = document.querySelector('[data-role-panel=${JSON.stringify(view)}]');
    const panelRect = panel?.getBoundingClientRect();
    return {
      view: ${JSON.stringify(view)},
      width: innerWidth,
      scrollWidth: document.documentElement.scrollWidth,
      horizontalOverflow: document.documentElement.scrollWidth > innerWidth,
      activeTab: Boolean(tab?.hasAttribute("data-active")),
      panelVisible: Boolean(panel && !panel.hidden && panelRect && panelRect.width > 0 && panelRect.height > 0),
      roleDeepLink: new URLSearchParams(location.search).get("role") === ${JSON.stringify(roleID)},
      viewDeepLink: ${JSON.stringify(view)} === "permissions"
        ? !new URLSearchParams(location.search).has("view")
        : new URLSearchParams(location.search).get("view") === ${JSON.stringify(view)},
      permissionCount: document.querySelector('[data-role-count="permissions"]')?.textContent.trim(),
      assignmentCount: document.querySelector('[data-role-count="assignments"]')?.textContent.trim(),
      directoryLinks: document.querySelectorAll('[data-role-directory-link]').length,
      directoryHrefs: [...document.querySelectorAll('[data-role-directory-link]')]
        .map((link) => link.getAttribute("href"))
        .sort(),
      permissionConditionSummary: Boolean(document.querySelector('[data-permission-condition-summary]')),
    };
  `)
  if (
    layout.horizontalOverflow ||
    !layout.activeTab ||
    !layout.panelVisible ||
    !layout.roleDeepLink ||
    !layout.viewDeepLink ||
    layout.permissionCount !== "1" ||
    layout.assignmentCount !== "3" ||
    (view === "permissions" && !layout.permissionConditionSummary) ||
    (view === "assignments" &&
      (layout.directoryLinks !== 2 ||
        JSON.stringify(layout.directoryHrefs) !==
          JSON.stringify(["/groups", "/users"])))
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

async function captureDirectoryRoles(name, width, height, kind, roleID) {
  await command("Emulation.setDeviceMetricsOverride", {
    width,
    height,
    deviceScaleFactor: 1,
    mobile: width < 600,
  })
  await Bun.sleep(250)
  const layout = await evaluate(`
    const dialog = document.querySelector('[data-directory-role-dialog=${JSON.stringify(kind)}]');
    const checkbox = document.querySelector('[data-directory-role-id=${JSON.stringify(roleID)}]');
    const rect = dialog?.getBoundingClientRect();
    return {
      width: innerWidth,
      scrollWidth: document.documentElement.scrollWidth,
      horizontalOverflow: document.documentElement.scrollWidth > innerWidth,
      dialogVisible: Boolean(dialog && rect && rect.width > 0 && rect.height > 0),
      dialogWithinViewport: Boolean(rect && rect.left >= 0 && rect.right <= innerWidth && rect.top >= 0 && rect.bottom <= innerHeight),
      roleChecked: checkbox?.getAttribute("aria-checked") === "true",
    };
  `)
  if (
    layout.horizontalOverflow ||
    !layout.dialogVisible ||
    !layout.dialogWithinViewport ||
    !layout.roleChecked
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

await Promise.all([
  command("Network.enable"),
  command("Page.enable"),
  command("Runtime.enable"),
])
await command("Network.clearBrowserCookies")
await command("Emulation.setDeviceMetricsOverride", {
  width: 1440,
  height: 1000,
  deviceScaleFactor: 1,
  mobile: false,
})

await navigate("/roles")
await waitFor(
  `location.pathname === "/login" && document.querySelector("#login-name")`
)
await setInput("#login-name", adminLogin)
await setInput("#password", password)
await evaluate(`document.querySelector("form").requestSubmit()`)
await waitFor(`location.pathname === "/roles"`, 20000)
await waitFor(`document.querySelector("h1")`)
await cleanup()

const suffix = Date.now().toString(36)
const roleName = `Acceptance Directory Role ${suffix}`
const groupName = `Acceptance Role Group ${suffix}`
const secondGroupType = "static"
const secondGroupName = `Acceptance Role Group B ${suffix}`
const departmentName = `Acceptance Role Department ${suffix}`
const group = await api("/principal-groups", {
  method: "POST",
  body: JSON.stringify({
    name: groupName,
    type: "static",
    description: "Role assignment acceptance",
    status: "active",
    sort_order: 10,
  }),
})
const secondGroup = await api("/principal-groups", {
  method: "POST",
  body: JSON.stringify({
    type: secondGroupType,
    name: secondGroupName,
    status: "active",
    sort_order: 10,
  }),
})
const department = await api("/departments", {
  method: "POST",
  body: JSON.stringify({
    name: departmentName,
    status: "active",
    sort_order: 10,
  }),
})
const member = (await api("/members")).find((item) => item.status === "active")
if (!member) throw new Error("active acceptance member was not returned")

await click("header:has(h1) button")
await waitFor(`document.querySelector("#role-form")`)
await setInput("#role-name", roleName)
await setInput("#role-description", "Directory-derived permissions")
await click('button[form="role-form"][type="submit"]')
await waitFor(`!document.querySelector("#role-form")`)
await waitFor(
  `[...document.querySelectorAll("button strong")].some((item) => item.textContent.trim() === ${JSON.stringify(roleName)})`
)

const persistedRoles = await api("/roles")
const role = persistedRoles.find((item) => item.name === roleName)
if (!role) throw new Error("created role was not returned")
const alternateRole = persistedRoles.find((item) => item.id !== role.id)
if (!alternateRole) throw new Error("an alternate role is required")
await waitFor(
  `[...document.querySelectorAll("h2")].some((item) => item.textContent.trim() === ${JSON.stringify(roleName)})`
)
await waitFor(
  `new URLSearchParams(location.search).get("role") === ${JSON.stringify(role.id)}`
)
const alternateRoleButton = `[...document.querySelectorAll("button strong")].find((item) => item.textContent.trim() === ${JSON.stringify(alternateRole.name)})?.closest("button")`
await waitFor(alternateRoleButton)
await evaluate(`${alternateRoleButton}.click()`)
await waitFor(
  `new URLSearchParams(location.search).get("role") === ${JSON.stringify(alternateRole.id)} && [...document.querySelectorAll("h2")].some((item) => item.textContent.trim() === ${JSON.stringify(alternateRole.name)})`
)
await evaluate(`history.back()`)
await waitFor(
  `new URLSearchParams(location.search).get("role") === ${JSON.stringify(role.id)} && [...document.querySelectorAll("h2")].some((item) => item.textContent.trim() === ${JSON.stringify(roleName)})`
)

await selectRoleView("scope")
await waitFor(`document.querySelector("#role-data-scope")`)
await click("#role-data-scope")
const selectedDepartmentsOption = `[...document.querySelectorAll('[role="option"]')].find((item) => item.textContent.trim() === "指定部门")`
await waitFor(selectedDepartmentsOption)
await clickElement(selectedDepartmentsOption)
const departmentSelector = `[data-scope-department-id=${JSON.stringify(department.id)}]`
await waitFor(`document.querySelector(${JSON.stringify(departmentSelector)})`)
await click(departmentSelector)
await waitFor(
  `document.querySelector(${JSON.stringify(departmentSelector)})?.getAttribute("aria-checked") === "true"`
)
const dataScopeResponseIndex = responses.length
await click("[data-save-data-scope]")
await waitForResponse(
  "PUT",
  `/roles/${encodeURIComponent(role.id)}/data-scope`,
  dataScopeResponseIndex
)
await waitFor(
  `document.querySelector(${JSON.stringify(departmentSelector)})?.getAttribute("aria-checked") === "true"`
)
const dataScope = await api(`/roles/${encodeURIComponent(role.id)}/data-scope`)
if (
  dataScope.scope !== "selected_departments" ||
  dataScope.department_ids.length !== 1 ||
  dataScope.department_ids[0] !== department.id
)
  throw new Error("role data scope did not persist")

await selectRoleView("permissions")
const permissionSelector = `document.querySelector('[data-permission-code="menu_view"] [role="checkbox"]')`
await waitFor(permissionSelector)
await clickElement(permissionSelector)
await waitFor(`${permissionSelector}?.getAttribute("aria-checked") === "true"`)
if (
  !(await api(`/roles/${encodeURIComponent(role.id)}/permission-grants`)).some(
    (grant) => grant.permission_code === "menu_view"
  )
)
  throw new Error("permission grant did not persist")

await click('[data-edit-permission-condition="menu_view"]')
await waitFor(`document.querySelector("#role-permission-condition-form")`)
await click("#permission-minimum-acr")
await waitFor(
  `document.querySelector('[role="option"][data-value="2"]') || [...document.querySelectorAll('[role="option"]')].some(item => /2/.test(item.textContent))`
)
await clickElement(
  `[...document.querySelectorAll('[role="option"]')].find(item => /2/.test(item.textContent))`
)
await setInput("#permission-client-id", "admin-console")
await setInput("#permission-network-zone", "corporate")
await setInput("#permission-time-start", "08:00")
await setInput("#permission-time-end", "18:00")
await setInput("#permission-timezone", "Asia/Shanghai")
await click("[data-save-permission-condition]")
await waitFor(`!document.querySelector("#role-permission-condition-form")`)
const permissionGrant = (
  await api(`/roles/${encodeURIComponent(role.id)}/permission-grants`)
).find((grant) => grant.permission_code === "menu_view")
if (
  !permissionGrant?.condition?.all ||
  permissionGrant.condition.all.length !== 4
)
  throw new Error("permission condition did not persist")
await waitFor(
  `document.querySelector('[data-permission-condition-summary]')?.textContent.includes("ACR >= 2")`
)

await selectRoleView("assignments")
const memberSelector = `[data-role-member-id=${JSON.stringify(member.subject)}]`
await waitFor(`document.querySelector(${JSON.stringify(memberSelector)})`)
await click(memberSelector)
await waitFor(
  `document.querySelector(${JSON.stringify(memberSelector)})?.getAttribute("aria-checked") === "true"`
)
if (
  !(await api(`/roles/${encodeURIComponent(role.id)}/members`)).includes(
    member.subject
  )
)
  throw new Error("direct role member assignment did not persist")
for (const [kind, item] of [
  ["group", group],
  ["group", secondGroup],
]) {
  const selector = `[data-assignee-type=${JSON.stringify(kind)}][data-assignee-id=${JSON.stringify(item.id)}]`
  await waitFor(`document.querySelector(${JSON.stringify(selector)})`)
  await click(selector)
  await waitFor(
    `document.querySelector(${JSON.stringify(selector)})?.getAttribute("aria-checked") === "true"`
  )
}

const bindings = await api(
  `/roles/${encodeURIComponent(role.id)}/directory-bindings`
)
if (
  bindings.length !== 2 ||
  !bindings.some(
    (binding) =>
      binding.assignee_type === "group" && binding.assignee_id === group.id
  ) ||
  !bindings.some(
    (binding) =>
      binding.assignee_type === "group" &&
      binding.assignee_id === secondGroup.id
  )
)
  throw new Error("directory bindings did not persist")
await waitFor(
  `document.querySelector('[data-role-count="permissions"]')?.textContent.trim() === "1" && document.querySelector('[data-role-count="assignments"]')?.textContent.trim() === "3"`
)

const reverseLayouts = []
for (const [kind, item, path] of [
  ["group", group, "/principal-groups"],
  ["group", secondGroup, "/principal-groups"],
]) {
  await navigate(path)
  await waitFor(`document.querySelector("h1")`)
  const manageButton = `[...document.querySelectorAll("tr")].find((row) => row.textContent.includes(${JSON.stringify(item.name)}))?.querySelector('[data-manage-directory-roles=${JSON.stringify(kind)}]')`
  await waitFor(manageButton)
  await evaluate(`${manageButton}.click()`)
  const reverseSelector = `[data-directory-role-id=${JSON.stringify(role.id)}]`
  await waitFor(
    `document.querySelector(${JSON.stringify(reverseSelector)})?.getAttribute("aria-checked") === "true"`
  )
  await click(reverseSelector)
  await waitFor(
    `document.querySelector(${JSON.stringify(reverseSelector)})?.getAttribute("aria-checked") === "false"`
  )
  const removedBindings = await api(
    `/roles/${encodeURIComponent(role.id)}/directory-bindings`
  )
  if (
    removedBindings.some(
      (binding) =>
        binding.assignee_type === kind && binding.assignee_id === item.id
    )
  )
    throw new Error(`${kind} reverse role removal did not persist`)
  await click(reverseSelector)
  await waitFor(
    `document.querySelector(${JSON.stringify(reverseSelector)})?.getAttribute("aria-checked") === "true"`
  )
  const restoredBindings = await api(
    `/roles/${encodeURIComponent(role.id)}/directory-bindings`
  )
  if (
    !restoredBindings.some(
      (binding) =>
        binding.assignee_type === kind && binding.assignee_id === item.id
    )
  )
    throw new Error(`${kind} reverse role assignment did not persist`)
  reverseLayouts.push(
    await captureDirectoryRoles(
      `${kind}-role-bindings-desktop.png`,
      1440,
      1000,
      kind,
      role.id
    ),
    await captureDirectoryRoles(
      `${kind}-role-bindings-mobile.png`,
      390,
      844,
      kind,
      role.id
    )
  )
  await command("Emulation.setDeviceMetricsOverride", {
    width: 1440,
    height: 1000,
    deviceScaleFactor: 1,
    mobile: false,
  })
  await click(`[data-open-role-id="${role.id}"]`)
  await waitFor(
    `location.pathname === "/roles" && new URLSearchParams(location.search).get("role") === ${JSON.stringify(role.id)} && new URLSearchParams(location.search).get("view") === "assignments"`
  )
  await waitFor(
    `[...document.querySelectorAll("h2")].some((item) => item.textContent.trim() === ${JSON.stringify(roleName)}) && document.querySelector('[data-role-view="assignments"]')?.hasAttribute("data-active")`
  )
}

await navigate(`/roles?role=${encodeURIComponent(role.id)}`)
await waitFor(`document.querySelector("h1")`)
await waitFor(
  `[...document.querySelectorAll("h2")].some((item) => item.textContent.trim() === ${JSON.stringify(roleName)}) && document.querySelector('[data-role-view="permissions"]')?.hasAttribute("data-active")`
)

const groupDelete = await apiRaw(
  `/groups/${encodeURIComponent(group.id)}?version=${group.version}`,
  { method: "DELETE" }
)
const secondGroupDelete = await apiRaw(
  `/principal-groups/${encodeURIComponent(secondGroup.id)}?version=${secondGroup.version}`,
  { method: "DELETE" }
)
if (groupDelete.status !== 409 || secondGroupDelete.status !== 409)
  throw new Error("role-bound directory deletion was not protected")

const layouts = []
await selectRoleView("permissions")
layouts.push(
  await captureRoleWorkspace(
    "role-permissions-desktop.png",
    1440,
    1000,
    "permissions",
    role.id,
    "[data-role-workspace-tabs]"
  ),
  await captureRoleWorkspace(
    "role-permissions-mobile.png",
    390,
    844,
    "permissions",
    role.id,
    "[data-role-workspace-tabs]"
  )
)
await selectRoleView("assignments")
layouts.push(
  await captureRoleWorkspace(
    "role-assignments-desktop.png",
    1440,
    1000,
    "assignments",
    role.id,
    "[data-role-workspace-tabs]"
  ),
  await captureRoleWorkspace(
    "role-assignments-mobile.png",
    390,
    844,
    "assignments",
    role.id,
    "[data-role-workspace-tabs]"
  )
)
await selectRoleView("scope")
layouts.push(
  await captureRoleWorkspace(
    "role-scope-desktop.png",
    1440,
    1000,
    "scope",
    role.id,
    "[data-role-workspace-tabs]"
  ),
  await captureRoleWorkspace(
    "role-scope-mobile.png",
    390,
    844,
    "scope",
    role.id,
    "[data-role-workspace-tabs]"
  )
)
await command("Emulation.setDeviceMetricsOverride", {
  width: 1440,
  height: 1000,
  deviceScaleFactor: 1,
  mobile: false,
})

await selectRoleView("assignments")
for (const [kind, item] of [
  ["group", group],
  ["group", secondGroup],
]) {
  const selector = `[data-assignee-type=${JSON.stringify(kind)}][data-assignee-id=${JSON.stringify(item.id)}]`
  await click(selector)
  await waitFor(
    `document.querySelector(${JSON.stringify(selector)})?.getAttribute("aria-checked") === "false"`
  )
}
await click(memberSelector)
await waitFor(
  `document.querySelector(${JSON.stringify(memberSelector)})?.getAttribute("aria-checked") === "false"`
)
if (
  (await api(`/roles/${encodeURIComponent(role.id)}/members`)).includes(
    member.subject
  )
)
  throw new Error("direct role member removal did not persist")

await selectRoleView("permissions")
await click('[data-edit-permission-condition="menu_view"]')
await waitFor(`document.querySelector("[data-clear-permission-condition]")`)
await click("[data-clear-permission-condition]")
await waitFor(`!document.querySelector("#role-permission-condition-form")`)
const clearedPermissionGrant = (
  await api(`/roles/${encodeURIComponent(role.id)}/permission-grants`)
).find((grant) => grant.permission_code === "menu_view")
if (!clearedPermissionGrant || clearedPermissionGrant.condition)
  throw new Error("permission condition did not clear")

const audit = await api("/audit-events?limit=200")
for (const type of [
  "role_directory_binding_added",
  "role_directory_binding_removed",
]) {
  const count = audit.filter(
    (event) => event.event_type === type && event.target_id === role.id
  ).length
  if (count !== 4)
    throw new Error(`expected four ${type} audit events, got ${count}`)
}
const scopeAuditCount = audit.filter(
  (event) =>
    event.event_type === "role_data_scope_updated" &&
    event.target_id === role.id
).length
if (scopeAuditCount !== 1)
  throw new Error(
    `expected one role_data_scope_updated audit event, got ${scopeAuditCount}`
  )
const conditionAuditCount = audit.filter(
  (event) =>
    event.event_type === "role_permission_condition_updated" &&
    event.target_id === role.id
).length
if (conditionAuditCount !== 2)
  throw new Error(
    `expected two role_permission_condition_updated audit events, got ${conditionAuditCount}`
  )
for (const type of ["role_member_added", "role_member_removed"]) {
  const count = audit.filter(
    (event) => event.event_type === type && event.target_id === role.id
  ).length
  if (count !== 1)
    throw new Error(`expected one ${type} audit event, got ${count}`)
}

await cleanup()
const unexpectedResponses = responses.filter(
  (response) =>
    response.status >= 400 && response.status !== 401 && response.status !== 409
)
const unexpectedFailures = failedRequests.filter((request) => !request.canceled)
socket.close()
if (consoleErrors.length > 0)
  throw new Error(`console errors: ${consoleErrors.join("; ")}`)
if (unexpectedFailures.length > 0)
  throw new Error(`failed requests: ${JSON.stringify(unexpectedFailures)}`)
if (unexpectedResponses.length > 0)
  throw new Error(
    `unexpected responses: ${JSON.stringify(unexpectedResponses)}`
  )
console.log(
  JSON.stringify(
    {
      roleID: role.id,
      groupID: group.id,
      secondGroupID: secondGroup.id,
      departmentID: department.id,
      layouts,
      reverseLayouts,
      auditEvents: 13,
      consoleErrors,
      failedRequests: unexpectedFailures,
      unexpectedResponses,
    },
    null,
    2
  )
)
