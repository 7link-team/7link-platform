import { afterAll, beforeAll, describe, it } from "bun:test"
import { existsSync, readFileSync } from "node:fs"
import { spawn } from "node:child_process"
import { fileURLToPath } from "node:url"
import { resolve, dirname } from "node:path"
import en from "../../messages/en-US.json"

// SSR cannot exercise loaded detail or decision handlers. Render the real
// component with only its API transport and realtime connection mocked.
let server: ReturnType<typeof Bun.serve>
const absolute = (path: string) => JSON.stringify(resolve(import.meta.dir, path).replaceAll("\\", "/"))

beforeAll(async () => {
  const built = await Bun.build({
    entrypoints: ["approval-test-fixture"], target: "browser",
    plugins: [{ name: "approval-fixture", setup(build) {
    // Read dependencies explicitly across repeated Windows browser fixture builds.
    build.onLoad({filter: /\.[cm]?js$/, namespace: "file"}, ({path}) => ({contents: readFileSync(path, "utf8"), loader: "js", resolveDir: dirname(path)}))
      build.onResolve({ filter: /^approval-test-fixture$/ }, () => ({ path: "entry", namespace: "fixture" }))
      build.onResolve({ filter: /[/]iam-api$/ }, () => ({ path: "api", namespace: "fixture" }))
      build.onResolve({ filter: /[/]use-realtime$/ }, () => ({ path: "realtime", namespace: "fixture" }))
      build.onLoad({ filter: /.*/, namespace: "fixture" }, ({ path }) => {
        if (path === "realtime") return { contents: "export function useRealtime() {}", loader: "js" }
        if (path === "api") return { loader: "js", contents: `
          export class ApiError extends Error {}
          export const businessCode = {captchaInvalid: 1};
          export const iamApi = {
            deliveryResultRequest: async (route) => route.url.endsWith("/merges") ? [] : ({items:[],has_more:false}),
            execution: async () => structuredClone(window.__approvalFixture.detail),
            decideExecutionPermission: async (...args) => {
              const fixture = window.__approvalFixture;
              fixture.calls.push(args);
              if (fixture.fail) throw new Error("Decision was rejected by the server");
              if (fixture.delay) await new Promise(resolve => { fixture.finish = resolve });
              fixture.detail.approvals = [];
            }
          };
        ` }
        return { loader: "tsx", resolveDir: import.meta.dir, contents: `
          import React from "react";
          import {createRoot} from "react-dom/client";
          import {MemoryRouter,Routes,Route,Outlet} from "react-router";
          import {IntlAppProvider} from "@workspace/ui/i18n/intl-provider";
          import ui from ${absolute("../../../../../packages/ui/src/i18n/messages/en-US.json")};
          import en from ${absolute("../../messages/en-US.json")};
          import Detail from ${absolute("./detail.tsx")};
          function Shell() { return <Outlet context={{permissions:new Set(window.__approvalFixture.permissions),permissionsResolved:true}}/> }
          createRoot(document.getElementById("root")).render(
            <IntlAppProvider initialLocale="en-US" initialMessages={{...ui,iam:en}}>
              <MemoryRouter initialEntries={["/admin/1/entities/3/executions/12"]}>
                <Routes><Route element={<Shell/>}><Route path="/admin/:tenantID/entities/:entityID/executions/:executionID" element={<Detail/>}/></Route></Routes>
              </MemoryRouter>
            </IntlAppProvider>);
        ` }
      })
    } }],
  })
  if (!built.success) throw new Error(built.logs.map(String).join("\n"))
  const script = await built.outputs[0]!.text()
  server = Bun.serve({ hostname: "127.0.0.1", port: 0, fetch: (request) => new URL(request.url).pathname === "/fixture.js"
    ? new Response(script, { headers: { "content-type": "text/javascript" } })
    : new Response('<!doctype html><html><body><div id="root"></div><script type="module" src="/fixture.js"></script></body></html>', { headers: { "content-type": "text/html" } }),
  })
}, 60_000)

afterAll(async () => { await server?.stop(true) })

const candidates = process.platform === "win32" ? [
  resolve(process.env["PROGRAMFILES(X86)"] ?? "C:/Program Files (x86)", "Microsoft/Edge/Application/msedge.exe"),
  resolve(process.env.PROGRAMFILES ?? "C:/Program Files", "Google/Chrome/Application/chrome.exe"),
] : []
const labels = { reason: en.executionPermissionReason, input: en.executionPermissionInput, allow: en.executionPermissionAllow, deny: en.executionPermissionDeny, saved: en.executionPermissionSaved }

