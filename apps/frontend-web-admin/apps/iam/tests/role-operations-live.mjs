import assert from "node:assert/strict"
import { mkdirSync, readFileSync, writeFileSync } from "node:fs"
import { randomBytes } from "node:crypto"
import { chromium } from "playwright"

const directory = new URL("../../../../backend-api/.temp/dev/", import.meta.url)
const runtime = JSON.parse(
  readFileSync(new URL("runtime.json", directory), "utf8")
)
const fixture = JSON.parse(
  readFileSync(new URL("demo-accounts.json", directory), "utf8")
)
const selected = process.argv
  .find((value) => value.startsWith("--account="))
  ?.split("=")[1]
const accounts = fixture.accounts.filter(
  (account) => !selected || account.id === selected
)
assert(accounts.length > 0)
const output = new URL(`role-operations-${Date.now()}/`, directory)
mkdirSync(output, { recursive: true })
const report = {
  at: new Date().toISOString(),
  webURL: runtime.webURL,
  status: "RUNNING",
  accounts: [],
}
const organizationFixtures = new Map()
let fixtureContext
const save = () =>
  writeFileSync(new URL("report.json", output), JSON.stringify(report, null, 2))
const browser = await chromium.launch({ channel: "chrome", headless: true })
const personalPaths = [
  "/user/profile",
  "/user/security",
  "/user/mfa",
  "/user/tokens",
  "/user/files",
  "/user/memberships",
  "/user/identities",
  "/user/closure",
  "/user/applications",
]

