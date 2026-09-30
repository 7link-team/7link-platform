/**
 * Prepares the account and tenant the browser audits sign in with.
 *
 * The audits used to assume both already existed, so a fresh database could not
 * run them and nothing recorded how the account they wanted had been made. This
 * script is that record: it creates the platform administrator through the
 * service's own bootstrap command, makes sure the tenant the console opens in
 * exists, and prints that tenant's id.
 *
 * Running it again changes nothing. The bootstrap command reports an already
 * bootstrapped platform as an outcome rather than an error, and the tenant step
 * looks the slug up before creating it, so this can sit in front of every audit
 * run without a first run and a later run differing.
 *
 * The password is never passed as an argument and never printed. It is either
 * handed to the command as a file it opens itself, or written to its standard
 * input.
 *
 *   IAM_BROWSER_PASSWORD_FILE=.local/audit-password \
 *   IAM_BOOTSTRAP_ARGS="--debug --authn.enabled --authn.issuer http://127.0.0.1:8080 ..." \
 *   bun scripts/bootstrap-audit-environment.mjs
 *
 * IAM_BOOTSTRAP_ARGS has to describe the same deployment the server runs with,
 * normally by naming its configuration file. The command drives the real module
 * lifecycle so that it writes ids, password hashes and audit entries exactly as
 * the running service would, and a lifecycle it cannot start is one it refuses
 * to write through.
 */

import { auditPassword } from "./audit-password.mjs"
import { auditCaptcha } from "./audit-captcha.mjs"

const api = (process.env.IAM_API_URL ?? "http://127.0.0.1:8080").replace(/\/$/, "")
const consoleURL = (
  process.env.IAM_CONSOLE_URL ?? "http://localhost:8091"
).replace(/\/$/, "")
const login = process.env.IAM_BROWSER_LOGIN ?? "admin@example.test"
const displayName =
  process.env.IAM_BROWSER_DISPLAY_NAME ?? "Platform Administrator"
const tenantSlug = process.env.IAM_BROWSER_TENANT_SLUG ?? "platform"
const tenantName = process.env.IAM_BROWSER_TENANT_NAME ?? "Platform"
const passwordFile = process.env.IAM_BROWSER_PASSWORD_FILE
const backendDir =
  process.env.IAM_BACKEND_DIR ??
  new URL(
    "../../../../backend-api/cmd/7link-api/",
    import.meta.url
  ).pathname.replace(/^\/(?=[A-Za-z]:)/, "")
const backendCommand = (process.env.IAM_BACKEND_COMMAND ?? "go run .").split(" ")
const bootstrapArgs = (
  process.env.IAM_BOOTSTRAP_ARGS ?? "--debug --authn.enabled"
)
  .split(" ")
  .filter(Boolean)

const password = auditPassword()

const note = (line) => process.stderr.write(line + "\n")

// Each status names a different thing to do next, so they are reported apart
// rather than collapsed into "the command failed".
const exitMeaning = {
  4: "another principal already holds this login",
  5: "the database schema is not ready — run `7link-api migrate` first",
  6: "the database was unavailable — retry",
  7: "the platform has no administrator to act on",
}

async function bootstrapAdministrator() {
  const child = Bun.spawn(
    [
      ...backendCommand,
      "iam",
      "bootstrap-platform-admin",
      ...bootstrapArgs,
      "--login",
      login,
      "--display-name",
      displayName,
      ...(passwordFile ? ["--password-file", passwordFile] : []),
    ],
    {
      cwd: backendDir,
      // With no file to point at, the command reads the password from standard
      // input, so it reaches neither an argument list nor a new file on disk.
      stdin: passwordFile ? "ignore" : new TextEncoder().encode(password),
      stdout: "pipe",
      stderr: "pipe",
    }
  )
  const [out, err, status] = await Promise.all([
    new Response(child.stdout).text(),
    new Response(child.stderr).text(),
    child.exited,
  ])
  const report = Object.fromEntries(
    out
      .split("\n")
      .map((line) => line.split(": "))
      .filter((pair) => pair.length === 2)
      .map(([key, value]) => [key.trim(), value.trim()])
  )
  // The report decides the outcome rather than the exit status, because a
  // wrapper can replace the status: `go run` turns any non-zero exit into 1 and
  // prints the real one, which would otherwise read as a failed bootstrap when
  // the platform was merely already bootstrapped.
  if (report.principal_id) {
    note(`administrator ${report.outcome}: ${report.login}`)
    return report.principal_id
  }
  const reported = Number(err.match(/exit status (\d+)/)?.[1] ?? status)
  throw new Error(
    `bootstrap-platform-admin exited ${reported}` +
      (exitMeaning[reported] ? ` (${exitMeaning[reported]})` : "") +
      `\n${err.trim() || out.trim()}`
  )
}

let cookie = ""

async function call(path, init = {}) {
  const response = await fetch(api + path, {
    ...init,
    headers: {
      "Content-Type": "application/json",
      // The service refuses a browser flow that arrives without the origin it
      // was configured to serve, so the script presents the console's own.
      Origin: consoleURL,
      ...(cookie ? { Cookie: cookie } : {}),
      ...(init.headers ?? {}),
    },
    redirect: "manual",
  })
  const issued = response.headers.getSetCookie?.() ?? []
  if (issued.length)
    cookie = [cookie, ...issued.map((value) => value.split(";")[0])]
      .filter(Boolean)
      .join("; ")
  const body = await response.json().catch(() => null)
  if (!response.ok)
    throw new Error(
      `${init.method ?? "GET"} ${path} → ${response.status} ${body?.message ?? ""}`
    )
  return body?.data
}

async function ensureTenant(ownerPrincipalID) {
  await call("/login", {
    method: "POST",
    body: JSON.stringify({
      login_name: login,
      password,
      return_url: consoleURL + "/",
      ...auditCaptcha(),
    }),
  })
  // Ask the service for this one tenant rather than for all of them: the
  // listing is a page, and it was only ever right here while the deployment
  // held fewer tenants than one page. Past that the tenant it wanted fell off
  // the end, the script decided it was absent, and creating it failed on the
  // slug that was there all along.
  const existing =
    (await call(
      "/tenants?include_deleted=false&limit=200&search=" +
        encodeURIComponent(tenantSlug)
    )) ?? []
  const found = existing.find((tenant) => tenant.slug === tenantSlug)
  if (found) {
    note(`tenant present: ${found.slug}`)
    return found.id
  }
  // Tenant creation installs the owner, the tenant administrator role and the
  // default menus in one transaction, so the account bootstrapped above can
  // administer this tenant as soon as the call returns.
  const created = await call("/tenants", {
    method: "POST",
    body: JSON.stringify({
      slug: tenantSlug,
      name: tenantName,
      owner_principal_id: ownerPrincipalID ?? (await call("/me")).subject,
    }),
  })
  note(`tenant created: ${created.slug}`)
  return created.id
}

const tenantID = await ensureTenant(
  process.env.IAM_BOOTSTRAP_SKIP === "true"
    ? undefined
    : await bootstrapAdministrator()
)
// The id alone goes to standard output, so a caller can read it directly:
//   IAM_BROWSER_TENANT=$(bun scripts/bootstrap-audit-environment.mjs)
process.stdout.write(tenantID + "\n")
