#!/usr/bin/env node
import { spawn, spawnSync } from "node:child_process";
import { randomBytes } from "node:crypto";
import {
  appendFileSync,
  createWriteStream,
  existsSync,
  mkdirSync,
  readFileSync,
  unlinkSync,
  writeFileSync,
} from "node:fs";
import { createServer } from "node:net";
import { createServer as createHTTPServer } from "node:http";
import { dirname, join, resolve } from "node:path";
import { fileURLToPath } from "node:url";
import { setTimeout as delay } from "node:timers/promises";

const root = resolve(dirname(fileURLToPath(import.meta.url)), "..");
const frontend = resolve(root, "../frontend-web-admin/apps/iam");
const directory = join(root, ".temp/dev");
const statePath = join(directory, "runtime.json");
const stopPath = join(directory, "stop-request");
const configPath = join(directory, "config.json");
const binary = join(
  directory,
  process.platform === "win32" ? "api.exe" : "api",
);
const initializedPath = join(directory, "admin-initialized");
// A literal is fine here and only here: this is the local development
// environment's own throwaway value, it never leaves this machine, and the
// service refuses anything shorter than eight characters.
const devCaptchaAnswer = "dev-captcha";
const options = new Map();
for (let i = 2; i < process.argv.length; i += 2) {
  const name = process.argv[i];
  if (name === "--status" || name === "--stop") {
    options.set(name, true);
    break;
  }
  if (
    ![
      "--api-port",
      "--web-port",
      "--demo-accounts",
      "--prefill-account",
    ].includes(name) ||
    process.argv[i + 1] === undefined
  )
    throw new Error(`Invalid development option: ${name}`);
  options.set(name, process.argv[i + 1]);
}
function boolean(name, fallback) {
  const value = options.get(name) ?? fallback;
  if (value !== "true" && value !== "false")
    throw new Error(`${name} requires true or false`);
  return value === "true";
}
function portOption(name, fallback) {
  const value = Number(options.get(name) ?? fallback);
  if (!Number.isInteger(value) || value < 1 || value > 65535)
    throw new Error(`${name} requires a port from 1 to 65535`);
  return value;
}
function run(command, args, cwd = root, env = {}, allowedStatuses = [0]) {
  return new Promise((done, reject) => {
    const child = spawn(command, args, {
      cwd,
      env: { ...process.env, ...env },
      windowsHide: true,
    });
    let stdout = "";
    let stderr = "";
    child.stdout.on("data", (chunk) => {
      stdout += chunk;
    });
    child.stderr.on("data", (chunk) => {
      stderr += chunk;
    });
    child.on("error", reject);
    child.on("close", (code) =>
      allowedStatuses.includes(code)
        ? done(stdout.trim())
        : reject(new Error(`${command} failed: ${stderr || stdout}`)),
    );
  });
}
async function ready(url) {
  try {
    return (await fetch(url, { signal: AbortSignal.timeout(1500) })).ok;
  } catch {
    return false;
  }
}
function print(settings) {
  console.log(
    `Web: ${settings.webURL}/user/login\nSwagger: ${settings.apiURL}/docs\nAPI: ${settings.apiURL}\nDatabase: SQLite (${join(directory, "dev.sqlite")})\nDemo account selector: ${settings.demoAccounts ? "enabled" : "disabled"}\nAccount: admin@example.test\nPassword file: ${join(directory, "admin-password")}\nCtrl+C stops both services; data and credentials are retained.`,
  );
  for (const entry of ["system", "platform", "tenant", "user"])
    console.log(
      `${entry} login: ${settings.webURL}/${entry === "tenant" ? "admin" : entry}/login`,
    );
  console.log(`Local development mail: ${join(directory, "mailbox.jsonl")}`);
}
async function availablePort(preferred) {
  return new Promise((resolvePort, reject) => {
    const server = createServer();
    server.once("error", (error) => {
      if (error.code === "EADDRINUSE" && preferred)
        availablePort(0).then(resolvePort, reject);
      else reject(error);
    });
    server.listen(preferred, "127.0.0.1", () => {
      const port = server.address().port;
      server.close(() => resolvePort(port));
    });
  });
}
if (options.has("--stop")) {
  if (
    !existsSync(statePath) ||
    !JSON.parse(readFileSync(statePath, "utf8")).running
  ) {
    console.log("Development environment is already stopped.");
  } else {
    writeFileSync(stopPath, "stop\n", { mode: 0o600 });
    for (let i = 0; JSON.parse(readFileSync(statePath, "utf8")).running; i++) {
      if (i === 40)
        throw new Error(
          "Development shutdown did not finish; inspect logs before restarting.",
        );
      await delay(500);
    }
    console.log(
      "Development environment stopped; data and credentials retained.",
    );
  }
} else if (options.has("--status")) {
  if (!existsSync(statePath))
    throw new Error(
      "Development environment has not been started; run make dev.",
    );
  const settings = JSON.parse(readFileSync(statePath, "utf8"));
  const running =
    settings.running &&
    (await ready(settings.apiURL + "/readyz")) &&
    (await ready(settings.webURL + "/user/login"));
  console.log(`Status: ${running ? "running" : "stopped"}`);
  print(settings);
  process.exitCode = running ? 0 : 1;
} else {
  const demoAccounts = boolean("--demo-accounts", "true");
  const prefill = boolean("--prefill-account", "false");
  if (prefill && !demoAccounts)
    throw new Error("Account prefill requires demo accounts.");
  const preferredAPI = portOption("--api-port", 8080);
  const preferredWeb = portOption("--web-port", 8091);
  if (preferredAPI === preferredWeb)
    throw new Error("API and Web ports must differ.");
  mkdirSync(directory, { recursive: true, mode: 0o700 });
  if (existsSync(statePath)) {
    const previous = JSON.parse(readFileSync(statePath, "utf8"));
    if (
      previous.running &&
      (await ready(previous.apiURL + "/readyz")) &&
      (await ready(previous.webURL + "/user/login"))
    ) {
      throw new Error(
        `Development environment is already running at ${previous.webURL}; stop it before changing startup settings.`,
      );
    }
  }
  const apiPort = await availablePort(preferredAPI);
  const webPort = await availablePort(preferredWeb);
  const grpcPort = await availablePort(0);
  const settings = {
    apiURL: `http://127.0.0.1:${apiPort}`,
    webURL: `http://127.0.0.1:${webPort}`,
    demoAccounts,
    pid: process.pid,
    running: false,
  };
  for (const name of [
    "signing-key",
    "mfa-key",
    "federation-key",
    "provisioning-key",
  ])
    if (!existsSync(join(directory, name)))
      writeFileSync(join(directory, name), randomBytes(32).toString("base64"), {
        mode: 0o600,
      });
  if (!existsSync(join(directory, "admin-password")))
    writeFileSync(
      join(directory, "admin-password"),
      randomBytes(24).toString("base64url") + "aA1!\n",
      { mode: 0o600 },
    );
  const mailboxPath = join(directory, "mailbox.jsonl");
  const mailbox = createHTTPServer(async (request, response) => {
    if (request.method !== "POST") {
      response.writeHead(405).end();
      return;
    }
    try {
      const chunks = [];
      let size = 0;
      for await (const chunk of request) {
        size += chunk.length;
        if (size > 65536) throw new Error("notification too large");
        chunks.push(chunk);
      }
      const message = JSON.parse(Buffer.concat(chunks).toString("utf8"));
      appendFileSync(mailboxPath, JSON.stringify(message) + "\n", {
        mode: 0o600,
      });
      response.writeHead(204).end();
    } catch {
      response.writeHead(400).end();
    }
  });
  await new Promise((resolveMailbox, reject) => {
    mailbox.once("error", reject);
    mailbox.listen(0, "127.0.0.1", resolveMailbox);
  });
  const notificationURL = `http://127.0.0.1:${mailbox.address().port}/notifications`;
  writeFileSync(
    configPath,
    JSON.stringify(
      {
        name: "Local Development",
        table_prefix: "dev_",
        http_server: { address: "127.0.0.1", port: apiPort },
        grpc_server: { address: "127.0.0.1", port: grpcPort },
        database: {
          main: {
            type: "sqlite",
            dsn: join(directory, "dev.sqlite"),
            writable: true,
            readable: true,
          },
        },
        files: { local_root: join(directory, "files") },
        iam: { tenant_registration_enabled: true },
        authn: {
          enabled: true,
          issuer: settings.apiURL,
          signing_key_file: join(directory, "signing-key"),
          mfa: { encryption_key_file: join(directory, "mfa-key") },
          registration: { enabled: true },
          captcha: { test_answer: devCaptchaAnswer },
          notification: { url: notificationURL },
          email_verification: {
            enabled: true,
            verify_url: settings.webURL + "/user/verify-email",
          },
          recovery: {
            enabled: true,
            reset_url: settings.webURL + "/user/recover",
          },
          web: {
            enabled: true,
            cookie_name: "dev_session",
            cookie_secure: false,
            post_login_url: settings.webURL + "/",
            post_logout_url: settings.webURL + "/user/login",
            allowed_return_urls: [settings.webURL + "/"],
            allowed_origins: [settings.webURL],
          },
        },
        federation: {
          enabled: true,
          encryption_key_file: join(directory, "federation-key"),
        },
        provisioning: {
          encryption_key_file: join(directory, "provisioning-key"),
        },
      },
      null,
      2,
    ) + "\n",
    { mode: 0o600 },
  );

  const children = [];
  if (existsSync(stopPath)) unlinkSync(stopPath);
  const stopWatcher = setInterval(() => {
    if (existsSync(stopPath)) void stop();
  }, 300);
  let stopping = false;
  let resolveStopped;
  const stopped = new Promise((done) => {
    resolveStopped = done;
  });
  function terminate(child, force) {
    if (!child.pid) return;
    if (child.exitCode !== null || child.signalCode !== null) return;
    if (process.platform === "win32")
      spawnSync(
        "taskkill",
        ["/PID", String(child.pid), "/T", ...(force ? ["/F"] : [])],
        { stdio: "ignore", windowsHide: true },
      );
    else {
      try {
        process.kill(-child.pid, force ? "SIGKILL" : "SIGTERM");
      } catch (error) {
        if (error.code !== "ESRCH") throw error;
      }
    }
  }
  async function stop() {
    if (stopping) return stopped;
    stopping = true;
    clearInterval(stopWatcher);
    const deadline = setTimeout(
      () => children.forEach((child) => terminate(child, true)),
      5000,
    );
    for (const child of [...children].reverse()) terminate(child, false);
    await Promise.all(children.map((child) => child.finished));
    await new Promise((resolveMailbox) => mailbox.close(resolveMailbox));
    clearTimeout(deadline);
    settings.running = false;
    writeFileSync(statePath, JSON.stringify(settings, null, 2) + "\n", {
      mode: 0o600,
    });
    resolveStopped();
  }
  function launch(command, args, cwd, name, env = {}) {
    const log = createWriteStream(join(directory, `${name}.log`), {
      mode: 0o600,
    });
    const child = spawn(command, args, {
      cwd,
      env: { ...process.env, ...env },
      detached: true,
      windowsHide: true,
      stdio: ["ignore", "pipe", "pipe"],
    });
    child.stdout.pipe(log, { end: false });
    child.stderr.pipe(log, { end: false });
    child.finished = new Promise((done) =>
      child.once("close", () => {
        log.end();
        done();
      }),
    );
    children.push(child);
    child.on("error", (error) => {
      console.error(error.message);
      process.exitCode = 1;
      void stop();
    });
    child.on("exit", () => {
      if (!stopping) {
        console.error(`${name} exited; see ${join(directory, name + ".log")}`);
        process.exitCode = 1;
        void stop();
      }
    });
  }
  async function waitReady(url) {
    for (let i = 0; i < 90; i++) {
      if (stopping) throw new Error("Development startup interrupted.");
      if (await ready(url)) return;
      await delay(500);
    }
    throw new Error(`Service did not become ready: ${url}; see ${directory}`);
  }
  process.once("SIGINT", () => void stop());
  process.once("SIGTERM", () => void stop());
  try {
    console.log(
      "Building backend and preparing the persistent local development environment...",
    );
    await run("go", ["build", "-o", binary, "./cmd/7link-api"]);
    await run(binary, ["migrate", "auto", "--config", configPath]);
    if (!existsSync(initializedPath)) {
      await run(
        binary,
        [
          "iam",
          "bootstrap-platform-admin",
          "--config",
          configPath,
          "--login",
          "admin@example.test",
          "--password-file",
          join(directory, "admin-password"),
        ],
        root,
        {},
        [0, 3],
      );
      writeFileSync(initializedPath, "initialized\n", { mode: 0o600 });
    }
    launch(
      binary,
      [
        "serve",
        "--config",
        configPath,
        "--debug",
        "--pprof.enabled",
        "--api.metrics.enabled",
        "--api.metrics.advertise",
        "--api.migration-status.enabled",
        "--api.migration-status.advertise",
        "--api.config-inspector.enabled",
        "--api.config-inspector.advertise",
        "--api.datasource-inspector.enabled",
        "--api.datasource-inspector.advertise",
      ],
      root,
      "backend",
    );
    await waitReady(settings.apiURL + "/readyz");
    const fixtureEnv = {
      IAM_API_URL: settings.apiURL,
      IAM_CONSOLE_URL: settings.webURL,
      IAM_BROWSER_PASSWORD_FILE: join(directory, "admin-password"),
      IAM_BROWSER_LOGIN: "admin@example.test",
      IAM_BROWSER_TENANT_SLUG: "development",
      IAM_BROWSER_TENANT_NAME: "Development",
      IAM_BACKEND_DIR: root,
      IAM_BOOTSTRAP_SKIP: "true",
      IAM_CAPTCHA_ANSWER: devCaptchaAnswer,
    };
    const tenantID = await run(
      "bun",
      ["scripts/bootstrap-audit-environment.mjs"],
      frontend,
      fixtureEnv,
    );
    const fixture = demoAccounts
      ? JSON.parse(
          await run("bun", ["scripts/bootstrap-file-personas.mjs"], frontend, {
            ...fixtureEnv,
            IAM_BROWSER_TENANT: tenantID,
            IAM_UAT_DIRECTORY: directory,
          }),
        )
      : { accounts: [] };
    const accounts = fixture.accounts;
    if (demoAccounts)
      writeFileSync(
        join(directory, "demo-accounts.json"),
        JSON.stringify(fixture, null, 2) + "\n",
        { mode: 0o600 },
      );
    launch(
      "bun",
      [
        "run",
        "dev",
        "--host",
        "127.0.0.1",
        "--port",
        String(webPort),
        "--strictPort",
      ],
      frontend,
      "frontend",
      {
        VITE_API_PROXY_TARGET: settings.apiURL,
        VITE_DEFAULT_TENANT: tenantID,
        VITE_DEV_LOGIN_ACCOUNTS_ENABLED: String(demoAccounts),
        VITE_DEV_LOGIN_CAPTCHA_ANSWER: devCaptchaAnswer,
        VITE_DEV_LOGIN_PREFILL: String(prefill),
        VITE_DEV_LOGIN_ACCOUNTS: JSON.stringify(accounts),
      },
    );
    await waitReady(settings.webURL + "/user/login");
    settings.running = true;
    settings.tenantID = tenantID;
    settings.accountCount = accounts.length;
    writeFileSync(statePath, JSON.stringify(settings, null, 2) + "\n", {
      mode: 0o600,
    });
    print(settings);
    await stopped;
  } catch (error) {
    console.error(error.message);
    process.exitCode = 1;
  } finally {
    await stop();
  }
}