async function call(context, path, method = "GET", data, expected) {
  const response = await context.request.fetch(`/api${path}`, {
    method,
    data,
    headers: { Origin: runtime.webURL },
  })
  if (expected) {
    assert.equal(response.status(), expected, `${method} ${path}`)
    return
  }
  assert(response.ok(), `${method} ${path}: ${response.status()}`)
  return (await response.json()).data
}
function row(page, name) {
  return page
    .locator("[data-file-id]")
    .filter({ has: page.getByText(name, { exact: true }) })
    .first()
}
async function action(page, name, label) {
  await row(page, name)
    .getByRole("button", { name: `Actions for ${name}`, exact: true })
    .click()
  await page.getByRole("menuitem", { name: label, exact: true }).click()
}
async function trash(page, name) {
  await action(page, name, "Move to trash")
  await page
    .getByRole("dialog")
    .getByRole("button", { name: "Move to trash", exact: true })
    .click()
  await row(page, name).waitFor({ state: "hidden" })
  // Base UI hides a closing modal from the accessibility tree before its exit
  // animation releases the modal layer. Wait for the DOM removal before opening another.
  await page.locator('[role="dialog"]').waitFor({ state: "detached" })
}
async function fileLifecycle(page, context, subject, stamp, width) {
  await page.goto("/user/files")
  await page.getByRole("button", { name: "New folder", exact: true }).waitFor()
  assert.equal(
    await page
      .getByRole("button", { name: "Create personal files", exact: true })
      .count(),
    0
  )
  await page.getByRole("button", { name: "Details view", exact: true }).click()
  const folder = `E2E-${stamp}`
  await page.getByRole("button", { name: "New folder", exact: true }).click()
  const editor = page.getByRole("dialog")
  await editor.getByRole("textbox", { name: "Name", exact: true }).fill(folder)
  await editor.getByRole("button", { name: "Save", exact: true }).click()
  await row(page, folder).waitFor()
  const file = `E2E-${stamp}.txt`
  const renamed = `E2E-${stamp}-renamed.txt`
  const content = `role acceptance ${stamp}`
  await page
    .locator('input[type="file"][aria-label="Upload file"]')
    .setInputFiles({
      name: file,
      mimeType: "text/plain",
      buffer: Buffer.from(content),
    })
  await row(page, file).waitFor()
  const downloadPromise = page.waitForEvent("download")
  await action(page, file, "Download")
  const download = await downloadPromise
  const stream = await download.createReadStream()
  const chunks = []
  for await (const chunk of stream) chunks.push(chunk)
  assert.equal(Buffer.concat(chunks).toString(), content)
  await action(page, file, "Rename")
  await editor.getByRole("textbox", { name: "Name", exact: true }).fill(renamed)
  await editor.getByRole("button", { name: "Save", exact: true }).click()
  await row(page, renamed).waitFor()
  await trash(page, renamed)
  await page.waitForLoadState("networkidle")
  if (width < 600) {
    await page
      .getByRole("button", { name: "Open folders", exact: true })
      .click()
    await page.getByRole("dialog").waitFor({ state: "visible" })
  }
  await page.getByRole("button", { name: "Recycle bin", exact: true }).click()
  if (width < 600) await page.keyboard.press("Escape")
  await row(page, renamed).waitFor()
  await action(page, renamed, "Restore")
  await row(page, renamed).waitFor({ state: "hidden" })
  await page.goto("/user/files")
  await row(page, renamed).waitFor()
  await page.reload()
  await row(page, renamed).waitFor()
  const roots = await call(context, `/principals/${subject}/files`)
  assert.equal(roots.items.length, 1)
  // Remove only this run's active fixtures; existing account files are untouched.
  await trash(page, renamed)
  await trash(page, folder)
}
async function roleCrud(page, context, account, stamp) {
  let path, apiPath
  if (account.id === "super-admin") {
    path = "/system/roles"
    apiPath = "/system/roles"
  } else if (account.id === "tenant-admin") {
    path = `/admin/${runtime.tenantID}/roles`
    apiPath = `/tenants/${runtime.tenantID}/roles`
  } else if (account.id === "operating-admin") {
    path = `/platform/${account.platformID}/roles`
    apiPath = `/operating-platforms/${account.platformID}/roles`
  } else return false
  await page.goto(path)
  await page.getByRole("button", { name: "Create role", exact: true }).click()
  const editor = page.getByRole("dialog")
  const name = `E2E role ${stamp}`
  await editor.locator("#role-name").fill(name)
  await editor.getByRole("button", { name: "Create", exact: true }).click()
  await editor.waitFor({ state: "hidden" })
  const roles = await call(context, apiPath)
  const created = roles.find((role) => role.name === name)
  assert(created, "UI role creation persists")
  try {
    await page.getByRole("button", { name: "Edit", exact: true }).click()
    await editor.locator("#role-name").fill(name + " edited")
    await editor.getByRole("button", { name: "Save", exact: true }).click()
    await editor.waitFor({ state: "hidden" })
    assert(
      (await call(context, apiPath)).some(
        (role) => role.id === created.id && role.name === name + " edited"
      )
    )
    await page.getByRole("button", { name: "Delete role", exact: true }).click()
    await page.waitForLoadState("networkidle")
    assert(
      !(await call(context, apiPath)).some((role) => role.id === created.id)
    )
  } finally {
    const remaining = await call(context, apiPath)
    if (remaining.some((role) => role.id === created.id))
      await call(
        context,
        account.id === "super-admin"
          ? `/system/roles/${created.id}`
          : `/roles/${created.id}`,
        "DELETE"
      )
  }
  return true
}
try {
  if (
    accounts.some((account) =>
      ["tenant-admin", "entity-account", "department-account"].includes(
        account.id
      )
    )
  ) {
    fixtureContext = await browser.newContext({ baseURL: runtime.webURL })
    const admin = fixture.accounts.find(
      (account) => account.id === "tenant-admin"
    )
    const config = JSON.parse(
      readFileSync(new URL("config.json", directory), "utf8")
    )
    await call(fixtureContext, "/login", "POST", {
      login_name: admin.loginName,
      password: admin.password,
      return_url: runtime.webURL + "/",
      captcha_type: "text",
      captcha_id: "automated",
      captcha_answer: config.authn.captcha.test_answer,
    })
    const collections = [
      `/tenants/${runtime.tenantID}/files`,
      `/entities/${fixture.entityID}/files`,
      ...Object.values(fixture.departments).map(
        (department) => `/departments/${department.id}/files`
      ),
    ]
    for (const collection of collections) {
      const roots = await call(fixtureContext, collection)
      for (const root of roots.items) {
        const records = {}
        organizationFixtures.set(root.id, records)
        for (const state of ["active", "trashed"]) {
          const name = `visibility-${randomBytes(6).toString("hex")}-${state}.txt`
          const content = `Authorized ${root.scope} root ${root.id}`
          const response = await fixtureContext.request.post(
            `/api${collection}`,
            {
              headers: { Origin: runtime.webURL },
              multipart: {
                parent_id: root.id,
                kind: "file",
                file: {
                  name,
                  mimeType: "text/plain",
                  buffer: Buffer.from(content),
                },
              },
            }
          )
          assert(
            response.ok(),
            `visibility fixture creation ${response.status()}`
          )
          const file = (await response.json()).data
          records[state] = { ...file, name, content }
          if (state === "trashed")
            await call(
              fixtureContext,
              `/files/${file.id}?revision=${file.revision}`,
              "DELETE"
            )
        }
      }
    }
  }
  for (const account of accounts) {
    const accountResult = { id: account.id, role: account.role, devices: [] }
    report.accounts.push(accountResult)
    let menuPaths = []
    for (const width of [1440, 390]) {
      const result = {
        width,
        status: "RUNNING",
        flows: [],
        pages: [],
        failures: [],
      }
      accountResult.devices.push(result)
      const context = await browser.newContext({
        baseURL: runtime.webURL,
        viewport: { width, height: 960 },
      })
      await context.addCookies([
        { name: "NEXT_LOCALE", value: "en-US", url: runtime.webURL },
      ])
      const page = await context.newPage()
      page.setDefaultTimeout(12_000)
      const errors = []
      let failedResponses = []
      page.on("pageerror", (error) => errors.push(error.message))
      page.on("dialog", (dialog) => dialog.accept())
      page.on("response", (response) => {
        const path = new URL(response.url()).pathname
        if (
          path.startsWith("/api/") &&
          response.status() >= 400 &&
          !/^\/api\/principals\/[^/]+\/avatar$/.test(path)
        )
          failedResponses.push(`${response.status()} ${path}`)
      })
      const phase = async (name, operation) => {
        try {
          await operation()
          result.flows.push({ name, status: "PASS" })
          console.log(`PASS ${account.id}/${width} ${name}`)
        } catch (error) {
          result.failures.push({ name, message: error.message })
          console.log(`FAIL ${account.id}/${width} ${name}: ${error.message}`)
          await page
            .screenshot({
              path: new URL(
                `${account.id}-${width}-${result.failures.length}.png`,
                output
              ).pathname.replace(/^\/([A-Z]:)/, "$1"),
              fullPage: true,
            })
            .catch(() => {})
        }
        save()
      }
      try {
        const prefix =
          account.entrances[0] === "tenant" ? "admin" : account.entrances[0]
        await phase("login and own identity", async () => {
          await page.goto(`/${prefix}/login`)
          await page.locator("#login-name").fill(account.loginName)
          await page.locator("#password").fill(account.password)
          await page.locator('form button[type="submit"]').click()
          await page.waitForURL(
            (url) =>
              url.pathname.startsWith(`/${prefix}/`) &&
              !/\/(login|workspaces)$/.test(url.pathname)
          )
          await page.waitForLoadState("networkidle")
          const identity = await call(context, "/me")
          assert(identity.subject)
          result.subject = identity.subject
          result.landing = new URL(page.url()).pathname
          if (width === 1440)
            menuPaths = await page
              .locator("aside nav a[href]")
              .evaluateAll((nodes) => [
                ...new Set(nodes.map((node) => node.getAttribute("href"))),
              ])
        })
        if (!result.subject) continue
        await phase("every displayed menu and personal page", async () => {
          const paths = [
            ...new Set([result.landing, ...menuPaths, ...personalPaths]),
          ]
          const pageFailures = []
          for (const path of paths) {
            failedResponses = []
            await page.goto(path)
            await page.waitForLoadState("networkidle")
            const alerts = await page.getByRole("alert").allTextContents()
            const unexpected = failedResponses.filter(
              (value) =>
                !value.endsWith(
                  `/api/tenants/${runtime.tenantID}/field-catalog`
                ) &&
                !(
                  /^503 \/api\/principals\/[^/]+\/passkeys$/.test(value) &&
                  alerts.includes("Passkeys are currently unavailable")
                )
            )
            result.pages.push({
              path,
              resolved: new URL(page.url()).pathname,
              errors: unexpected,
              alerts,
            })
            if (
              failedResponses.some((value) =>
                /^503 \/api\/principals\/[^/]+\/passkeys$/.test(value)
              )
            ) {
              assert(
                await page
                  .getByRole("button", { name: "Add passkey", exact: true })
                  .isDisabled()
              )
              result.flows.push({
                name: "Passkey unavailable state displayed; disabled add action",
                status: "PASS",
                limitation:
                  "Passkey enrollment not tested: local service unavailable",
              })
            }
            if (
              unexpected.length ||
              alerts.some((value) =>
                /Forbidden|HTTP 404|Failed to load|Unable to load/i.test(value)
              )
            )
              pageFailures.push({ path, errors: unexpected, alerts })
            const bounds = await page.evaluate(() => ({
              content: document.documentElement.scrollWidth,
              viewport: innerWidth,
            }))
            if (bounds.content > bounds.viewport + 1)
              pageFailures.push({ path, error: "horizontal overflow" })
          }
          assert.deepEqual(pageFailures, [], "menu/page failures")
        })
        await phase(
          "personal folder/upload/download/rename/trash/restore/reload",
          () =>
            fileLifecycle(
              page,
              context,
              result.subject,
              randomBytes(6).toString("hex"),
              width
            )
        )
        if (
          ["super-admin", "tenant-admin", "operating-admin"].includes(
            account.id
          )
        )
          await phase("authorized UI role create/edit/delete", () =>
            roleCrud(page, context, account, randomBytes(6).toString("hex"))
          )
        else
          await phase("forbidden management mutations", async () => {
            await call(
              context,
              "/system/roles",
              "POST",
              { name: "denied matrix role" },
              403
            )
            await call(
              context,
              `/tenants/${runtime.tenantID}/roles`,
              "POST",
              { name: "denied matrix tenant role" },
              403
            )
          })
        if (account.tenantID)
          await phase(
            "organization file visibility and department boundaries",
            async () => {
              const collections = [
                ["tenant", runtime.tenantID, "tenants", "tenant_id"],
                ["entity", fixture.entityID, "entities", "entity_id"],
                ...Object.values(fixture.departments).map((department) => [
                  "department",
                  department.id,
                  "departments",
                  "department_id",
                ]),
              ]
              for (const [scope, id, resource, ownerField] of collections) {
                const response = await context.request.get(
                  `/api/${resource}/${id}/files`
                )
                assert(
                  [200, 403].includes(response.status()),
                  `${resource}/${id}: ${response.status()}`
                )
                const items = response.ok()
                  ? (await response.json()).data.items
                  : []
                for (const root of items) {
                  assert.equal(root.scope, scope)
                  assert.equal(root[ownerField], id)
                }
                if (
                  account.id === "department-account" &&
                  !(
                    scope === "department" &&
                    id === fixture.departments["UAT Finance"].id
                  )
                )
                  assert.equal(
                    items.length,
                    0,
                    "department account must not see other collection roots"
                  )
                result.flows.push({
                  name: `${scope} collection ${id}`,
                  status: "PASS",
                  response: response.status(),
                  roots: items.length,
                })
              }
            }
          )
        if (
          ["tenant-admin", "entity-account", "department-account"].includes(
            account.id
          )
        )
          await phase(
            "authorized organization roots and contents visible, downloadable, reloadable and scoped in recycle bin",
            async () => {
              const path =
                account.id === "tenant-admin"
                  ? `/admin/${runtime.tenantID}/files`
                  : `/admin/${runtime.tenantID}/entities/${fixture.entityID}/files`
              const collections = [
                ...(account.id === "tenant-admin"
                  ? [`/tenants/${runtime.tenantID}/files`]
                  : []),
                ...(account.id !== "department-account"
                  ? [`/entities/${fixture.entityID}/files`]
                  : []),
                ...Object.values(fixture.departments)
                  .filter(
                    (department) =>
                      account.id !== "department-account" ||
                      department.id === fixture.departments["UAT Finance"].id
                  )
                  .map((department) => `/departments/${department.id}/files`),
              ]
              const expected = (
                await Promise.all(
                  collections.map((collection) => call(context, collection))
                )
              ).flatMap((page) => page.items)
              assert.equal(
                expected.length,
                account.id === "tenant-admin"
                  ? 4
                  : account.id === "entity-account"
                    ? 3
                    : 1
              )
              await page.goto(path)
              await page.waitForLoadState("networkidle")
              if (width < 600)
                await page
                  .getByRole("button", { name: "Open folders", exact: true })
                  .click()
              const navigation =
                width < 600
                  ? page.getByRole("dialog")
                  : page.locator("main aside")
              const rootButtons = navigation.locator("button[data-root-id]")
              assert.equal(
                await rootButtons.count(),
                expected.length,
                "all authorized roots must appear in the UI"
              )
              const labels = await rootButtons.allTextContents()
              assert(
                labels.some(
                  (label) =>
                    label.trim() === fixture.departments["UAT Finance"].name
                )
              )
              if (account.id === "department-account")
                assert(
                  !labels.some(
                    (label) =>
                      label.trim() ===
                      fixture.departments["UAT Operations"].name
                  )
                )
              if (width < 600) await page.keyboard.press("Escape")
              for (const root of expected) {
                if (width < 600)
                  await page
                    .getByRole("button", { name: "Open folders", exact: true })
                    .click()
                await navigation
                  .locator(`button[data-root-id="${root.id}"]`)
                  .click()
                await page.waitForURL(
                  (url) => url.searchParams.get("directory") === root.id
                )
                await page.waitForLoadState("networkidle")
                assert.equal(
                  (await page.getByRole("alert").allTextContents()).length,
                  0
                )
                const response = await call(context, `/files/${root.id}/items`)
                assert(
                  response.path.some((segment) => segment.id === root.id),
                  "authorized root opens its actual contents"
                )
                const marker = organizationFixtures.get(root.id).active
                await row(page, marker.name).waitFor()
                const downloadPromise = page.waitForEvent("download")
                await action(page, marker.name, "Download")
                const stream = await (await downloadPromise).createReadStream()
                const chunks = []
                for await (const chunk of stream) chunks.push(chunk)
                assert.equal(Buffer.concat(chunks).toString(), marker.content)
                await page.reload()
                await page.waitForLoadState("networkidle")
                await row(page, marker.name).waitFor()
                assert.equal(
                  (await page.getByRole("alert").allTextContents()).length,
                  0
                )
              }
              if (width < 600)
                await page
                  .getByRole("button", { name: "Open folders", exact: true })
                  .click()
              await navigation
                .getByRole("button", { name: "Recycle bin", exact: true })
                .click()
              if (width < 600) await page.keyboard.press("Escape")
              for (const root of expected)
                await row(
                  page,
                  organizationFixtures.get(root.id).trashed.name
                ).waitFor()
              if (account.id === "department-account") {
                for (const [rootID, records] of organizationFixtures)
                  if (rootID !== expected[0].id) {
                    assert.equal(await row(page, records.trashed.name).count(), 0)
                    const denied = await context.request.get(`/api/files/${records.active.id}/content`)
                    assert([403, 404].includes(denied.status()), "forged file ID must not expose another owner's content")
                  }
              }
              result.organizationRoots = expected.map((root) => ({
                id: root.id,
                scope: root.scope,
              }))
            }
          )
        await phase(
          "account-view entry follows creation permission",
          async () => {
            if (["super-admin", "system-admin"].includes(account.id)) {
              const permissions = await call(context, "/system/permissions")
              await page.goto("/system/accounts/system")
              await page.waitForLoadState("networkidle")
              const buttons = await page
                .getByRole("button", { name: /^Account view / })
                .count()
              assert.equal(
                buttons > 0,
                permissions.includes("account_view_create")
              )
            }
          }
        )
        await phase("UI logout and protected route redirect", async () => {
          await page.goto("/user/profile")
          await page
            .getByRole("button", { name: "Account menu", exact: true })
            .click()
          await page
            .getByRole("menuitem", { name: "Sign out", exact: true })
            .click()
          await page.locator("#login-name").waitFor()
          await call(context, "/me", "GET", undefined, 401)
          await page.goto("/user/files")
          await page.locator("#login-name").waitFor()
          assert.equal(new URL(page.url()).pathname, "/user/login")
        })
        if (errors.length)
          result.failures.push({ name: "uncaught browser errors", errors })
      } finally {
        result.status = result.failures.length ? "FAIL" : "PASS"
        await context.close()
        save()
      }
    }
  }
} catch (error) {
  report.failure = error.message
  throw error
} finally {
  if (fixtureContext) {
    report.cleanup = []
    for (const records of organizationFixtures.values()) {
      if (!records.active) continue
      try {
        await call(
          fixtureContext,
          `/files/${records.active.id}?revision=${records.active.revision}`,
          "DELETE"
        )
        report.cleanup.push({ id: records.active.id, status: "PASS" })
      } catch (error) {
        report.cleanup.push({
          id: records.active.id,
          status: "FAIL",
          message: error.message,
        })
      }
    }
    await fixtureContext.close()
  }
  await browser.close()
  report.status = report.accounts.every((account) =>
    account.devices.every((device) => device.status === "PASS")
  )
    ? "PASS"
    : "FAIL"
  if (report.failure || report.cleanup?.some((entry) => entry.status !== "PASS"))
    report.status = "FAIL"
  save()
  console.log(`Report: ${new URL("report.json", output).pathname}`)
}
assert.equal(report.status, "PASS")
