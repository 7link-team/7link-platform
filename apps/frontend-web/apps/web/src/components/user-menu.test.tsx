import { describe, expect, it } from "bun:test"
import { renderToStaticMarkup } from "react-dom/server"
import { MemoryRouter } from "react-router"
import { IntlAppProvider } from "@workspace/ui/i18n/intl-provider"
import { AuthContext, type AuthContextValue } from "./auth"
import { UserMenu } from "./user-menu"
import type { Session } from "../lib/iam-api"
import en from "../messages/en-US.json"

function render(session: Session | null, tenantID = "") {
  return renderToStaticMarkup(
    <IntlAppProvider initialLocale="en-US" initialMessages={{ iam: en }}>
      <MemoryRouter>
        <AuthContext.Provider value={{ session } as AuthContextValue}>
          <UserMenu tenantID={tenantID} />
        </AuthContext.Provider>
      </MemoryRouter>
    </IntlAppProvider>
  )
}

describe("account menu identity", () => {
  const session: Session = {
    subject: "9007199254740993",
    issuer: "test",
    acr: 1,
  }

  it("never uses the internal subject as a display name while self profile loads", () => {
    for (const tenant of ["", "123"]) {
      const html = render(session, tenant)
      expect(html).toContain(en.accountMenu)
      expect(html).not.toContain(session.subject)
    }
  })

  it("keeps available account names and email readable even with a selected tenant", () => {
    for (const tenant of ["", "123"]) {
      expect(
        render({ ...session, preferred_username: " Alice " }, tenant)
      ).toContain(">Alice</span>")
      expect(
        render(
          {
            ...session,
            preferred_username: " ",
            email: " alice@example.test ",
          },
          tenant
        )
      ).toContain(">alice@example.test</span>")
    }
  })

  it("does not render an account menu after logout", () => {
    expect(render(null)).toBe("")
  })
})

