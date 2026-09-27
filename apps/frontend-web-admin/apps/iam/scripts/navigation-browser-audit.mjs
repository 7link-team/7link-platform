import assert from "node:assert/strict"
import { chromium } from "playwright"
import { auditPassword } from "./audit-password.mjs"

const baseURL = process.env.IAM_CONSOLE_URL ?? "http://localhost:8091"
const browser = await chromium.launch({
  channel: process.env.IAM_BROWSER_CHANNEL ?? "chrome",
  headless: true,
})
try {
  for (const viewport of [
    { width: 1440, height: 1000 },
    { width: 390, height: 844 },
  ]) {
    const context = await browser.newContext({ baseURL, viewport })
    const page = await context.newPage()
    const errors = []
    page.on("pageerror", (error) => errors.push(error.message))
    page.on("console", (message) => {
      if (
        message.type() === "error" &&
        message.text().includes("Maximum update depth")
      )
        errors.push(message.text())
    })
    const cdp = await context.newCDPSession(page)
    await cdp.send("Network.enable")
    await cdp.send("Network.emulateNetworkConditions", {
      offline: false,
      latency: 250,
      downloadThroughput: 1024 * 1024,
      uploadThroughput: 1024 * 1024,
      connectionType: "cellular3g",
    })
    await page.goto("/system/tenants")
    await page.locator("#login-name").waitFor()
    assert.equal(new URL(page.url()).pathname, "/login")
    assert.deepEqual(
      errors,
      [],
      "lazy login navigation must not restart on each router update"
    )
    await page
      .locator("#login-name")
      .fill(process.env.IAM_BROWSER_LOGIN ?? "admin@example.test")
    await page.locator("#password").fill(auditPassword())
    await page.locator('form button[type="submit"]').click()
    await page.getByRole("heading", { name: "租户管理", exact: true }).waitFor()
    assert.equal(
      new URL(page.url()).pathname,
      "/tenants",
      "login returns to the protected deep link"
    )
    assert.deepEqual(errors, [])
    await context.close()
  }
  console.log(
    "PASS: slow-network anonymous redirect and authenticated deep-link return, desktop/mobile, no React update loops"
  )
} finally {
  await browser.close()
}
