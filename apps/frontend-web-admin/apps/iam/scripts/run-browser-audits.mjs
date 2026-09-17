import { spawn } from "node:child_process"
import { mkdir, mkdtemp, readFile, rm, writeFile } from "node:fs/promises"
import { tmpdir } from "node:os"
import { join } from "node:path"
import { fileURLToPath } from "node:url"
import { chromium, request } from "playwright"
import { auditPassword } from "./audit-password.mjs"

const audits = [
  "department",
  "position",
  "group",
  "entity",
  "tenant",
  "role",
  "member-authorization",
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
const output = process.env.IAM_BROWSER_AUDIT_OUTPUT
if (output) await mkdir(output, { recursive: true })
let tenantSubject
if (process.env.IAM_BROWSER_TENANT) {
  const context = await request.newContext({ baseURL })
  try {
    const login = await context.post("/api/authn/sessions", {
      headers: { Origin: baseURL },
      data: {
        login_name: process.env.IAM_BROWSER_LOGIN ?? "admin@example.test",
        password: auditPassword(),
        return_url: baseURL + "/",
      },
    })
    if (!login.ok() || (await login.json()).data?.status !== "authenticated")
      throw new Error("Browser audit tenant setup requires authenticated login")
    const session = await context.get("/api/authn/sessions/current")
    if (!session.ok()) throw new Error("Browser audit session lookup failed")
    tenantSubject = (await session.json()).data?.subject
    if (!tenantSubject) throw new Error("Browser audit session has no subject")
  } finally {
    await context.delete("/api/authn/sessions/current", {
      headers: { Origin: baseURL },
    })
    await context.dispose()
  }
}
for (const name of selected.length ? selected : audits) {
  const profile = await mkdtemp(join(tmpdir(), "iam-browser-audit-"))
  let browser
  try {
    browser = await chromium.launchPersistentContext(profile, {
      channel: process.env.IAM_BROWSER_CHANNEL ?? "chrome",
      headless: true,
      locale: "zh-CN",
      args: ["--remote-debugging-port=0"],
    })
    const page = browser.pages()[0] ?? (await browser.newPage())
    await browser.addInitScript(() => {
      document.cookie = "NEXT_LOCALE=zh-CN; Path=/; SameSite=Lax"
    })
    await browser.addCookies([
      { name: "NEXT_LOCALE", value: "zh-CN", url: baseURL },
    ])
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
        ({ tenant, subject }) => {
          localStorage.setItem("iam.tenant", tenant)
          localStorage.setItem("iam.tenant:subject", subject)
        },
        { tenant: process.env.IAM_BROWSER_TENANT, subject: tenantSubject }
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
      if (output)
        await page.screenshot({
          path: join(output, `${name}-failure.png`),
          fullPage: true,
        })
      console.error(
        JSON.stringify(
          await page.evaluate(() => ({
            path: location.pathname,
            text: document.querySelector("main")?.innerText,
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
if (output)
  await writeFile(
    join(output, "report.json"),
    JSON.stringify(
      { at: new Date().toISOString(), baseURL, browserAudits: results },
      null,
      2
    ) + "\n"
  )
if (results.some((result) => result.status !== "PASS")) process.exitCode = 1