it("loads self profile and rejects stale account responses in a browser", async () => {
  const { fileURLToPath } = await import("node:url")
  const { dirname, resolve } = await import("node:path")
  const { existsSync, readFileSync } = await import("node:fs")
  const { spawn } = await import("node:child_process")
  const absolute = (path: string) =>
    JSON.stringify(fileURLToPath(new URL(path, import.meta.url)))
  const built = await Bun.build({
    entrypoints: ["account-menu-fixture"],
    target: "browser",
    define: { "import.meta.env": "{}" },
    plugins: [
      {
        name: "account-menu-fixture",
        setup(build) {
          build.onLoad(
            { filter: /\.[cm]?js$/, namespace: "file" },
            ({ path }) => ({
              contents: readFileSync(path, "utf8"),
              loader: "js",
              resolveDir: dirname(path),
            })
          )
          build.onResolve({ filter: /^account-menu-fixture$/ }, () => ({
            path: "entry",
            namespace: "account-menu-fixture",
          }))
          build.onLoad(
            { filter: /.*/, namespace: "account-menu-fixture" },
            () => ({
              loader: "tsx",
              resolveDir: import.meta.dir,
              contents: `
        import React from 'react'; import {createRoot} from 'react-dom/client';
        import {MemoryRouter} from 'react-router';
        import {IntlAppProvider} from '@workspace/ui/i18n/intl-provider';
        import {UserMenu} from ${absolute("./user-menu.tsx")};
        import {AuthContext} from ${absolute("./auth.ts")};
        import {selfProfileApi} from ${absolute("../app/security/profile-api.ts")};
        import {iamApi} from ${absolute("../lib/iam-api.ts")};
        import {activateAccountView} from ${absolute("../lib/account-view.ts")};
        import en from ${absolute("../messages/en-US.json")};
        window.profileRequests=[];
        window.activatePersonalView=()=>activateAccountView({scope_type:'personal',scope_id:'9007199254740996',mode:'read_only'}, {id:'71',token:'avt_test-view',mode:'read_only',expires_at:'2099-01-01T00:00:00Z'}, '/system/accounts','');
        iamApi.myAvatar=async()=>{throw new Error('No avatar')};
        selfProfileApi.get=(subject,signal)=>new Promise((resolve,reject)=>window.profileRequests.push({subject,signal,resolve,reject}));
        const root=createRoot(document.getElementById('root'));
        window.renderAccount=(subject,revision=0)=>root.render(<IntlAppProvider initialLocale="en-US" initialMessages={{iam:en}}><MemoryRouter><AuthContext.Provider value={{session:subject?{subject,issuer:'test',acr:1,revision}:null}}><UserMenu tenantID="123"/></AuthContext.Provider></MemoryRouter></IntlAppProvider>);
        window.renderAccount('9007199254740993');
      `,
            })
          )
        },
      },
    ],
  })
  expect(built.success).toBe(true)
  if (!built.success) throw new Error(built.logs.map(String).join("\n"))
  const script = await built.outputs[0]!.text()
  const server = Bun.serve({
    hostname: "127.0.0.1",
    port: 0,
    fetch: (request) =>
      new URL(request.url).pathname === "/fixture.js"
        ? new Response(script, {
            headers: { "content-type": "text/javascript" },
          })
        : new Response(
            '<html><body><div id="root"></div><script type="module" src="/fixture.js"></script></body></html>',
            { headers: { "content-type": "text/html" } }
          ),
  })
  const candidates =
    process.platform === "win32"
      ? [
          resolve(
            process.env["PROGRAMFILES(X86)"] ?? "C:/Program Files (x86)",
            "Microsoft/Edge/Application/msedge.exe"
          ),
          resolve(
            process.env.PROGRAMFILES ?? "C:/Program Files",
            "Google/Chrome/Application/chrome.exe"
          ),
        ]
      : []
  const check = `const assert=require('node:assert/strict');const {chromium}=require(${JSON.stringify(fileURLToPath(import.meta.resolve("playwright")))});
    (async()=>{const browser=await chromium.launch({headless:true,executablePath:${JSON.stringify(candidates.find(existsSync)) ?? "undefined"}});try{
      const page=await browser.newPage();page.on('pageerror',error=>console.error(error));await page.goto(${JSON.stringify(server.url.href)});
      const menu=page.getByRole('button',{name:'Account menu',exact:true});
      await page.waitForFunction(()=>window.profileRequests?.length===1,{},{timeout:5000});
      assert(!(await menu.textContent()).includes('9007199254740993'));
      await page.evaluate(()=>window.profileRequests[0].resolve({id:'9007199254740993',display_name:'Alice Example',email:'alice@example.test'}));
      await menu.filter({hasText:'Alice Example'}).waitFor();
      await page.evaluate(()=>window.renderAccount('9007199254740994'));
      await page.waitForFunction(()=>window.profileRequests.length===2);
      assert(!(await menu.textContent()).includes('Alice Example'));
      assert(await page.evaluate(()=>window.profileRequests[0].signal.aborted));
      await page.evaluate(()=>window.renderAccount('9007199254740995'));
      await page.waitForFunction(()=>window.profileRequests.length===3);
      await page.evaluate(()=>window.profileRequests[1].resolve({id:'9007199254740994',display_name:'Stale Account'}));
      await page.evaluate(()=>new Promise(resolve=>requestAnimationFrame(()=>requestAnimationFrame(resolve))));
      assert(!(await menu.textContent()).includes('Stale Account'));
      await page.evaluate(()=>window.profileRequests[2].resolve({id:'9007199254740995',display_name:'Current Account'}));
      await menu.filter({hasText:'Current Account'}).waitFor();
      assert(!(await menu.textContent()).includes('Stale Account'));
      await page.evaluate(()=>window.renderAccount('9007199254740995',1));
      await page.waitForFunction(()=>window.profileRequests.length===4);
      await page.evaluate(()=>window.profileRequests[3].resolve({id:'9007199254740995',display_name:'Edited Account'}));
      await menu.filter({hasText:'Edited Account'}).waitFor();
      await page.evaluate(()=>window.renderAccount(null));await menu.waitFor({state:'hidden'});
      await page.evaluate(()=>window.renderAccount('9007199254740996'));
      await page.waitForFunction(()=>window.profileRequests.length===5);
      await page.evaluate(()=>window.profileRequests[4].reject(new Error('Unavailable')));
      assert.equal((await menu.textContent()).trim(),'Account menu');
      await page.evaluate(()=>window.renderAccount('9007199254740996',1));
      await page.waitForFunction(()=>window.profileRequests.length===6);
      await page.evaluate(()=>window.profileRequests[5].resolve({id:'9007199254740996',display_name:'Private Self Profile'}));
      await menu.filter({hasText:'Private Self Profile'}).waitFor();
      await page.evaluate(()=>{window.activatePersonalView();window.renderAccount('9007199254740996',2)});
      await menu.waitFor();
      await page.evaluate(()=>new Promise(resolve=>requestAnimationFrame(()=>requestAnimationFrame(resolve))));
      assert.equal(await page.evaluate(()=>window.profileRequests.length),6,'personal view must not call cookie-only self profile');
      assert.equal((await menu.textContent()).trim(),'Account menu','personal view must not display cached private profile');
    }finally{await browser.close();}})().catch(e=>{console.error(e);process.exitCode=1});`
  try {
    await new Promise<void>((done, reject) => {
      const child = spawn("node", ["-e", check], {
        windowsHide: true,
        stdio: ["ignore", "pipe", "pipe"],
      })
      let output = ""
      child.stdout.on("data", (chunk) => {
        output += String(chunk)
      })
      child.stderr.on("data", (chunk) => {
        output += String(chunk)
      })
      child.on("error", reject)
      child.on("exit", (code) =>
        code === 0
          ? done()
          : reject(new Error(output || `Browser exit ${code}`))
      )
    })
  } finally {
    await server.stop(true)
  }
}, 60_000)
