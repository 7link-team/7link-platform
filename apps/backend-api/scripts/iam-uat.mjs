#!/usr/bin/env node
// Local-only UAT orchestration. Domain initialization stays in the existing CLI/API.
import { spawn, spawnSync } from "node:child_process";
import assert from "node:assert/strict";
import { createHash, randomBytes } from "node:crypto";
import { existsSync, readFileSync, writeFileSync, mkdirSync, renameSync, unlinkSync, openSync, closeSync } from "node:fs";
import { createServer as createHTTPServer } from "node:http";
import { createServer as createNetServer } from "node:net";
import { dirname, join, resolve } from "node:path";
import { fileURLToPath } from "node:url";
import { setTimeout as delay } from "node:timers/promises";

const script = fileURLToPath(import.meta.url);
const root = resolve(dirname(script), "..");
const frontend = resolve(root, "../frontend-web-admin/apps/iam");
const directory = join(root, ".temp/iam-uat");
const identity = createHash("sha256").update(root).digest("hex").slice(0, 12);
const container = `iam-uat-${identity}`;
const label = "local.iam-uat.workspace";
const title = `iam-uat-supervisor:${identity}`;
const configPath = join(directory, "config.yaml");
const statePath = join(directory, "state.json");
const pidPath = join(directory, "supervisor.pid");
const readyPath = join(directory, "ready.json");
const binary = join(directory, "api");
const notificationPath = join(directory, "notification-last.json");
process.umask(0o077);

