import assert from "node:assert/strict"

export async function systemWorkspaceAcceptance({
  admin,
  request,
  createAccount,
  systemTarget,
  login,
  contexts,
  eventually,
  pass,
  openUI: openAccountView,
  exitUI: exitAccountView,
  statement,
  operator,
  ownerA,
  platformA,
  tenantB,
  ownerContext,
  capture,
  client,
}) {
  let page = admin.page
  const openUI = async (...args) => {
    await openAccountView(...args)
    page = admin.page
  }
  const exitUI = async () => {
    await exitAccountView()
    page = admin.page
  }
  const select = async (id, name) => {
    await page.locator(id).click()
    await page.getByRole("option", { name, exact: true }).click()
  }
  const desktop = () => page.setViewportSize({ width: 1440, height: 1000 })
  const worker = await createAccount("system-workspace-worker")
  let department, role
  await desktop()
  for (const [category, heading, owner] of [
    ["system", "System accounts"],
    ["platform", "Platform accounts", platformA],
    ["tenant", "Tenant accounts", tenantB],
    ["user", "User accounts"],
  ]) {
    await page.goto(
      category === "user" ? "/system/accounts" : `/system/accounts/${category}`
    )
    await page.getByRole("heading", { name: heading, exact: true }).waitFor()
    assert.equal(
      await page
        .getByRole("navigation", { name: "Main navigation", exact: true })
        .getByRole("link", { name: "User accounts", exact: true })
        .getAttribute("aria-current"),
      category === "user" ? "page" : null
    )
    if (owner) {
      await select("#account-owner", owner.name)
      await page.locator(`[data-account-id="${ownerA.id}"]`).waitFor()
      const result = await request(
        `/system/principals?category=${category}&owner_id=${owner.id}`
      )
      assert(result.items.some((item) => item.id === ownerA.id))
      assert.equal(result.total, result.items.length)
    }
    if (category === "user")
      await page.locator(`[data-account-id="${worker.id}"]`).waitFor()
    await capture(page, `system-${category}-accounts`)
    await desktop()
  }
  pass(
    "system account categories",
    "same global account appears in explicit platform and tenant scopes; server totals and four account pages render on desktop/mobile"
  )

  await page.goto("/system/departments")
  await page
    .getByRole("button", { name: "Create department", exact: true })
    .click()
  await page.locator("#department-name").fill("System acceptance department")
  await page.locator('[form="department-form"][type="submit"]').click()
  await page.getByRole("dialog").waitFor({ state: "hidden" })
  department = (await request("/system/departments")).find(
    (item) => item.name === "System acceptance department"
  )
  assert(department)
  const departmentRow = page.locator(`[data-department-id="${department.id}"]`)
  await departmentRow
    .getByRole("button", { name: `Edit ${department.name}`, exact: true })
    .click()
  await page
    .locator("#department-name")
    .fill("System acceptance department edited")
  await page.locator('[form="department-form"][type="submit"]').click()
  await page.getByRole("dialog").waitFor({ state: "hidden" })
  department = (await request("/system/departments")).find(
    (item) => item.id === department.id
  )
  assert.equal(department.name, "System acceptance department edited")
  await capture(page, "system-departments")
  await desktop()

  await page.goto("/system/roles")
  await page.getByRole("button", { name: "Create role", exact: true }).click()
  await page.locator("#role-name").fill("System acceptance menu role")
  await page
    .locator("#role-description")
    .fill("Real system role assignment acceptance")
  await page.locator('[form="role-form"][type="submit"]').click()
  await page.getByRole("dialog").waitFor({ state: "hidden" })
  role = (await request("/system/roles")).find(
    (item) => item.name === "System acceptance menu role"
  )
  assert(role && !role.builtin)
  await page.locator(`[data-role-id="${role.id}"]`).click()
  await page.getByRole("button", { name: "Edit", exact: true }).click()
  await page
    .locator("#role-description")
    .fill("Edited system role with menu read access")
  await page.locator('[form="role-form"][type="submit"]').click()
  await page.getByRole("dialog").waitFor({ state: "hidden" })
  const permission = page
    .locator('[data-permission-code="system_menu_view"]')
    .getByRole("checkbox")
  await permission.click()
  await page
    .locator('[data-permission-code="system_account_view"]')
    .getByRole("checkbox")
    .click()
  await eventually(
    async () =>
      (await request(`/system/roles/${role.id}/permission-grants`)).some(
        (grant) =>
          grant.permission_code === "system_menu_view" ||
          grant.code === "system_menu_view"
      ),
    "real permission grant"
  )
  await capture(page, "system-roles")
  await desktop()
  pass(
    "system department and role editing",
    "UI creates and edits real system department/role and grants system_menu_view"
  )

  await page.goto("/system/accounts/system")
  await page
    .getByRole("button", { name: "Add system account", exact: true })
    .click()
  await page.locator("#system-member-search").fill(worker.login_name)
  await select(
    "#system-member-principal",
    `${worker.display_name} (${worker.login_name})`
  )
  await page
    .getByRole("status")
    .filter({ hasText: "Loading" })
    .waitFor({ state: "hidden" })
  await select("#system-member-department", department.name)
  await page
    .getByRole("dialog")
    .getByRole("button", { name: "Add system account", exact: true })
    .click()
  await page.locator(`[data-system-member-role="${role.id}"]`).waitFor()
  await page.locator(`[data-system-member-role="${role.id}"]`).click()
  await eventually(
    async () =>
      (await request(`/system/roles/${role.id}/members`)).includes(worker.id),
    "system member role persisted"
  )
  await page
    .getByRole("dialog")
    .locator('button[data-slot="button"]')
    .filter({ hasText: /^Close$/ })
    .click()
  assert.equal(
    (await request(`/system/members/${worker.id}`)).department_id,
    department.id
  )
  await page.reload()
  await page
    .locator(`[data-account-id="${worker.id}"]`)
    .getByRole("cell", { name: role.name, exact: true })
    .waitFor()
  const workerLogin = await login(worker.login_name, "ViewAcceptance-aA1!2026")
  contexts.push(workerLogin.context)
  await workerLogin.page.goto("/system/menus")
  await workerLogin.page
    .getByRole("heading", { name: "System menu management", exact: true })
    .waitFor()
  await request("/system/menus", "GET", undefined, 200, workerLogin.context)
  await workerLogin.page.goto("/system/accounts/tenant")
  await workerLogin.page.locator("#account-owner").click()
  await workerLogin.page
    .getByRole("option", { name: tenantB.name, exact: true })
    .click()
  await workerLogin.page
    .getByRole("button", { name: "Manage memberships", exact: true })
    .click()
  await workerLogin.page
    .getByRole("heading", {
      name: `Manage memberships · ${tenantB.name}`,
      exact: true,
    })
    .waitFor()
  assert.equal(
    await workerLogin.page
      .getByRole("button", { name: "Create", exact: true })
      .count(),
    0
  )
  await request(
    `/system/tenants/${tenantB.id}/members`,
    "POST",
    { subject: worker.id, display_name: worker.display_name },
    403,
    workerLogin.context
  )
  await capture(workerLogin.page, "system-account-reader-owner-members")
  await page
    .locator(`[data-account-id="${worker.id}"]`)
    .getByRole("button", {
      name: `System access for ${worker.display_name}`,
      exact: true,
    })
    .click()
  await eventually(
    async () =>
      (await page
        .locator(`[data-system-member-role="${role.id}"]`)
        .getAttribute("aria-checked")) === "true",
    "persisted system role checkbox"
  )
  await page.locator(`[data-system-member-role="${role.id}"]`).click()
  await eventually(
    async () =>
      !(await request(`/system/roles/${role.id}/members`)).includes(worker.id),
    "system role revoked"
  )
  await request("/system/menus", "GET", undefined, 403, workerLogin.context)
  await page
    .getByRole("dialog")
    .locator('button[data-slot="button"]')
    .filter({ hasText: /^Close$/ })
    .click()
  pass(
    "system membership and live revocation",
    "named global picker, department placement, role assignment and real logged-in denial after revoke"
  )

  const builtin = (await request("/system/roles")).find((item) => item.builtin)
  assert(builtin)
  await page.goto("/system/roles")
  await page.locator(`[data-role-id="${builtin.id}"]`).click()
  await page
    .getByRole("heading", { name: "Full system administrator", exact: true })
    .waitFor()
  assert.equal(
    await page.getByRole("button", { name: "Edit", exact: true }).count(),
    0
  )
  assert.equal(
    await page
      .getByRole("button", { name: "Delete role", exact: true })
      .count(),
    0
  )
  assert(
    await page
      .locator('[data-permission-code="system_menu_view"]')
      .getByRole("checkbox")
      .isDisabled()
  )
  await request(
    `/system/roles/${builtin.id}`,
    "PATCH",
    { name: "Forbidden builtin edit" },
    403
  )
  await request(`/system/roles/${builtin.id}`, "DELETE", undefined, 403)
  await request(
    `/system/roles/${builtin.id}/permissions/system_menu_view`,
    "DELETE",
    undefined,
    403
  )
  pass(
    "protected full system administrator",
    "immutable builtin role controls and actual backend403"
  )

  for (const [kind, owner] of [
    ["platform", platformA],
    ["tenant", tenantB],
  ]) {
    const prefix = `/system/${kind === "platform" ? "operating-platforms" : "tenants"}/${owner.id}`
    assert(
      !(await request(`${prefix}/members`)).some(
        (member) => member.subject === operator.subject
      ),
      "operator has no local membership"
    )
    const localPrefix = `/${kind === "platform" ? "operating-platforms" : "tenants"}/${owner.id}`
    const fallbackRole = await request(
      `${localPrefix}/roles`,
      "POST",
      {
        name: `System managed ${kind} role`,
        description: "Scoped system management acceptance",
      },
      undefined,
      ownerContext
    )
    const entities =
      kind === "tenant"
        ? await request(
            `${localPrefix}/entities`,
            "GET",
            undefined,
            undefined,
            ownerContext
          )
        : []
    const localDepartment = await request(
      `${localPrefix}/departments`,
      "POST",
      {
        name: `System managed ${kind} department`,
        ...(kind === "tenant" ? { entity_id: entities[0]?.id } : {}),
        status: "active",
        sort_order: 0,
      },
      undefined,
      ownerContext
    )
    await page.goto(`/system/accounts/${kind}/${owner.id}`)
    await page
      .getByRole("heading", {
        name: `Manage memberships · ${owner.name}`,
        exact: true,
      })
      .waitFor()
    await page.getByRole("button", { name: "Create", exact: true }).click()
    await page.locator("#system-member-search").fill(worker.login_name)
    await select(
      "#system-member-principal",
      `${worker.display_name} (${worker.login_name})`
    )
    await select("#system-member-department", localDepartment.name)
    await page
      .getByRole("dialog")
      .getByRole("button", { name: "Create", exact: true })
      .click()
    await eventually(
      async () =>
        (await request(`${prefix}/members`)).some(
          (member) => member.subject === worker.id
        ),
      "scoped membership created without operator membership"
    )
    assert.equal(
      (await request(`${prefix}/members/${worker.id}`)).department_id,
      localDepartment.id
    )
    if (fallbackRole) {
      await page
        .locator(`[data-system-member-role="${fallbackRole.id}"]`)
        .click()
      await eventually(
        async () =>
          (await request(`${prefix}/members/${worker.id}/roles`)).includes(
            fallbackRole.id
          ),
        "scoped role assigned"
      )
      await page
        .getByRole("dialog")
        .locator('button[data-slot="button"]')
        .filter({ hasText: /^Close$/ })
        .click()
      const memberRow = page.locator(`[data-owner-member-id="${worker.id}"]`)
      await memberRow
        .getByRole("cell", { name: fallbackRole.name, exact: true })
        .waitFor()
      await capture(page, `system-${kind}-assigned-member`)
      await desktop()
      await memberRow
        .getByRole("button", {
          name: `Manage membership for ${worker.display_name}`,
          exact: true,
        })
        .click()
      await eventually(
        async () =>
          (await page
            .locator(`[data-system-member-role="${fallbackRole.id}"]`)
            .getAttribute("aria-checked")) === "true",
        "persisted scoped role checkbox"
      )
      await page
        .locator(`[data-system-member-role="${fallbackRole.id}"]`)
        .click()
      await eventually(
        async () =>
          !(await request(`${prefix}/members/${worker.id}/roles`)).includes(
            fallbackRole.id
          ),
        "scoped role revoked"
      )
    }
    await select("#system-member-department", "No department")
    await select("#system-member-status", "Disable")
    await page
      .getByRole("dialog")
      .getByRole("button", { name: "Save", exact: true })
      .click()
    await eventually(
      async () =>
        (await request(`${prefix}/members/${worker.id}`)).status === "disabled",
      "scoped member disabled"
    )
    await page
      .getByRole("dialog")
      .locator('button[data-slot="button"]')
      .filter({ hasText: /^Close$/ })
      .click()
    await capture(page, `system-${kind}-members`)
    await desktop()
    await request(
      `/roles/${fallbackRole.id}`,
      "DELETE",
      undefined,
      undefined,
      ownerContext
    )
    await request(
      `/departments/${localDepartment.id}?version=${localDepartment.version}`,
      "DELETE",
      undefined,
      undefined,
      ownerContext
    )
  }
  pass(
    "system scoped owner membership",
    "superadmin without local membership manages explicit platform and tenant memberships and assigns/revokes local roles"
  )

  await page.goto("/system/files")
  await page
    .getByRole("button", { name: "Create system shared files", exact: true })
    .click()
  await page.getByRole("button", { name: "New folder", exact: true }).waitFor()
  const folder = async (name) => {
    await page.getByRole("button", { name: "New folder", exact: true }).click()
    await page.locator("#file-editor-name").fill(name)
    await page.locator('[form="file-editor-form"][type="submit"]').click()
    await page.getByRole("dialog").waitFor({ state: "hidden" })
    await page
      .getByLabel("Files and folders")
      .getByRole("button", { name, exact: true })
      .waitFor()
  }
  await folder("System acceptance shared folder")
  const content = "System shared content acceptance\n"
  await page
    .locator('input[type="file"][aria-label="Upload file"]')
    .setInputFiles({
      name: "system-acceptance.txt",
      mimeType: "text/plain",
      buffer: Buffer.from(content),
    })
  await page
    .getByLabel("Files and folders")
    .getByRole("button", { name: "system-acceptance.txt", exact: true })
    .waitFor()
  const roots = await client("files", undefined, "", true)
  const root = roots.items.find((item) => item.scope === "system")
  assert(root && !root.tenant_id && !root.principal_id && !root.entity_id)
  const children = await client("fileItems", undefined, root.id)
  const uploaded = children.items.find(
    (item) => item.name === "system-acceptance.txt"
  )
  assert(uploaded)
  const downloaded = await admin.context.request.get(
    `/api/files/${uploaded.id}/content`
  )
  assert(downloaded.ok())
  assert.equal(await downloaded.text(), content)
  const personal = await client("files", null)
  assert(!personal.items.some((item) => item.id === root.id))
  await request("/system/files", "GET", undefined, 403, workerLogin.context)
  await capture(page, "system-shared-files")
  await desktop()
  await page.getByRole("button", { name: "Language", exact: true }).click()
  await page.getByRole("button", { name: /简体中文/ }).click()
  await page.getByText("系统共享文件", { exact: true }).first().waitFor()
  await capture(page, "system-shared-files-zh", "打开主导航")
  await desktop()
  await page.goto("/system/roles")
  await page.locator(`[data-role-id="${builtin.id}"]`).click()
  await page
    .getByRole("heading", { name: "完整系统管理员", exact: true })
    .waitFor()
  await capture(page, "system-builtin-role-zh", "打开主导航")
  await desktop()
  await page.getByRole("button", { name: "语言", exact: true }).click()
  await page.getByRole("button", { name: /English/ }).click()
  pass(
    "real system shared files",
    "singleton system root, UI folder/upload, actual content download, personal-space separation and restricted403"
  )

  await openUI(systemTarget, /System administration/)
  await page.getByRole("link", { name: "File management", exact: true }).click()
  assert(
    await page
      .getByRole("button", { name: "New folder", exact: true })
      .isDisabled()
  )
  const denied = await page.evaluate(async (id) => {
    const { accountViewRequest } = await import("/src/lib/account-view.ts")
    const body = new FormData()
    body.set("parent_id", id)
    body.set("kind", "directory")
    body.set("name", "Forbidden readonly folder")
    const response = await fetch("/api/system/files", {
      ...accountViewRequest("GET"),
      method: "POST",
      body,
    })
    return response.status
  }, root.id)
  assert.equal(denied, 403)
  await capture(page, "system-files-readonly-view")
  await exitUI()
  await openUI(systemTarget, /System administration/, true)
  await page.getByRole("link", { name: "File management", exact: true }).click()
  await folder("System writable view folder")
  const writableFolder = (
    await client("fileItems", undefined, root.id)
  ).items.find((item) => item.name === "System writable view folder")
  assert(writableFolder)
  await eventually(
    async () =>
      !!statement(
        "SELECT principal_id,detail FROM e2e_iam_audit_events WHERE event_type='file_directory_created' AND target_id=? ORDER BY id DESC LIMIT 1"
      ).get(writableFolder.id),
    "writable file audit"
  )
  const event = statement(
    "SELECT principal_id,detail FROM e2e_iam_audit_events WHERE event_type='file_directory_created' AND target_id=? ORDER BY id DESC LIMIT 1"
  ).get(writableFolder.id)
  assert.equal(String(event.principal_id), operator.subject)
  assert.equal(JSON.parse(event.detail).viewed_principal_id, systemTarget.id)
  await capture(page, "system-files-writable-view")
  await exitUI()
  pass(
    "system files account views",
    "readonly controls and actual403; writable view creates real shared folder with operator/target audit attribution"
  )

  await request(`/system/members/${worker.id}`, "PATCH", {
    department_id: "",
    status: "disabled",
  })
  await request(`/system/roles/${role.id}`, "DELETE")
  department = (await request("/system/departments")).find(
    (item) => item.id === department.id
  )
  await page.goto("/system/departments")
  page.once("dialog", (dialog) => dialog.accept())
  await page
    .locator(`[data-department-id="${department.id}"]`)
    .getByRole("button", { name: `Delete ${department.name}`, exact: true })
    .click()
  await eventually(
    async () =>
      !(await request("/system/departments")).some(
        (item) => item.id === department.id
      ),
    "system department deleted"
  )
}