// Bun's Windows WebSocket/pipe transport cannot drive Playwright reliably.
// Native Node runs the installed Playwright; Bun still owns assertions/results.
async function checkInBrowser(check: string, options: { permissions?: string[]; fail?: boolean; delay?: boolean } = {}) {
  const script = `
    const assert = require("node:assert/strict");
    const {chromium} = require(${JSON.stringify(fileURLToPath(import.meta.resolve("playwright")))});
    const labels = ${JSON.stringify(labels)};
    (async () => {
      const browser = await chromium.launch({headless:true,executablePath:${JSON.stringify(candidates.find(existsSync)) ?? "undefined"}});
      try {
        const page = await browser.newPage();
        await page.addInitScript(configuration => Object.assign(window,{__approvalFixture:{
          ...configuration,calls:[],detail:{
            execution:{id:"12",project_id:"3",work_item_id:"5",machine_id:"8",version:2,state:"RUNNING",result_state:"PENDING",dispatch_epoch:1,
              plan_hash:"a".repeat(64),plan:{runtime:"CODEX",model:"test-model",workdir:"/work",base_commit:"b".repeat(40),objective:"Verify approval",authorization_mode:"ON_REQUEST"}},
            logs:[],evidence:[],approvals:[{id:"17",tool:"shell",input:{command:"git status",path:"<untrusted>&path"}}]
          }
        }}),${JSON.stringify({ permissions: options.permissions ?? ["interaction_respond"], fail: options.fail ?? false, delay: options.delay ?? false })});
        await page.goto(${JSON.stringify(server.url.href)});
        await page.locator('a[href$="/workspace/12"]').waitFor();
        const reason = page.getByRole("textbox",{name:labels.reason});
        const allow = page.getByRole("button",{name:labels.allow,exact:true});
        const deny = page.getByRole("button",{name:labels.deny,exact:true});
        const calls = () => page.evaluate(() => window.__approvalFixture.calls);
        ${check}
      } finally { await browser.close(); }
    })().catch(error => { console.error(error); process.exitCode=1 });
  `
  await new Promise<void>((done, reject) => {
    const child = spawn("node", ["-e", script], { windowsHide: true, stdio: ["ignore", "pipe", "pipe"] })
    let output = ""
    child.stdout.on("data", (chunk) => { output += String(chunk) })
    child.stderr.on("data", (chunk) => { output += String(chunk) })
    child.on("error", reject)
    child.on("exit", (code) => code === 0 ? done() : reject(new Error(output || `Browser assertion process exited ${code}`)))
  })
}

describe("execution permission decisions", () => {
  it("requires interaction_respond to expose approval controls or input", async () => {
    await checkInBrowser(`
      assert.equal(await reason.count(),0);
      assert.equal(await page.getByLabel(labels.input).count(),0);
      assert.equal(await allow.count(),0);
      assert.deepEqual(await calls(),[]);
    `, { permissions: ["execution_stop"] })
  }, 30_000)

  it("renders exact tool input as text and rejects empty or blank reasons", async () => {
    await checkInBrowser(`
      assert.equal(await page.getByLabel(labels.input).textContent(),JSON.stringify({command:"git status",path:"<untrusted>&path"},null,2));
      assert.equal(await page.locator("untrusted").count(),0);
      for (const value of ["","   "]) {
        await reason.fill(value);
        assert.equal(await allow.isDisabled(),true);
        assert.equal(await deny.isDisabled(),true);
      }
      assert.deepEqual(await calls(),[]);
    `)
  }, 30_000)

  for (const decision of ["ALLOW", "DENY"] as const) {
    it(`sends the exact ${decision} request once and disables controls while saving`, async () => {
      await checkInBrowser(`
        await reason.fill("Limit the operation to the inspected directory");
        await ${decision === "ALLOW" ? "allow" : "deny"}.click();
        await page.waitForFunction(() => Boolean(window.__approvalFixture.finish));
        assert.deepEqual(await calls(),[["12","17","${decision}","Limit the operation to the inspected directory"]]);
        assert.equal(await reason.isDisabled(),true);
        assert.equal(await allow.isDisabled(),true);
        assert.equal(await deny.isDisabled(),true);
        await page.evaluate(() => window.__approvalFixture.finish());
        await page.getByText(labels.saved,{exact:true}).waitFor();
        assert.equal(await reason.count(),0);
      `, { delay: true })
    }, 30_000)
  }

  it("preserves the reason and exposes failure without claiming success", async () => {
    await checkInBrowser(`
      await reason.fill("Deny access to the requested directory");
      await deny.click();
      await page.getByText("Decision was rejected by the server",{exact:true}).waitFor();
      assert.equal(await reason.inputValue(),"Deny access to the requested directory");
      assert.equal(await page.getByText(labels.saved,{exact:true}).count(),0);
      assert.equal(await deny.isEnabled(),true);
      assert.deepEqual(await calls(),[["12","17","DENY","Deny access to the requested directory"]]);
    `, { fail: true })
  }, 30_000)
})