function run(command, args, options = {}) {
  const result = spawnSync(command, args, { cwd: root, encoding: "utf8", stdio: "pipe", ...options });
  if (result.error) throw result.error;
  if (result.status !== 0) throw new Error(`${command} failed (${result.status}); ${result.stderr || result.stdout || "see UAT log"}`);
  return result.stdout?.trim() ?? "";
}
function save(path, value) {
  writeFileSync(path, JSON.stringify(value, null, 2) + "\n", { mode: 0o600 });
}
function state() {
  if (!existsSync(statePath)) throw new Error("Run init first.");
  return JSON.parse(readFileSync(statePath, "utf8"));
}
function inspect() {
  const result = spawnSync("docker", ["container", "inspect", container], { encoding: "utf8" });
  if (result.status !== 0) {
    if (result.stderr?.includes("No such container") || result.stderr?.includes("No such object")) return null;
    throw new Error("Cannot inspect Docker; check that the local daemon is running.");
  }
  const info = JSON.parse(result.stdout)[0];
  if (info.Config.Labels?.[label] !== identity) throw new Error("Refusing a container without this workspace's UAT ownership label.");
  return info;
}
function localDocker() {
  if (process.env.DOCKER_HOST || process.env.DOCKER_CONTEXT) throw new Error("Unset Docker endpoint overrides; UAT only uses the current local Docker context.");
  const context = JSON.parse(run("docker", ["context", "inspect"]));
  if (!context[0].Endpoints.docker.Host.startsWith("unix://")) throw new Error("UAT requires a local Unix-socket Docker context.");
  run("docker", ["info", "--format", "{{.ServerVersion}}"]);
}
async function mysqlReady() {
  await waitFor(() => spawnSync("docker", ["exec", container, "sh", "-c", 'MYSQL_PWD="$MYSQL_PASSWORD" mysqladmin ping --host=127.0.0.1 --user=iam_uat --silent'], { stdio: "ignore" }).status === 0, "MySQL startup");
}
async function availablePort(preferred) {
  return new Promise((resolvePort, reject) => {
    const server = createNetServer();
    server.once("error", (error) => {
      if (error.code === "EADDRINUSE" && preferred !== 0) availablePort(0).then(resolvePort, reject);
      else reject(error);
    });
    server.listen(preferred, "127.0.0.1", () => {
      const port = server.address().port;
      server.close(() => resolvePort(port));
    });
  });
}
async function waitFor(check, description, attempts = 90) {
  for (let attempt = 0; attempt < attempts; attempt++) {
    if (await check()) return;
    await delay(1000);
  }
  throw new Error(`Timed out: ${description}. Inspect ${join(directory, "uat.log")}`);
}
async function httpReady(url) {
  try { return (await fetch(url, { signal: AbortSignal.timeout(1500) })).ok; }
  catch { return false; }
}
function supervisor() {
  if (!existsSync(pidPath)) return null;
  const pid = Number(readFileSync(pidPath, "utf8").trim());
  if (!Number.isSafeInteger(pid) || pid < 2) throw new Error("Invalid supervisor PID file.");
  const result = spawnSync("ps", ["-p", String(pid), "-o", "command="], { encoding: "utf8" });
  if (result.status !== 0) return null;
  if (result.stdout.trim() !== title) throw new Error("PID was reused or does not belong to this UAT supervisor; refusing to signal it.");
  return pid;
}
async function init() {
  localDocker();
  if (existsSync(statePath)) { console.log("Already initialized; use start or reset."); return; }
  if (existsSync(directory) || inspect()) throw new Error("Partial initialization exists. Inspect it and use reset to preserve it before retrying.");
  mkdirSync(directory, { recursive: true, mode: 0o700 });
  const mysqlPassword = randomBytes(24).toString("hex");
  writeFileSync(join(directory, "mysql.env"), `MYSQL_ROOT_PASSWORD=${randomBytes(24).toString("hex")}\nMYSQL_DATABASE=iam_uat\nMYSQL_USER=iam_uat\nMYSQL_PASSWORD=${mysqlPassword}\n`, { mode: 0o600 });
  for (const key of ["signing-key", "mfa-key", "federation-key", "provisioning-key"]) {
    writeFileSync(join(directory, key), randomBytes(32).toString("base64") + "\n", { mode: 0o600 });
  }
  const captchaTestAnswer = randomBytes(24).toString("base64url");
  writeFileSync(join(directory, "captcha-test-answer"), captchaTestAnswer + "\n", { mode: 0o600 });
  writeFileSync(join(directory, "admin-password"), randomBytes(24).toString("base64url") + "aA1!\n", { mode: 0o600 });
  const image = readFileSync(join(root, "Makefile"), "utf8").match(/^MYSQL8_IMAGE \?= (.+)$/m)?.[1];
  if (!image?.includes("@sha256:")) throw new Error("Missing pinned MySQL image in Makefile.");
  const mysqlPort = await availablePort(13306);
  run("docker", ["run", "--detach", "--name", container, "--label", `${label}=${identity}`,
    "--env-file", join(directory, "mysql.env"), "--publish", `127.0.0.1:${mysqlPort}:3306`, image,
    "--log-bin-trust-function-creators=ON", "--character-set-server=utf8mb4", "--collation-server=utf8mb4_0900_ai_ci"]);
  await mysqlReady();
  const databasePort = Number(inspect().NetworkSettings.Ports["3306/tcp"][0].HostPort);
  const apiPort = await availablePort(18080);
  const webPort = await availablePort(18091);
  const grpcPort = await availablePort(19090);
  const notificationPort = await availablePort(18100);
  const apiURL = `http://127.0.0.1:${apiPort}`;
  const webURL = `http://127.0.0.1:${webPort}`;
  const passkeyURL = `http://localhost:${webPort}`;
  save(configPath, {
    name: "IAM Local UAT", table_prefix: "uat_",
    http_server: { address: "127.0.0.1", port: apiPort },
    grpc_server: { address: "127.0.0.1", port: grpcPort },
    database: { main: { type: "mysql", dsn: `iam_uat:${mysqlPassword}@tcp(127.0.0.1:${databasePort})/iam_uat?parseTime=true&multiStatements=true&charset=utf8mb4`, writable: true, readable: true } },
    authn: { enabled: true, issuer: apiURL, signing_key_file: join(directory, "signing-key"),
      mfa: { encryption_key_file: join(directory, "mfa-key") },
      notification: { url: `http://127.0.0.1:${notificationPort}/notifications`, poll_interval: "100ms", request_timeout: "5s", max_attempts: 3 },
      email_verification: { enabled: true, verify_url: webURL + "/verify-email" },
      registration: { enabled: true, captcha: { test_answer: captchaTestAnswer } },
      passkey: { enabled: true, rp_id: "localhost", display_name: "IAM Local UAT", origins: [passkeyURL] },
      web: { enabled: true, cookie_name: `uat_${identity}`, cookie_secure: false,
        post_login_url: webURL + "/", post_logout_url: webURL + "/login",
        allowed_return_urls: [webURL + "/", passkeyURL + "/"], allowed_origins: [webURL, passkeyURL] } },
    // Local protocol audits run their test IdP on an ephemeral loopback port.
    federation: { enabled: true, encryption_key_file: join(directory, "federation-key"),
      outbound_allow_plaintext: true, outbound_allow_networks: ["127.0.0.1/32"] },
    provisioning: { encryption_key_file: join(directory, "provisioning-key") },
  });
  console.log("Building the backend and initializing the isolated schema...");
  run("make", ["build", `OUTPUT=${binary}`], { stdio: "inherit" });
  run(binary, ["migrate", "auto", "--config", configPath]);
  run(binary, ["iam", "bootstrap-platform-admin", "--config", configPath,
    "--login", "admin@example.test", "--display-name", "UAT Administrator", "--password-file", join(directory, "admin-password")]);
  save(statePath, { apiURL, webURL, apiPort, webPort, grpcPort, notificationPort, databasePort, container, image,
    initializedAt: new Date().toISOString(), revision: run("git", ["rev-parse", "HEAD"]),
    binarySHA256: createHash("sha256").update(readFileSync(binary)).digest("hex") });
  console.log(`Initialized. Password file: ${join(directory, "admin-password")} (not printed).`);
}
async function serve() {
  process.title = title;
  const settings = state();
  writeFileSync(pidPath, String(process.pid), { flag: "wx", mode: 0o600 });
  const children = [];
  const notificationServer = settings.notificationPort ? createHTTPServer((request, response) => {
    if (request.method !== "POST" || request.url !== "/notifications") {
      response.writeHead(404).end();
      return;
    }
    let body = "";
    let oversized = false;
    request.setEncoding("utf8");
    request.on("data", (chunk) => {
      if (oversized) return;
      body += chunk;
      if (Buffer.byteLength(body) > 1024 * 1024) {
        oversized = true;
        body = "";
        response.writeHead(413).end();
      }
    });
    request.on("end", () => {
      if (oversized) return;
      try {
        save(notificationPath, JSON.parse(body));
        response.writeHead(204).end();
      } catch {
        response.writeHead(400).end();
      }
    });
  }) : null;
  let stopping = false;
  let stopped;
  const completion = new Promise((resolveStop) => { stopped = resolveStop; });
  const stop = async () => {
    if (stopping) return;
    stopping = true;
    if (existsSync(readyPath)) unlinkSync(readyPath);
    for (const child of children) child.kill("SIGTERM");
    const force = setTimeout(() => { for (const child of children) child.kill("SIGKILL"); }, 35000);
    await Promise.all(children.map((child) => child.finished));
    clearTimeout(force);
    if (notificationServer?.listening) {
      await new Promise((resolveClose) => notificationServer.close(resolveClose));
    }
    unlinkSync(pidPath);
    stopped();
  };
  process.on("SIGTERM", stop);
  process.on("SIGINT", stop);
  const launch = (command, args, options) => {
    if (stopping) throw new Error("UAT startup was interrupted.");
    const child = spawn(command, args, { stdio: "inherit", ...options });
    child.finished = new Promise((done) => child.once("close", done));
    children.push(child);
    child.on("error", (error) => { console.error(error.message); process.exitCode = 1; void stop(); });
    child.on("exit", () => { if (!stopping) { process.exitCode = 1; void stop(); } });
    return child;
  };
  try {
    if (notificationServer) {
      await new Promise((resolveListen, reject) => {
        notificationServer.once("error", reject);
        notificationServer.listen(settings.notificationPort, "127.0.0.1", resolveListen);
      });
    }
    launch(binary, ["serve", "--config", configPath], { cwd: root });
    await waitFor(async () => { if (stopping) throw new Error("API exited during startup."); return httpReady(settings.apiURL + "/readyz"); }, "API readiness");
    const tenant = run("bun", ["scripts/bootstrap-audit-environment.mjs"], { cwd: frontend,
      env: { ...process.env, IAM_API_URL: settings.apiURL, IAM_CONSOLE_URL: settings.webURL,
        IAM_BROWSER_PASSWORD_FILE: join(directory, "admin-password"), IAM_BROWSER_LOGIN: "admin@example.test",
        IAM_BROWSER_TENANT_SLUG: "uat", IAM_BROWSER_TENANT_NAME: "UAT",
        IAM_BACKEND_DIR: root, IAM_BACKEND_COMMAND: "./.temp/iam-uat/api", IAM_BOOTSTRAP_ARGS: "--config .temp/iam-uat/config.yaml" } });
    writeFileSync(join(directory, "tenant-id"), tenant + "\n", { mode: 0o600 });
    const demoAccounts = process.argv.includes("--demo-accounts");
    const prefillDemoAccount = process.argv.includes("--prefill-demo-account");
    if (prefillDemoAccount && !demoAccounts) throw new Error("--prefill-demo-account requires --demo-accounts");
    const personaFixture = demoAccounts
      ? JSON.parse(run("bun", ["scripts/bootstrap-file-personas.mjs"], { cwd: frontend,
        env: { ...process.env, IAM_API_URL: settings.apiURL, IAM_CONSOLE_URL: settings.webURL,
          IAM_BROWSER_PASSWORD_FILE: join(directory, "admin-password"), IAM_BROWSER_TENANT: tenant,
          IAM_UAT_DIRECTORY: directory } }))
      : { accounts: [] };
    if (demoAccounts) save(join(directory, "file-personas.json"), { ...personaFixture,
      accounts: personaFixture.accounts.map(({ password: _password, ...account }) => account) });
    launch("bun", ["run", "dev", "--host", "127.0.0.1", "--port", String(settings.webPort), "--strictPort"],
      { cwd: frontend, env: { ...process.env, VITE_API_PROXY_TARGET: settings.apiURL,
        VITE_DEFAULT_TENANT: tenant, VITE_DEV_LOGIN_ACCOUNTS: JSON.stringify(personaFixture.accounts),
        VITE_DEV_LOGIN_ACCOUNTS_ENABLED: String(demoAccounts), VITE_DEV_LOGIN_PREFILL: String(prefillDemoAccount) } });
    await waitFor(async () => { if (stopping) throw new Error("UAT process exited during startup."); return httpReady(settings.webURL + "/login"); }, "console startup");
    save(readyPath, { pid: process.pid, ...settings });
    console.log(`UAT ready: ${settings.webURL}`);
    await completion;
  } finally { await stop(); }
}
async function start() {
  const demoFlags = process.argv.slice(3).filter((arg) => arg === "--demo-accounts" || arg === "--prefill-demo-account");
  if (demoFlags.includes("--prefill-demo-account") && !demoFlags.includes("--demo-accounts"))
    throw new Error("--prefill-demo-account requires --demo-accounts");
  localDocker();
  const settings = state();
  if (supervisor()) {
    if (!existsSync(readyPath) || !await httpReady(settings.apiURL + "/readyz")) throw new Error("UAT supervisor exists but is not ready; inspect uat.log.");
    console.log(`Already running: ${settings.webURL}`); return;
  }
  if (existsSync(pidPath)) unlinkSync(pidPath);
  if (existsSync(readyPath)) unlinkSync(readyPath);
  if (!inspect()) throw new Error("UAT database container is missing; do not recreate it over existing credentials.");
  run("docker", ["start", container]);
  await mysqlReady();
  const databasePort = Number(inspect().NetworkSettings.Ports["3306/tcp"][0].HostPort);
  if (databasePort !== settings.databasePort) throw new Error("Docker changed the database port; refusing to use a stale database address.");
  for (const port of [settings.apiPort, settings.webPort, settings.grpcPort, settings.notificationPort].filter(Number.isInteger)) {
    if (await availablePort(port) !== port) throw new Error(`Port ${port} is occupied; stop the conflicting process before starting this UAT.`);
  }
  const log = openSync(join(directory, "uat.log"), "a", 0o600);
  const child = spawn(process.execPath, [script, "serve", ...demoFlags], { cwd: root, detached: true, stdio: ["ignore", log, log] });
  child.unref();
  closeSync(log);
  try {
    await waitFor(() => { if (child.exitCode !== null) throw new Error("UAT supervisor exited; see uat.log."); return existsSync(readyPath); }, "UAT startup", 120);
  } catch (error) { await stop(); throw error; }
  console.log(`Console: ${settings.webURL}\nAPI: ${settings.apiURL}\nLogin: admin@example.test\nPassword file: ${join(directory, "admin-password")}`);
}
async function stop() {
  localDocker();
  const pid = supervisor();
  if (pid) {
    process.kill(pid, "SIGTERM");
    await waitFor(() => !existsSync(pidPath), "graceful UAT stop", 45);
  }
  if (inspect()?.State.Running) run("docker", ["stop", container]);
  console.log("UAT stopped; database and credentials retained.");
}
async function reset() {
  if (process.argv[3] !== "--preserve-old") throw new Error("Use reset --preserve-old; old data and credentials will be retained, not deleted.");
  await stop();
  const suffix = new Date().toISOString().replace(/[:.]/g, "-");
  if (inspect()) run("docker", ["rename", container, `${container}-saved-${suffix}`]);
  if (existsSync(directory)) renameSync(directory, `${directory}-saved-${suffix}`);
  console.log(`Previous UAT preserved with suffix ${suffix}. Run init, then start for a fresh UAT.`);
}
async function verify() {
  const settings = state();
  if (!supervisor() || !existsSync(readyPath)) throw new Error("Start UAT before verification.");
  const tenant = readFileSync(join(directory, "tenant-id"), "utf8").trim();
  const client = () => {
    const cookies = new Map();
    return async (path, { method = "GET", body, expected = 200, origin = settings.webURL } = {}) => {
      const response = await fetch(settings.webURL + "/api" + path, {
        method, redirect: "manual", signal: AbortSignal.timeout(10000),
        headers: { "Content-Type": "application/json", Origin: origin,
          Cookie: [...cookies].map(([key, value]) => `${key}=${value}`).join("; ") },
        ...(body ? { body: JSON.stringify(body) } : {}),
      });
      for (const issued of response.headers.getSetCookie()) {
        const pair = issued.split(";")[0];
        const separator = pair.indexOf("=");
        cookies.set(pair.slice(0, separator), pair.slice(separator + 1));
      }
      assert.equal(response.status, expected, `${method} ${path}`);
      return (await response.json().catch(() => null))?.data;
    };
  };
  const login = async (call, name, file) => {
    const result = await call("/login", { method: "POST", body: {
      login_name: name, password: readFileSync(join(directory, file), "utf8").trim(), return_url: settings.webURL + "/",
    } });
    assert.equal(result.status, "authenticated");
    await call("/me/session");
  };
  const admin = client();
  const member = client();
  const personasPath = join(directory, "personas.json");
  const personas = existsSync(personasPath) ? JSON.parse(readFileSync(personasPath, "utf8")) : null;
  let cleanup;
  await admin("/readyz");
  await login(admin, "admin@example.test", "admin-password");
  try {
    const listing = await admin(`/tenants/${tenant}/principals?limit=200&search=member%40example.test`);
    let principal = listing.items.find((item) => item.login_name === "member@example.test");
    if (personas) {
      assert.equal(tenant, personas.tenantID, "UAT tenant must survive restart");
      assert.equal(principal?.id, personas.memberID, "UAT member must survive restart; do not silently recreate it");
    }
    if (!principal) {
      const passwordPath = join(directory, "member-password");
      if (!existsSync(passwordPath)) writeFileSync(passwordPath, randomBytes(24).toString("base64url") + "aA1!\n", { mode: 0o600 });
      principal = await admin(`/tenants/${tenant}/principals`, { method: "POST", expected: 201, body: {
        login_name: "member@example.test", password: readFileSync(passwordPath, "utf8").trim(), display_name: "UAT Member",
      } });
    }
    assert.deepEqual(await admin(`/tenants/${tenant}/members/${encodeURIComponent(principal.id)}/roles`), []);
    await login(member, "member@example.test", "member-password");
    if (!personas) save(personasPath, { tenantID: tenant, memberID: principal.id });
    await member(`/tenants/${tenant}/principals`, { expected: 403 });
    await member(`/platform/principals/${encodeURIComponent(principal.id)}/lifecycle`, { expected: 403 });
    await admin("/me/session", { method: "DELETE", expected: 403, origin: "https://untrusted.invalid" });
    await admin("/me/session");
  } finally {
    cleanup = await Promise.allSettled([
      member("/me/session", { method: "DELETE" }),
      admin("/me/session", { method: "DELETE" }),
    ]);
  }
  for (const result of cleanup) if (result.status === "rejected") throw result.reason;
  await admin("/me/session", { expected: 401 });
  await member("/me/session", { expected: 401 });
  const report = { at: new Date().toISOString(), binarySHA256: settings.binarySHA256, result: "PASS",
    checks: ["proxy-readiness", "admin-login", "member-login", "stable-persona-identity", "member-has-no-roles", "tenant-admin-denied", "platform-lifecycle-denied", "cross-origin-write-denied", "logout-invalidates-session"],
    browserAcceptance: "NOT_RUN", productionAcceptance: "NOT_READY" };
  save(join(directory, "verification.json"), report);
  console.log(JSON.stringify(report, null, 2));
}

try {
  switch (process.argv[2]) {
    case "init": await init(); break;
    case "start": await start(); break;
    case "serve": await serve(); break;
    case "stop": await stop(); break;
    case "reset": await reset(); break;
    case "verify": await verify(); break;
    case "status": {
      const settings = state();
      const running = Boolean(supervisor());
      console.log(JSON.stringify({ ...settings, running,
        ready: running && existsSync(readyPath) && await httpReady(settings.apiURL + "/readyz") && await httpReady(settings.webURL + "/login") }, null, 2));
      break;
    }
    default: throw new Error("Usage: node scripts/iam-uat.mjs init|start [--demo-accounts] [--prefill-demo-account]|stop|status|verify|reset --preserve-old");
  }
} catch (error) { console.error(error.message); process.exitCode = 1; }
