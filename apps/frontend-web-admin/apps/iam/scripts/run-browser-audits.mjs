import { spawn } from "node:child_process"
import { mkdtemp, readFile, rm } from "node:fs/promises"
import { tmpdir } from "node:os"
import { join } from "node:path"
import { fileURLToPath } from "node:url"
import { chromium } from "playwright"

const audits = [
  "department",
  "position",
  "group",
  "entity",
  "tenant",
  "role",
  "invitation",
  "access-request",
  "access-review",
  "service-account",
  "scim",
  "registration",
  "passkey",
]
const selected = process.argv.slice(2)
for (const name of selected) {
  if (!audits.includes(name)) throw new Error(`Unknown browser audit: ${name}`)
}
const baseURL = process.env.IAM_CONSOLE_URL ?? "http://localhost:8091"
const results = []
for (const name of selected.length ? selected : audits) {
  const profile = await mkdtemp(join(tmpdir(), "iam-browser-audit-"))
  let browser
  try {
    browser = await chromium.launchPersistentContext(profile, {
      channel: process.env.IAM_BROWSER_CHANNEL ?? "chrome",
      headless: true,
      args: ["--remote-debugging-port=0"],
    })
    const page = browser.pages()[0] ?? (await browser.newPage())
    const diagnostics = await browser.newCDPSession(page)
    await diagnostics.send("Runtime.enable")
    diagnostics.on("Runtime.consoleAPICalled", (event) => {
      if (
        event.type === "error" &&
        event.args.some((argument) =>
          String(argument.value).includes("Maximum update depth")
        )
      ) {
        console.error(
          JSON.stringify({
            audit: name,
            updateLoop: event.stackTrace?.callFrames.slice(0, 16),
          })
        )
      }
    })
    // Legacy CDP scripts own dialog handling; disable Playwright's auto-dismiss.
    page.on("dialog", () => {})
    await page.goto(baseURL + "/login")
    if (process.env.IAM_BROWSER_TENANT) {
      await page.evaluate(
        (tenant) => localStorage.setItem("iam.tenant", tenant),
        process.env.IAM_BROWSER_TENANT
      )
    }
    const port = Number(
      (await readFile(join(profile, "DevToolsActivePort"), "utf8")).split(
        "\n"
      )[0]
    )
    if (!Number.isInteger(port) || port <= 0)
      throw new Error("Browser did not publish a CDP port")
    console.log(`RUN: ${name}`)
    const status = await new Promise((resolve, reject) => {
      const child = spawn(
        "bun",
        [
          fileURLToPath(new URL(`${name}-browser-audit.mjs`, import.meta.url)),
          String(port),
          baseURL,
        ],
        {
          stdio: "inherit",
          windowsHide: true,
          signal: AbortSignal.timeout(180000),
        }
      )
      child.once("error", reject)
      child.once("exit", (code) => resolve(code))
    })
    results.push({ name, status: status === 0 ? "PASS" : "FAIL" })
    if (status !== 0) {
      console.error(
        JSON.stringify(
          await page.evaluate(() => ({
            path: location.pathname,
            alerts: [...document.querySelectorAll('[role="alert"]')].map(
              (element) => element.textContent
            ),
            invalid: [...document.querySelectorAll("input:invalid")].map(
              (element) => ({
                id: element.id,
                message: element.validationMessage,
              })
            ),
          }))
        )
      )
    }
  } catch (error) {
    console.error(`${name}: ${error.message}`)
    results.push({ name, status: "FAIL" })
  } finally {
    await browser?.close()
    await rm(profile, { recursive: true, force: true, maxRetries: 3 })
  }
}
console.log(JSON.stringify({ browserAudits: results }, null, 2))
if (results.some((result) => result.status !== "PASS")) process.exitCode = 1
