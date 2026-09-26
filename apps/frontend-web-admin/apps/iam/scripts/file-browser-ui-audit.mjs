// Browser contract audit with mocked HTTP boundaries; not backend acceptance.
import assert from "node:assert/strict"
import { mkdirSync } from "node:fs"
import { fileURLToPath } from "node:url"
import { chromium } from "playwright"

const baseURL = process.env.IAM_CONSOLE_URL ?? "http://127.0.0.1:18191"
const screenshots = new URL(
  "../../../../../.local/screenshots/file-browser/",
  import.meta.url
)
mkdirSync(screenshots, { recursive: true })
const browser = await chromium.launch({
  channel: process.env.IAM_BROWSER_CHANNEL ?? "chrome",
  headless: true,
})
const errors = []
const row = (page, id) => page.locator(`[data-file-id="${id}"]`)
async function openProperties(page, id, name) {
  await row(page, id).click({ button: "right" })
  await page.getByRole("menuitem", { name: "Properties", exact: true }).click()
}
async function fits(page) {
  await page.waitForFunction(
    () =>
      !document.querySelector(
        '[role="dialog"][data-starting-style], [role="dialog"][data-ending-style]'
      )
  )
  await page.evaluate(() =>
    Promise.all(
      document
        .getAnimations()
        .map((animation) => animation.finished.catch(() => {}))
    )
  )
  assert(
    await page.evaluate(
      () => document.documentElement.scrollWidth <= innerWidth
    ),
    "horizontal overflow"
  )
  for (const dialog of await page.getByRole("dialog").all()) {
    if (!(await dialog.isVisible())) continue
    const box = await dialog.boundingBox()
    const size = page.viewportSize()
    assert(
      box &&
        box.x >= 0 &&
        box.y >= 0 &&
        box.x + box.width <= size.width + 1 &&
        box.y + box.height <= size.height + 1,
      `dialog outside viewport: ${JSON.stringify({ box, size })}`
    )
  }
}
async function waitCount(page, count) {
  await page.waitForFunction(
    (value) => document.querySelectorAll("[data-file-id]").length === value,
    count
  )
}
try {
  for (const [name, viewport] of [
    ["desktop", { width: 1440, height: 1000 }],
    ["tablet", { width: 768, height: 1024 }],
    ["mobile", { width: 390, height: 844 }],
  ]) {
    const page = await browser.newPage({ viewport })
    page.on("pageerror", (error) => errors.push(error.message))
    await page.goto(`${baseURL}/scripts/fixtures/file-browser.html`)
    await waitCount(page, 5)
    await fits(page)
    await page.screenshot({
      path: fileURLToPath(new URL(`${name}-details.png`, screenshots)),
      fullPage: true,
    })
    assert.equal(
      await page.getByRole("heading", { name: "Files", exact: true }).count(),
      0,
      "redundant file page title is visible"
    )
    if (name === "desktop") {
      const browserSurface = page.locator(
        'section[aria-label="Files"] > div.relative'
      )
      const dragTypes = await browserSurface.evaluate((element) => {
        const data = new DataTransfer()
        data.items.add(new File(["one"], "one.txt", { type: "text/plain" }))
        data.items.add(new File(["two"], "two.txt", { type: "text/plain" }))
        globalThis.__fileBrowserAuditDrop = data
        element.dispatchEvent(
          new DragEvent("dragenter", {
            bubbles: true,
            cancelable: true,
            dataTransfer: data,
          })
        )
        return [...data.types]
      })
      assert(dragTypes.includes("Files"), "browser did not create a file drag")
      await page
        .getByText("Drop files or folders here to upload", { exact: true })
        .waitFor()
      await browserSurface.evaluate((element) => {
        element.dispatchEvent(
          new DragEvent("drop", {
            bubbles: true,
            cancelable: true,
            dataTransfer: globalThis.__fileBrowserAuditDrop,
          })
        )
      })
      await page
        .getByTestId("operation")
        .filter({ hasText: "uploaded:one.txt,two.txt" })
        .waitFor()
      await row(page, "a").click({ button: "right" })
      await page.getByRole("menuitem", { name: "Copy", exact: true }).click()
      await page
        .getByTestId("operation")
        .filter({ hasText: "copied:Annual report.pdf" })
        .waitFor()
      const directorySurface = page.locator(
        '[aria-label="Current folder menu"]'
      )
      const surfaceBox = await directorySurface.boundingBox()
      assert(surfaceBox, "current folder menu surface is missing")
      await directorySurface.click({
        button: "right",
        position: { x: 8, y: Math.max(8, surfaceBox.height - 8) },
      })
      await page
        .getByRole("menuitem", { name: "Paste 1 items", exact: true })
        .click()
      await page
        .getByTestId("operation")
        .filter({ hasText: "pasted:1" })
        .waitFor()
    }
    await page
      .getByRole("button", { name: "Selection mode", exact: true })
      .click()
    await row(page, "a").getByRole("checkbox").check()
    await row(page, "c")
      .getByRole("checkbox")
      .click({ modifiers: ["Shift"] })
    assert(
      await row(page, "b").getByRole("checkbox").isChecked(),
      "range omitted intermediate item"
    )
    assert(
      await row(page, "c").getByRole("checkbox").isChecked(),
      "range omitted endpoint"
    )
    assert(
      await page
        .getByRole("button", {
          name: "Download selected files",
          exact: true,
        })
        .isEnabled(),
      "selected files have no download action"
    )
    await page
      .getByRole("button", {
        name: "Move selected items to trash",
        exact: true,
      })
      .click()
    await page
      .getByText("Move 3 selected items and their contents to trash?", {
        exact: true,
      })
      .waitFor()
    await page.getByRole("button", { name: "Cancel", exact: true }).click()
    await page
      .getByRole("button", { name: "Sort descending", exact: true })
      .click()
    assert(
      await row(page, "a").getByRole("checkbox").isChecked(),
      "sort changed ID selection"
    )
    await page.getByRole("textbox", { name: "Search this page" }).fill("Budget")
    await waitCount(page, 1)
    assert(
      await row(page, "b").getByRole("checkbox").isChecked(),
      "search changed ID selection"
    )
    await page
      .getByRole("textbox", { name: "Search this page" })
      .fill("no-such-file")
    await page
      .getByText("No matching items on this page", { exact: true })
      .waitFor()
    await page.getByRole("textbox", { name: "Search this page" }).fill("")
    await waitCount(page, 5)
    for (const view of ["Small icons", "Large icons", "Details view"]) {
      await page.getByRole("button", { name: view, exact: true }).click()
      await fits(page)
      assert(
        await row(page, "a").getByRole("checkbox").isChecked(),
        "view lost selection"
      )
      assert.equal(
        await row(page, "a")
          .getByRole("button", {
            name: "Actions for Annual report.pdf",
            exact: true,
          })
          .count(),
        view === "Details view" ? 1 : 0,
        "icon view retained the overflow action button"
      )
      await page.screenshot({
        path: fileURLToPath(
          new URL(
            `${name}-${view.split(" ")[0].toLowerCase()}.png`,
            screenshots
          )
        ),
        fullPage: true,
      })
    }
    await page.getByRole("combobox", { name: "Type", exact: true }).click()
    await page.getByRole("option", { name: "Folder", exact: true }).click()
    await waitCount(page, 1)
    assert.equal(
      await row(page, "folder").count(),
      1,
      "type filter did not retain folder"
    )
    await page.getByRole("combobox", { name: "Type", exact: true }).click()
    await page.getByRole("option", { name: "All types", exact: true }).click()
    await waitCount(page, 5)
    await row(page, "a").getByRole("checkbox").focus()
    await page.keyboard.press("Space")
    assert(
      !(await row(page, "a").getByRole("checkbox").isChecked()),
      "keyboard selection did not toggle"
    )
    await page.keyboard.press("Space")
    assert(
      await row(page, "a").getByRole("checkbox").isChecked(),
      "keyboard selection did not restore"
    )
    await openProperties(page, "a", "Annual report.pdf")
    await page.getByText("application/pdf", { exact: true }).waitFor()
    await fits(page)
    await page.screenshot({
      path: fileURLToPath(new URL(`${name}-properties.png`, screenshots)),
      fullPage: true,
    })
    await page
      .getByRole("button", { name: "Close properties", exact: true })
      .click()
    if (name !== "desktop") {
      await page.getByRole("dialog").waitFor({ state: "hidden" })
      const trigger = row(page, "a").getByRole("button", {
        name: "Actions for Annual report.pdf",
        exact: true,
      })
      await trigger.focus()
      await page.keyboard.press("Enter")
      await page
        .getByRole("menuitem", { name: "Properties", exact: true })
        .focus()
      await page.keyboard.press("Enter")
      await page.getByRole("dialog").waitFor()
      await fits(page)
      await page.keyboard.press("Escape")
      await page.getByRole("dialog").waitFor({ state: "hidden" })
      assert(
        await trigger.evaluate((element) => document.activeElement === element),
        "properties did not restore keyboard focus"
      )
    }
    await openProperties(
      page,
      "long",
      "Very-long-file-name-without-spaces-for-responsive-layout-validation-2026.pdf"
    )
    await fits(page)
    await page
      .getByRole("button", { name: "Close properties", exact: true })
      .click()
    await page.getByRole("button", { name: "Next page", exact: true }).click()
    await waitCount(page, 1)
    assert.equal(
      await row(page, "next").getByRole("checkbox").count(),
      0,
      "page retained selection mode"
    )
    await page
      .getByRole("button", { name: "Previous page", exact: true })
      .click()
    await waitCount(page, 5)
    assert.equal(
      await row(page, "a").getByRole("checkbox").count(),
      0,
      "previous page retained selection mode"
    )
    assert.equal(
      await row(page, "folder")
        .getByRole("button", { name: "Open Documents", exact: true })
        .count(),
      0,
      "folder retained a redundant open arrow"
    )
    await row(page, "folder").getByTitle("Documents").click()
    await page.getByText("This folder is empty", { exact: true }).waitFor()
    assert.equal(await page.getByTestId("location").textContent(), "folder")
    await page.getByRole("button", { name: "Back", exact: true }).click()
    await waitCount(page, 5)
    await page.getByRole("button", { name: "Forward", exact: true }).click()
    await page.getByText("This folder is empty", { exact: true }).waitFor()
    await page
      .getByRole("navigation", { name: "Location", exact: true })
      .getByRole("button", { name: "Entity files", exact: true })
      .click()
    await waitCount(page, 5)
    if (name !== "desktop") {
      await page
        .getByRole("button", { name: "Open folders", exact: true })
        .click()
      await page
        .getByRole("dialog")
        .getByRole("button", { name: "Documents", exact: true })
        .click()
      await page.getByText("This folder is empty", { exact: true }).waitFor()
      await page
        .getByRole("navigation", { name: "Location", exact: true })
        .getByRole("button", { name: "Entity files", exact: true })
        .click()
      await waitCount(page, 5)
    }
    if (name === "desktop") {
      await openProperties(page, "a", "Annual report.pdf")
      await page
        .getByRole("button", { name: "Fixture: revoke metadata", exact: true })
        .click()
      assert.equal(
        await page.getByText("application/pdf", { exact: true }).count(),
        0,
        "stale field after metadata revocation"
      )
    }
    for (const state of ["forbidden", "loading", "error"]) {
      await page
        .getByRole("button", { name: `Fixture: ${state}`, exact: true })
        .click()
      await waitCount(page, 0)
      assert.equal(
        await page.getByText("Annual report.pdf", { exact: true }).count(),
        0,
        "stale file after failure"
      )
      assert.equal(
        await page
          .getByRole("button", { name: "Team files", exact: true })
          .count(),
        0,
        "stale space navigation after failure"
      )
      await fits(page)
    }
    await page.close()
    console.log(
      `PASS ${name}: selection, range, sort, search, views, properties, pagination, navigation, failure isolation, layout`
    )
  }
  for (const locale of ["zh-CN", "ms-MY"]) {
    const page = await browser.newPage({
      viewport: { width: 390, height: 844 },
    })
    page.on("pageerror", (error) => errors.push(error.message))
    await page.goto(
      `${baseURL}/scripts/fixtures/file-browser.html?locale=${locale}`
    )
    await waitCount(page, 5)
    await fits(page)
    await page.close()
    console.log(`PASS ${locale}: mobile rendering`)
  }
  for (const [name, authenticated, viewport] of [
    ["anonymous", false, { width: 1440, height: 1000 }],
    ["authenticated-desktop", true, { width: 1440, height: 1000 }],
    ["authenticated-mobile", true, { width: 390, height: 844 }],
  ]) {
    const page = await browser.newPage({ viewport })
    page.on("pageerror", (error) => errors.push(error.message))
    const fileCalls = []
    await page.route("**/api/**", async (route) => {
      const request = route.request()
      const url = new URL(request.url())
      const path = url.pathname.slice(4)
      if (path === "/me") {
        if (!authenticated)
          return route.fulfill({
            status: 401,
            json: { code: 401, message: "Unauthorized" },
          })
        return route.fulfill({
          json: {
            code: 0,
            data: { subject: "1", organization_id: "11", acr: 1 },
          },
        })
      }
      if (path === "/tenants" || path === "/field-catalog")
        return route.fulfill({
          status: 403,
          json: { code: 403, message: "Forbidden" },
        })
      if (path === "/entities")
        return route.fulfill({
          json: {
            code: 0,
            message: "ok",
            data: [{ id: "80", name: "Fixture entity", status: "active" }],
          },
        })
      if (path === "/me/permissions")
        return route.fulfill({
          json: { code: 0, message: "ok", data: ["file_view"] },
        })
      if (path === "/files" || path.startsWith("/files/")) {
        fileCalls.push({
          path: path + url.search,
          tenant: request.headers()["x-tenant-id"],
          entity: request.headers()["x-entity-id"],
        })
        if (path === "/files")
          return route.fulfill({
            json: {
              code: 0,
              message: "ok",
              data: {
                items: [
                  {
                    id: "2",
                    name: "Entity documents",
                    kind: "directory",
                    scope: "entity",
                    tenant_id: "11",
                    entity_id: "80",
                  },
                ],
              },
            },
          })
        if (path === "/files/2/items")
          return route.fulfill({
            json: {
              code: 0,
              message: "ok",
              data: {
                path: [{ id: "2", name: "Entity documents" }],
                items: [
                  {
                    id: "3",
                    name: "Policies",
                    kind: "directory",
                    updated_at: "2026-09-18T12:00:00Z",
                    revision: 1,
                    version: 1,
                  },
                  {
                    id: "4",
                    name: "Quarterly report.pdf",
                    kind: "file",
                    size: 4096,
                    detected_type: "application/pdf",
                    extension: "pdf",
                    updated_at: "2026-09-18T12:30:00Z",
                    revision: 1,
                    version: 2,
                  },
                ],
              },
            },
          })
        return route.fulfill({
          status: 404,
          json: { code: 404, message: "not_found" },
        })
      }
      if (path === "/me/avatar")
        return route.fulfill({
          json: { code: 0, message: "ok", data: { style: "custom", url: "" } },
        })
      const data =
        path === "/me/tenants"
          ? [{ id: "11", name: "Fixture tenant", status: "active" }]
          : path === "/me/menus"
            ? [
                {
                  id: "files-menu",
                  label: "Files",
                  path: "/files",
                  sort_order: 1,
                  children: [],
                },
              ]
            : []
      return route.fulfill({ json: { code: 0, message: "ok", data } })
    })
    await page.goto(`${baseURL}/files${authenticated ? "?entity_id=80" : ""}`)
    if (authenticated) {
      try {
        await waitCount(page, 2)
      } catch (cause) {
        throw new Error(
          `file route did not render: ${JSON.stringify({
            url: page.url(),
            text: (await page.locator("body").innerText()).slice(0, 1000),
            fileCalls,
          })}`,
          { cause }
        )
      }
      await page.getByText("Quarterly report.pdf", { exact: true }).waitFor()
      const expectedFilePaths = ["/files?limit=100", "/files/2/items?limit=100"]
      const fileCallCounts = new Map(
        expectedFilePaths.map((path) => [
          path,
          fileCalls.filter((call) => call.path === path).length,
        ])
      )
      assert.deepEqual(
        [...new Set(fileCalls.map(({ path }) => path))],
        expectedFilePaths
      )
      assert(
        [...fileCallCounts.values()].every((count) => count >= 1 && count <= 2),
        "file route issued more requests than React Strict Mode permits"
      )
      assert(
        fileCalls.every(
          ({ tenant, entity }) => tenant === "11" && entity === "80"
        ),
        "file request lost tenant or entity boundary"
      )
      await fits(page)
      await page.screenshot({
        path: fileURLToPath(new URL(`${name}-route.png`, screenshots)),
        fullPage: true,
      })
    } else {
      await page.waitForURL("**/login")
      assert.equal(fileCalls.length, 0, "anonymous route called files API")
    }
    await page.close()
    console.log(
      `PASS /files route ${name}: ${authenticated ? "authorized live-data rendering and request boundaries" : "anonymous login redirect"}`
    )
  }
  for (const [name, viewport] of [
    ["account-desktop", { width: 1440, height: 1000 }],
    ["account-mobile", { width: 390, height: 844 }],
  ]) {
    const page = await browser.newPage({ viewport })
    page.on("pageerror", (error) => errors.push(error.message))
    const fileCalls = []
    await page.route("**/api/**", async (route) => {
      const request = route.request()
      const url = new URL(request.url())
      const path = url.pathname.slice(4)
      if (path === "/me")
        return route.fulfill({
          json: {
            code: 0,
            data: { subject: "1", organization_id: "11", acr: 1 },
          },
        })
      if (path === "/me/avatar")
        return route.fulfill({
          json: { code: 0, message: "ok", data: { style: "custom", url: "" } },
        })
      if (path === "/files" || path.startsWith("/files/")) {
        fileCalls.push({
          path: path + url.search,
          tenant: request.headers()["x-tenant-id"],
          entity: request.headers()["x-entity-id"],
        })
        if (path === "/files")
          return route.fulfill({
            json: {
              code: 0,
              message: "ok",
              data: {
                items: [
                  {
                    id: "20",
                    name: "Account files",
                    kind: "directory",
                    scope: "account",
                    principal_id: "1",
                  },
                ],
              },
            },
          })
        if (path === "/files/20/items")
          return route.fulfill({
            json: {
              code: 0,
              message: "ok",
              data: {
                path: [{ id: "20", name: "Account files" }],
                items: [
                  {
                    id: "21",
                    name: "Private note.txt",
                    kind: "file",
                    size: 12,
                    detected_type: "text/plain",
                    extension: "txt",
                    updated_at: "2026-09-19T12:00:00Z",
                    revision: 1,
                    version: 1,
                  },
                ],
              },
            },
          })
      }
      return route.fulfill({ json: { code: 0, message: "ok", data: [] } })
    })
    await page.goto(`${baseURL}/me/files`)
    await waitCount(page, 1)
    await page.getByText("Private note.txt", { exact: true }).waitFor()
    assert.deepEqual(
      [...new Set(fileCalls.map(({ path }) => path))],
      ["/files?limit=100", "/files/20/items?limit=100"]
    )
    assert(
      fileCalls.every(({ tenant, entity }) => !tenant && !entity),
      "account file request leaked tenant or entity selectors"
    )
    await fits(page)
    await page.screenshot({
      path: fileURLToPath(new URL(`${name}-route.png`, screenshots)),
      fullPage: true,
    })
    await page.close()
    console.log(
      `PASS /me/files route ${name}: account ownership and absent organization selectors`
    )
  }
  assert.deepEqual(errors, [], "browser runtime errors")
} finally {
  await browser.close()
}
