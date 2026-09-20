import { randomBytes } from "node:crypto"
import { existsSync, readFileSync, writeFileSync } from "node:fs"
import { join } from "node:path"

const apiURL = process.env.IAM_API_URL?.replace(/\/$/u, "")
const consoleURL = process.env.IAM_CONSOLE_URL?.replace(/\/$/u, "")
const tenantID = process.env.IAM_BROWSER_TENANT
const sourceTenantID = process.env.IAM_PERSONA_SOURCE_TENANT
const directory = process.env.IAM_UAT_DIRECTORY
const adminPasswordFile = process.env.IAM_BROWSER_PASSWORD_FILE
if (!apiURL || !consoleURL || !tenantID || !directory || !adminPasswordFile)
  throw new Error("Missing file persona bootstrap environment")

let cookie = ""
async function call(
  path,
  { method = "GET", body, tenant = true, tenantHeader = tenantID } = {}
) {
  const resourcePath = tenant
    ? `/tenants/${encodeURIComponent(tenantHeader)}${path}`
    : path
  const response = await fetch(apiURL + resourcePath, {
    method,
    redirect: "manual",
    headers: {
      "Content-Type": "application/json",
      Origin: consoleURL,
      ...(cookie ? { Cookie: cookie } : {}),
    },
    ...(body === undefined ? {} : { body: JSON.stringify(body) }),
  })
  const issued = response.headers.getSetCookie?.() ?? []
  if (issued.length)
    cookie = issued.map((value) => value.split(";", 1)[0]).join("; ")
  const payload = await response.json().catch(() => null)
  if (!response.ok)
    throw new Error(
      `${method} ${resourcePath}: ${response.status} ${payload?.message ?? ""}`
    )
  return payload?.data
}

await call("/sessions", {
  method: "POST",
  tenant: false,
  body: {
    login_name: "admin@example.test",
    password: readFileSync(adminPasswordFile, "utf8").trim(),
    return_url: consoleURL + "/",
  },
})

function passwordFor(id) {
  const path = join(directory, `${id}-password`)
  if (!existsSync(path))
    writeFileSync(path, randomBytes(24).toString("base64url") + "aA1!\n", {
      mode: 0o600,
    })
  return { path, password: readFileSync(path, "utf8").trim() }
}

async function ensureFromList(path, match, create) {
  const listed = await call(path)
  const items = Array.isArray(listed) ? listed : (listed?.items ?? [])
  return items.find(match) ?? call(path, { method: "POST", body: create })
}

const entity = await ensureFromList(
  "/entities",
  (item) => item.name === "UAT File Company",
  { name: "UAT File Company", type: "unit", status: "active", metadata: {} }
)
const departments = {}
for (const name of ["UAT Finance", "UAT Operations"]) {
  departments[name] = await ensureFromList(
    "/departments?limit=200",
    (item) => item.name === name,
    { name, entity_id: entity.id, status: "active", sort_order: 1 }
  )
}

const definitions = [
  {
    id: "system-admin",
    role: "系统子管理员",
    loginName: "system-admin@example.test",
    displayName: "UAT System Administrator",
    scope: "all",
    platformPermissions: ["tenant_view"],
  },
  {
    id: "tenant-admin",
    role: "租户管理员",
    loginName: "tenant-admin@example.test",
    displayName: "UAT Tenant Administrator",
    scope: "all",
    tenantAdmin: true,
  },
  {
    id: "entity-account",
    role: "实体子账号",
    loginName: "entity-account@example.test",
    displayName: "UAT Entity Account",
    scope: "all",
    entityBinding: true,
  },
  {
    id: "department-account",
    role: "部门子账号（财务部）",
    loginName: "department-account@example.test",
    displayName: "UAT Department Account",
    scope: "department",
    departmentID: departments["UAT Finance"].id,
  },
]

const filePermissions = [
  "entity_view",
  "dept_view",
  "file_view",
  "file_read_all",
  "file_list",
  "file_traverse",
  "file_read",
  "file_download",
  "file_create",
  "file_delete",
]
const accounts = [
  {
    id: "super-admin",
    role: "系统唯一超级管理员",
    loginName: "admin@example.test",
    password: readFileSync(adminPasswordFile, "utf8").trim(),
  },
]

