import { expect, it } from "bun:test"
import { mkdtempSync, writeFileSync, rmSync } from "node:fs"
import { tmpdir } from "node:os"
import { join } from "node:path"
import config from "./vite.config"
import type { ProxyOptions } from "vite"

it("loads the documented proxy target from the mode env file and preserves process overrides", async () => {
  const directory = mkdtempSync(join(tmpdir(), "workspace-proxy-"))
  const cwd = process.cwd()
  const previous = process.env.VITE_API_PROXY_TARGET
  try {
    process.chdir(directory)
    delete process.env.VITE_API_PROXY_TARGET
    writeFileSync(join(directory, ".env.regression"), "VITE_API_PROXY_TARGET=http://127.0.0.1:54329\n")
    if (typeof config !== "function") throw new Error("expected mode-dependent config")
    const fromFile = await config({ command: "serve", mode: "regression" })
    const proxy = fromFile.server?.proxy?.["/api"] as ProxyOptions
    expect(proxy.target).toBe("http://127.0.0.1:54329")
    expect(proxy.rewrite?.("/api/deployment")).toBe("/deployment")
    expect(proxy.rewrite?.("/api/me?check=1")).toBe("/me?check=1")
    process.env.VITE_API_PROXY_TARGET = "http://127.0.0.1:54331"
    const overridden = await config({ command: "serve", mode: "regression" })
    expect((overridden.server?.proxy?.["/api"] as ProxyOptions).target).toBe("http://127.0.0.1:54331")
  } finally {
    process.chdir(cwd)
    if (previous === undefined) delete process.env.VITE_API_PROXY_TARGET
    else process.env.VITE_API_PROXY_TARGET = previous
    rmSync(directory, { recursive: true })
  }
})
