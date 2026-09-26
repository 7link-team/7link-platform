import assert from "node:assert/strict"
import { readFileSync, mkdirSync } from "node:fs"
import { chromium } from "playwright"

const directory = new URL(
  "../../../../backend-api/.temp/iam-uat/",
  import.meta.url
)
const { webURL } = JSON.parse(
  readFileSync(new URL("state.json", directory), "utf8")
)
assert.equal(new URL(webURL).hostname, "127.0.0.1")
const screenshots = new URL("browser/workspace/", directory)
mkdirSync(screenshots, { recursive: true })
const browser = await chromium.launch({ channel: "chrome", headless: true })
try {
  for (const locale of ["zh-CN", "en-US", "ms-MY"]) {
    const messages = JSON.parse(
      readFileSync(
        new URL(`../src/messages/${locale}.json`, import.meta.url),
        "utf8"
      )
    )
    for (const width of [1440, 390]) {
      const context = await browser.newContext({
        baseURL: webURL,
        viewport: { width, height: 900 },
      })
      await context.addCookies([
        { name: "NEXT_LOCALE", value: locale, url: webURL },
      ])
      const page = await context.newPage()
      const errors = []
      page.on("pageerror", (error) => errors.push(error.message))
      let fixtures = null
      try {
        await page.goto("/login")
        await page
          .locator('input[name="login_name"]')
          .fill("admin@example.test")
        await page
          .locator('input[name="password"]')
          .fill(
            readFileSync(new URL("admin-password", directory), "utf8").trim()
          )
        await page.locator('button[type="submit"]').click()
        const switcher = page.getByRole("button", {
          name: messages.switchWorkspace,
          exact: true,
        })
        await switcher.waitFor()
        fixtures = await page.evaluate(async () => {
          const { iamApi, setTenant } = await import("/src/lib/iam-api.ts")
          const tenants = (await iamApi.tenants()).filter(
            (tenant) => tenant.status === "active"
          )
          const tenant = tenants[0]
          setTenant(tenant.id)
          const created = {
            tenant,
            other: tenants[1],
            entities: [],
            roles: [],
            principal: (await iamApi.session()).subject,
          }
          try {
            for (const suffix of ["A", "B"]) {
              const name = `Workspace-${Date.now()}-${suffix}`
              const entity = await iamApi.createEntity({
                type: "unit",
                name,
                status: "active",
                metadata: { audit: suffix },
              })
              created.entities.push(entity)
              const role = await iamApi.createRole({
                name: `${name}-role`,
                description: "Workspace audit fixture",
              })
              created.roles.push(role)
              await iamApi.putEntityRoleBinding(
                entity.id,
                role.id,
                created.principal,
                { effect: "allow" }
              )
            }
          } catch (error) {
            for (const [index, entity] of created.entities.entries()) {
              if (created.roles[index]) {
                await iamApi.deleteEntityRoleBinding(
                  entity.id,
                  created.roles[index].id,
                  created.principal
                )
                await iamApi.deleteRole(created.roles[index].id)
              }
              await iamApi.deleteEntity(entity.id)
            }
            throw error
          }
          window.dispatchEvent(new Event("entity-catalog-change"))
          return created
        })
        const selectEntity = async (entity) => {
          await switcher.click()
          await page
            .getByRole("dialog")
            .getByRole("button", { name: entity.name, exact: true })
            .click()
          await page
            .getByRole("heading", {
              name: messages.entityOverview,
              exact: true,
            })
            .waitFor()
        }
        const navigateView = async (key) => {
          if (width < 768)
            await page
              .getByRole("button", { name: messages.openMainNav, exact: true })
              .click()
          await page
            .getByRole("navigation", { name: messages.mainNav, exact: true })
            .getByRole("link", { name: messages[key], exact: true })
            .filter({ visible: true })
            .click()
          if (width < 768) await page.keyboard.press("Escape")
          await page
            .getByRole("heading", { name: messages[key], exact: true })
            .waitFor()
        }
        await switcher.click()
        await page
          .getByRole("dialog")
          .getByRole("button", { name: fixtures.entities[0].name, exact: true })
          .waitFor()
        const triggerBox = await switcher.boundingBox()
        const panelBox = await page.getByRole("dialog").boundingBox()
        assert(triggerBox && panelBox)
        assert(panelBox.y >= triggerBox.y + triggerBox.height)
        assert(panelBox.y - triggerBox.y - triggerBox.height < 12)
        assert(panelBox.x >= 0 && panelBox.x + panelBox.width <= width)
        await page.getByRole("dialog").evaluate(async (element) => {
          await Promise.all(
            element.getAnimations().map((animation) => animation.finished)
          )
        })
        await page.screenshot({
          path: new URL(`${locale}-${width}-panel.png`, screenshots).pathname,
        })
        await page.keyboard.press("Escape")
        await selectEntity(fixtures.entities[0])
        assert.equal(
          await page
            .locator(`aside a[href="/tenants/${fixtures.tenant.id}/users"]`)
            .count(),
          0
        )
        await navigateView("entityRoles")
        await page
          .getByRole("cell", { name: fixtures.roles[0].name, exact: true })
          .waitFor()
        assert.equal(
          await page
            .getByRole("cell", { name: fixtures.roles[1].name, exact: true })
            .count(),
          0
        )
        await page.reload()
        await page
          .getByRole("cell", { name: fixtures.roles[0].name, exact: true })
          .waitFor()
        await selectEntity(fixtures.entities[1])
        await navigateView("entityRoles")
        await page
          .getByRole("cell", { name: fixtures.roles[1].name, exact: true })
          .waitFor()
        assert.equal(
          await page
            .getByRole("cell", { name: fixtures.roles[0].name, exact: true })
            .count(),
          0
        )
        assert.equal(
          await page.evaluate(
            () => document.documentElement.scrollWidth > innerWidth
          ),
          false
        )
        if (locale !== "zh-CN")
          assert.doesNotMatch(
            await page.locator("body").innerText(),
            /\p{Script=Han}/u
          )
        await page.screenshot({
          path: new URL(`${locale}-${width}-roles.png`, screenshots).pathname,
        })
        await navigateView("entitySettings")
        await page
          .getByRole("row")
          .filter({ hasText: fixtures.entities[1].name })
          .waitFor()
        assert.equal(
          await page
            .getByRole("row")
            .filter({ hasText: fixtures.entities[0].name })
            .count(),
          0
        )
        await switcher.click()
        await page
          .getByRole("dialog")
          .getByRole("button", {
            name: messages.tenantPerspective,
            exact: true,
          })
          .click()
        await page.waitForURL((url) => url.pathname === "/")
        await page
          .getByRole("heading", { name: messages.identityControl, exact: true })
          .waitFor()
        assert.equal(
          await page
            .locator(
              `aside a[href="/tenants/${fixtures.tenant.id}/entities/` +
                fixtures.entities[1].id +
                '/roles"]'
            )
            .count(),
          0
        )
        if (fixtures.other) {
          await switcher.click()
          await page
            .getByRole("dialog")
            .getByRole("button")
            .filter({ hasText: fixtures.other.slug })
            .click()
          await page
            .getByRole("dialog")
            .getByRole("button", {
              name: messages.tenantPerspective,
              exact: true,
            })
            .click()
          await page.goto(
            `/tenants/${fixtures.tenant.id}/entities/${fixtures.entities[0].id}/roles`
          )
          await page
            .getByText(messages.entityUnavailable, { exact: true })
            .waitFor()
          assert.equal(
            await page
              .getByRole("cell", { name: fixtures.roles[0].name, exact: true })
              .count(),
            0
          )
        }
        assert.deepEqual(errors, [])
        console.log(
          `PASS ${locale} ${width}: panel, tenant view, populated entity isolation, refresh, settings, cross-tenant denial`
        )
      } finally {
        if (fixtures)
          await page.evaluate(async (created) => {
            const { iamApi, setTenant } = await import("/src/lib/iam-api.ts")
            setTenant(created.tenant.id)
            for (const [index, entity] of created.entities.entries()) {
              await iamApi.deleteEntityRoleBinding(
                entity.id,
                created.roles[index].id,
                created.principal
              )
              await iamApi.deleteRole(created.roles[index].id)
              await iamApi.deleteEntity(entity.id)
            }
            await iamApi.logout()
          }, fixtures)
        await context.close()
      }
    }
  }
} finally {
  await browser.close()
}
