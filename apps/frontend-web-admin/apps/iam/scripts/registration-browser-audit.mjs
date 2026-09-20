import { Database } from "bun:sqlite"
import { spawnSync } from "node:child_process"
import { readFileSync } from "node:fs"
import { fileURLToPath } from "node:url"

const port = Number(process.argv[2] ?? 9333)
const baseURL = process.argv[3] ?? "http://localhost:8091"
const notificationFile =
  process.env.IAM_NOTIFICATION_FILE ??
  new URL("../../../../../.local/notification-last.json", import.meta.url)
// The identity service keeps its database wherever it was started from, which
// is not something this repository can know, so let the caller name it.
const databaseFile =
  process.env.IAM_DATABASE_FILE ??
  fileURLToPath(new URL("../../../../../.local/iam-smoke.db", import.meta.url))
const databaseContainer = process.env.IAM_DATABASE_CONTAINER
const databaseTablePrefix = process.env.IAM_DATABASE_TABLE_PREFIX ?? ""
const screenshotRoot = new URL(
  "../../../../../.local/screenshots/",
  import.meta.url
)

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
const failures = []
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
    failures.push({
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

async function navigate(url) {
  await command("Page.navigate", { url: new URL(url, baseURL).href })
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

async function submit() {
  await evaluate(`
    const form = document.querySelector("form");
    if (!(form instanceof HTMLFormElement)) throw new Error("form not found");
    form.requestSubmit();
  `)
}

async function capture(name, width, height) {
  await command("Emulation.setDeviceMetricsOverride", {
    width,
    height,
    deviceScaleFactor: 1,
    mobile: width < 600,
  })
  await Bun.sleep(250)
  const layout = await evaluate(`return {
    width: innerWidth,
    scrollWidth: document.documentElement.scrollWidth,
    horizontalOverflow: document.documentElement.scrollWidth > innerWidth,
  }`)
  if (layout.horizontalOverflow)
    throw new Error(`Horizontal overflow at ${width}px`)
  const image = await command("Page.captureScreenshot", {
    format: "png",
    fromSurface: true,
    captureBeyondViewport: false,
  })
  await Bun.write(
    new URL(name, screenshotRoot),
    Buffer.from(image.data, "base64")
  )
  return layout
}

async function registrationNotification(email, timeout = 10000) {
  const deadline = Date.now() + timeout
  while (Date.now() < deadline) {
    try {
      const payload = await Bun.file(notificationFile).json()
      if (payload.type === "email_verification" && payload.recipient === email)
        return payload
    } catch {}
    await Bun.sleep(100)
  }
  throw new Error(`Registration notification was not delivered for ${email}`)
}

function registrationState(email) {
  if (!databaseContainer) {
    const database = new Database(databaseFile, { readonly: true })
    const principal = database
      .query(
        `SELECT p.email_verified, p.activation_required, COUNT(tm.tenant_id) AS memberships
         FROM iam_principals p
         LEFT JOIN iam_tenant_members tm ON tm.principal_id = p.id
         WHERE p.email = ?
         GROUP BY p.id, p.email_verified, p.activation_required`
      )
      .get(email)
    database.close()
    return principal
  }
  if (!/^[A-Za-z0-9_]*$/.test(databaseTablePrefix))
    throw new Error("IAM_DATABASE_TABLE_PREFIX is invalid")
  const encodedEmail = Buffer.from(email).toString("base64")
  const query = `SELECT p.email_verified, p.activation_required, COUNT(tm.tenant_id)
FROM ${databaseTablePrefix}iam_principals p
LEFT JOIN ${databaseTablePrefix}iam_tenant_members tm ON tm.principal_id = p.id
WHERE p.email = CONVERT(FROM_BASE64('${encodedEmail}') USING utf8mb4)
GROUP BY p.id, p.email_verified, p.activation_required`
  const dockerArgs = [
    "exec",
    databaseContainer,
    "sh",
    "-c",
    'MYSQL_PWD="$MYSQL_PASSWORD" exec mysql --host=127.0.0.1 --user="$MYSQL_USER" --database="$MYSQL_DATABASE" --batch --raw --skip-column-names --execute="$1"',
    "registration-audit",
    query,
  ]
  let commandName = "docker"
  let commandArgs = dockerArgs
  if (process.env.IAM_DOCKER_SSH) {
    const key = process.env.IAM_DOCKER_SSH_KEY
    if (!key)
      throw new Error("IAM_DOCKER_SSH_KEY is required for remote Docker")
    commandName = "ssh"
    // SSH sends a shell command; preserve SQL and container-shell arguments.
    const remoteCommand = ["docker", ...dockerArgs]
      .map((argument) => `'${argument.replaceAll("'", "'\"'\"'")}'`)
      .join(" ")
    commandArgs = [
      "-o",
      "BatchMode=yes",
      "-o",
      "IdentitiesOnly=yes",
      "-o",
      "ConnectTimeout=5",
      "-i",
      key,
      process.env.IAM_DOCKER_SSH,
      remoteCommand,
    ]
  }
  const result = spawnSync(commandName, commandArgs, {
    encoding: "utf8",
    timeout: 15000,
    windowsHide: true,
  })
  if (result.error) throw result.error
  if (result.status !== 0)
    throw new Error(`registration state query failed: ${result.stderr.trim()}`)
  const values = result.stdout.trim().split("\t").map(Number)
  if (values.length !== 3 || values.some((value) => !Number.isInteger(value)))
    return undefined
  return {
    email_verified: values[0],
    activation_required: values[1],
    memberships: values[2],
  }
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

await navigate("/login")
await waitFor(`document.querySelector('a[href="/register"]')`)
await navigate("/register")
await waitFor(`document.querySelector("#registration-email")`)

const suffix = Date.now().toString(36)
const email = `registration-${suffix}@example.test`
const password = `Registration password ${suffix}`
await setInput("#registration-email", email)
await setInput("#registration-name", `Registration ${suffix}`)
await setInput("#registration-password", password)
await setInput("#registration-confirm", password)

// The character challenge is unreadable to a script, so this run expects the
// deployment to be configured with a universal captcha answer and to be given
// the same string here. A deployment that leaves it unset has no universal
// answer at all, which is what makes this safe to keep in the repository.
const captchaAnswer =
  (process.env.IAM_CAPTCHA_TEST_ANSWER_FILE
    ? readFileSync(process.env.IAM_CAPTCHA_TEST_ANSWER_FILE, "utf8").trim()
    : process.env.IAM_CAPTCHA_TEST_ANSWER) ?? "acceptance-captcha-answer"
await waitFor(`document.querySelector("#registration-captcha")`, 20000)
await waitFor(
  `document.querySelector("#registration-captcha ~ button img")`,
  20000
)

// A wrong answer must say so; it used to be reported as an invalid email or
// password, which sends the user off fixing fields that were already correct.
await setInput("#registration-captcha", `${captchaAnswer}-wrong`)
await submit()
await waitFor(`document.body.innerText.includes("验证码不正确")`, 20000)
if (await evaluate(`return document.body.innerText.includes("至少 12 个字符")`))
  throw new Error(
    "A rejected captcha was reported as an invalid email or password"
  )

// Every challenge is single use, so the retry has to wait for a fresh image.
await waitFor(
  `document.querySelector("#registration-captcha ~ button img")`,
  20000
)
await setInput("#registration-captcha", captchaAnswer)
await submit()
await waitFor(`document.body.innerText.includes("请检查邮箱")`, 20000)

const notification = await registrationNotification(email)
const verificationURL = new URL(notification.verification_url)
if (!verificationURL.searchParams.get("token"))
  throw new Error("Verification notification has no token")

const layouts = [
  await capture("admin-registration-desktop.png", 1440, 1000),
  await capture("admin-registration-mobile.png", 390, 844),
]
await command("Emulation.setDeviceMetricsOverride", {
  width: 1440,
  height: 1000,
  deviceScaleFactor: 1,
  mobile: false,
})

await navigate(`${verificationURL.pathname}${verificationURL.search}`)
await waitFor(`document.body.innerText.includes("邮箱已验证")`, 20000)
if (await evaluate(`return location.search !== ""`))
  throw new Error("Verification token remained in the browser URL")

const principal = registrationState(email)
if (
  !principal ||
  principal.email_verified !== 1 ||
  principal.activation_required !== 0 ||
  principal.memberships !== 0
)
  throw new Error(`Unexpected registration state: ${JSON.stringify(principal)}`)

await navigate("/login")
await waitFor(`document.querySelector("#login-name")`)
await setInput("#login-name", email)
await setInput("#password", password)
await submit()
await waitFor(`location.pathname === "/"`, 20000)
await waitFor(
  `document.body.innerText.includes("此账户没有可用的活跃工作区。")`
)
await evaluate(`
  const session = await fetch("/api/sessions/current", { credentials: "include" });
  if (!session.ok || !(await session.json()).data?.subject)
    throw new Error("Registered principal did not establish an authenticated session");
  const tenants = await fetch("/api/me/tenants", { credentials: "include" });
  if (!tenants.ok || (await tenants.json()).data.length !== 0)
    throw new Error("Registration unexpectedly granted tenant membership");
  if (document.querySelector('a[href="/users"]'))
    throw new Error("Registered principal unexpectedly sees administrative navigation");
  const logout = await fetch("/api/sessions/current", { method: "DELETE", credentials: "include" });
  if (!logout.ok) throw new Error("Registration session logout failed");
  if ((await fetch("/api/sessions/current", { credentials: "include" })).status !== 401)
    throw new Error("Registration session remained active after logout");
`)

// The run deliberately submits one wrong captcha, so exactly one rejected
// registration belongs here; a second one would be a real regression.
let allowedRejectedRegistrations = 1
const unexpectedResponses = responses.filter((response) => {
  if (response.status < 400) return false
  if (
    response.status === 401 &&
    response.url.endsWith("/api/sessions/current")
  )
    return false
  if (
    response.status === 403 &&
    (response.url.includes("/api/me/menus") ||
      response.url.includes("/api/tenants"))
  )
    return false
  // A newly registered global principal has no tenant field-policy access.
  if (
    response.method === "GET" &&
    response.status === 403 &&
    response.url.endsWith("/api/field-catalog")
  )
    return false
  if (
    response.status === 422 &&
    response.url.endsWith("/api/registrations") &&
    allowedRejectedRegistrations > 0
  ) {
    allowedRejectedRegistrations -= 1
    return false
  }
  return true
})
if (allowedRejectedRegistrations !== 0)
  throw new Error("The wrong captcha was accepted instead of being rejected")
const unexpectedFailures = failures.filter(
  (failure) => !failure.canceled && failure.error !== "net::ERR_ABORTED"
)
socket.close()
if (consoleErrors.length)
  throw new Error(`console errors: ${JSON.stringify(consoleErrors)}`)
if (unexpectedFailures.length)
  throw new Error(`failed requests: ${JSON.stringify(unexpectedFailures)}`)
if (unexpectedResponses.length)
  throw new Error(
    `unexpected responses: ${JSON.stringify(unexpectedResponses)}`
  )

console.log(
  JSON.stringify({
    registration: true,
    emailVerified: true,
    activationCleared: true,
    tenantMemberships: principal.memberships,
    globalAccountWithoutWorkspace: true,
    login: true,
    layouts,
    consoleErrors,
    unexpectedFailures,
    unexpectedResponses,
  })
)
