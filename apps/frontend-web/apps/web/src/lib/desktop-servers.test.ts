import { expect, test } from "bun:test"
import { spawn } from "node:child_process"
import { existsSync, readFileSync } from "node:fs"
import { resolve } from "node:path"
import { fileURLToPath } from "node:url"

test("desktop chooser persists multiple origins and connects Web and API on each selected origin", async () => {
  const launcher = resolve(import.meta.dir, "../../../../../frontend-app/launcher")
  const calls: string[] = []
  const servers = ["first", "second"].map(name => Bun.serve({
    hostname: "127.0.0.1",
    port: 0,
    fetch(request) {
      const path = new URL(request.url).pathname
      if (path === "/api/session") {
        calls.push(name)
        return Response.json({ server: name })
      }
      return new Response('<!doctype html><body><output></output><script>fetch("/api/session").then(r=>r.json()).then(data=>document.querySelector("output").textContent=data.server)</script>', {
        headers: { "Content-Type": "text/html" },
      })
    },
  }))
  const chooser = Bun.serve({
    hostname: "127.0.0.1",
    port: 0,
    fetch(request) {
      const path = new URL(request.url).pathname
      const file = path === "/servers.mjs" ? "servers.mjs" : path === "/style.css" ? "style.css" : "index.html"
      return new Response(readFileSync(resolve(launcher, file)), {
        headers: { "Content-Type": file.endsWith("mjs") ? "text/javascript" : file.endsWith("css") ? "text/css" : "text/html" },
      })
    },
  })
  const candidates = process.platform === "win32" ? [
    resolve(process.env["PROGRAMFILES(X86)"] ?? "C:/Program Files (x86)", "Microsoft/Edge/Application/msedge.exe"),
    resolve(process.env.PROGRAMFILES ?? "C:/Program Files", "Google/Chrome/Application/chrome.exe"),
  ] : []
  const script = `
    const assert = require("node:assert/strict");
    const { chromium } = require(${JSON.stringify(fileURLToPath(import.meta.resolve("playwright")))});
    (async () => {
      const browser = await chromium.launch({headless:true, executablePath:${JSON.stringify(candidates.find(existsSync)) ?? "undefined"}});
      try {
        const page = await browser.newPage({viewport:{width:390,height:844}});
        const errors = [];
        page.on("pageerror", error => errors.push(String(error)));
        const chooser = ${JSON.stringify(chooser.url.origin)};
        const origins = ${JSON.stringify(servers.map((server, index) => index === 0 ? server.url.origin : server.url.origin.replace("127.0.0.1", "localhost")))};
        await page.goto(chooser);
        assert.equal(await page.locator("#empty").isVisible(), true);
        for (const [index, origin] of origins.entries()) {
          await page.getByLabel("服务器地址", {exact:true}).fill(origin);
          await page.getByRole("button", {name:"保存并连接", exact:true}).click();
          await page.waitForURL(origin + "/");
          await page.getByText(index === 0 ? "first" : "second", {exact:true}).waitFor();
          if (index === 0) {
            await page.evaluate(() => {
              localStorage.setItem("server-marker", "first");
              document.cookie = "serverSession=first; Path=/; SameSite=Lax";
            });
          } else {
            assert.equal(await page.evaluate(() => localStorage.getItem("server-marker")), null);
            assert.equal(await page.evaluate(() => document.cookie.includes("serverSession=first")), false);
          }
          await page.goto(chooser);
          assert.equal(await page.locator("#servers a").count(), index + 1);
        }
        await page.reload();
        assert.equal(await page.locator("#servers a").count(), 2);
        await page.locator("#servers a").first().click();
        await page.waitForURL(origins[0] + "/");
        await page.getByText("first", {exact:true}).waitFor();
        assert.equal(await page.evaluate(() => localStorage.getItem("server-marker")), "first");
        assert.equal(await page.evaluate(() => document.cookie.includes("serverSession=first")), true);
        await page.goto(chooser);
        await page.getByLabel("服务器地址", {exact:true}).fill("https://user:secret@example.com");
        await page.getByRole("button", {name:"保存并连接", exact:true}).click();
        assert.equal(new URL(page.url()).origin, chooser);
        assert.ok(await page.getByRole("alert").textContent());
        await page.getByRole("button", {name:"移除 " + origins[0], exact:true}).click();
        await page.reload();
        assert.equal(await page.locator("#servers a").count(), 1);
        assert.equal(await page.locator("#servers a").getAttribute("href"), origins[1]);
        assert.equal(await page.evaluate(() => document.documentElement.scrollWidth <= innerWidth), true);
        await page.evaluate(() => Storage.prototype.setItem = () => {throw new Error("denied")});
        await page.getByRole("button", {name:"移除 " + origins[1], exact:true}).click();
        assert.equal(await page.locator("#servers a").count(), 1);
        assert.ok(await page.getByRole("alert").textContent());
        assert.deepEqual(errors, []);
      } finally { await browser.close(); }
    })().catch(error => {console.error(error); process.exitCode = 1});
  `
  try {
    await new Promise<void>((done, reject) => {
      const child = spawn("node", ["-e", script], { windowsHide: true, stdio: ["ignore", "pipe", "pipe"] })
      let output = ""
      child.stdout.on("data", value => { output += String(value) })
      child.stderr.on("data", value => { output += String(value) })
      child.on("error", reject)
      child.on("exit", code => code === 0 ? done() : reject(new Error(output || `Browser exit ${code}`)))
    })
    expect(calls).toEqual(["first", "second", "first"])
  } finally {
    await chooser.stop(true)
    for (const server of servers) await server.stop(true)
  }
}, 45000)
