import { describe, expect, it } from "bun:test"
import { readFileSync } from "node:fs"
import { renderToStaticMarkup } from "react-dom/server"
import { MemoryRouter, Outlet, Route, Routes } from "react-router"
import { IntlAppProvider } from "@workspace/ui/i18n/intl-provider"
import ui from "../../../../../packages/ui/src/i18n/messages/en-US.json"
import en from "../../messages/en-US.json"
import { SpawnRequests } from "./spawn-requests"

function render(permissions: string[]) {
  return renderToStaticMarkup(<IntlAppProvider initialLocale="en-US" initialMessages={{ ...ui, iam: en }}><MemoryRouter><Routes><Route element={<Outlet context={{ permissions: new Set(permissions), permissionsResolved: true }} />}><Route path="/" element={<SpawnRequests tenant="1" />} /></Route></Routes></MemoryRouter></IntlAppProvider>)
}

describe("Agent request review", () => {
  it("shows the current management boundary and explicit source limitation", () => {
    expect(render([])).not.toContain(en.spawnRequestsTitle)
    const html = render(["agent_manage"])
    expect(html).toContain(en.spawnRequestsTitle)
    expect(html).toContain(en.spawnRequestsDescription)
    expect(html).toContain('role="status"')
    expect(html).not.toContain(en.spawnRequestAction_ADOPT)
  })
  it("provides every state and action in all three locales", () => {
    const keys = Object.keys(en).filter(key => key.startsWith("spawnRequest"))
    expect(keys.length).toBe(15)
    for (const locale of ["en-US", "zh-CN", "ms-MY"]) {
      const messages = JSON.parse(readFileSync(new URL(`../../messages/${locale}.json`, import.meta.url), "utf8"))
      for (const key of keys) {
        expect(typeof messages[key]).toBe("string")
        expect(messages[key]).not.toContain("??")
      }
    }
  })
})