for (const definition of definitions) {
  const credential = passwordFor(definition.id)
  const principalPath =
    "/principals?limit=200&search=" + encodeURIComponent(definition.loginName)
  const localPrincipals = await call(principalPath)
  let principal = localPrincipals.items.find(
    (item) => item.login_name === definition.loginName
  )
  if (!principal && sourceTenantID) {
    const sourcePrincipals = await call(principalPath, {
      tenantHeader: sourceTenantID,
    })
    principal = sourcePrincipals.items.find(
      (item) => item.login_name === definition.loginName
    )
    if (principal)
      await call("/members", {
        method: "POST",
        body: { subject: principal.id, display_name: definition.displayName },
      })
  }
  principal ??= await call("/principals", {
    method: "POST",
    body: {
      login_name: definition.loginName,
      password: credential.password,
      display_name: definition.displayName,
    },
  })
  await call(`/members/${principal.id}`, {
    method: "PATCH",
    body: {
      display_name: definition.displayName,
      status: "active",
      ...(definition.departmentID
        ? { department_id: definition.departmentID }
        : {}),
    },
  })
  const role = await ensureFromList(
    "/roles?limit=200",
    (item) => item.name === `UAT Files: ${definition.role}`,
    {
      name: `UAT Files: ${definition.role}`,
      description: "Stable local file-visibility acceptance persona",
    }
  )
  for (const permission of definition.tenantAdmin
    ? ["tenant_administer"]
    : filePermissions)
    await call(`/roles/${role.id}/permissions/${permission}`, { method: "PUT" })
  await call(`/roles/${role.id}/data-scope`, {
    method: "PUT",
    body: { scope: definition.scope, department_ids: [] },
  })
  if (definition.entityBinding) {
    // The console resolves its entity catalog and menu before it can attach an
    // entity context. Keep those discovery permissions separate so tenant-level
    // membership cannot expose the tenant file root.
    const discoveryRole = await ensureFromList(
      "/roles?limit=200",
      (item) => item.name === "UAT Files: 实体账号入口",
      {
        name: "UAT Files: 实体账号入口",
        description: "Read-only console discovery for the entity persona",
      }
    )
    for (const permission of ["entity_view", "file_view"])
      await call(`/roles/${discoveryRole.id}/permissions/${permission}`, {
        method: "PUT",
      })
    await call(`/roles/${discoveryRole.id}/data-scope`, {
      method: "PUT",
      body: { scope: "self", department_ids: [] },
    })
    await call(`/roles/${discoveryRole.id}/members/${principal.id}`, {
      method: "PUT",
    })
    const labelRole = await ensureFromList(
      "/roles?limit=200",
      (item) => item.name === "UAT Files: 实体账号部门标签",
      {
        name: "UAT Files: 实体账号部门标签",
        description: "Read-only names for departments visible in entity files",
      }
    )
    await call(`/roles/${labelRole.id}/permissions/dept_view`, {
      method: "PUT",
    })
    await call(`/roles/${labelRole.id}/data-scope`, {
      method: "PUT",
      body: {
        scope: "selected_departments",
        department_ids: Object.values(departments).map(
          (department) => department.id
        ),
      },
    })
    await call(`/roles/${labelRole.id}/members/${principal.id}`, {
      method: "PUT",
    })
    await call(
      `/entities/${entity.id}/role-bindings/${role.id}/${principal.id}`,
      {
        method: "PUT",
        body: { effect: "allow" },
      }
    )
  } else
    await call(`/roles/${role.id}/members/${principal.id}`, { method: "PUT" })
  if (definition.platformPermissions)
    await call(`/platform/administrators/${principal.id}`, {
      method: "PUT",
      tenant: false,
      body: {
        full_administrator: false,
        permissions: definition.platformPermissions,
      },
    })
  accounts.push({
    id: definition.id,
    role: definition.role,
    loginName: definition.loginName,
    password: credential.password,
    tenantID,
  })
}

accounts[0].tenantID = tenantID

process.stdout.write(
  JSON.stringify({ accounts, tenantID, entityID: entity.id, departments }) +
    "\n"
)
