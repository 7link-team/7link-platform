import { describe, expect, test } from "bun:test"
import { lastConnection, rememberConnection, resumeConnection, savedServers, serverOrigin } from "../launcher/servers.mjs"

describe("runtime deployment origins", () => {
  test("accepts independent domains and local nginx ports without an API endpoint", () => {
    expect(serverOrigin(" https://EXAMPLE.com:443/ ")).toBe("https://example.com")
    expect(serverOrigin("http://127.0.0.1:8080")).toBe("http://127.0.0.1:8080")
    expect(serverOrigin("https://other.example:8443")).toBe("https://other.example:8443")
  })

  test("rejects executable schemes, credentials and API/deployment subpaths", () => {
    for (const value of ["", "example.com", "//example.com", "javascript:alert(1)",
      "file:///tmp/index.html", "https://user:password@example.com", "https://example.com/api",
      "https://example.com?token=secret", "https://example.com/#fragment"]) {
      expect(() => serverOrigin(value)).toThrow()
    }
  })

  test("loads multiple saved origins, normalizes duplicates and retains ports", () => {
    const storage = { getItem: () => JSON.stringify([
      "https://example.com", "https://EXAMPLE.com/", "http://localhost:8080", "https://other.example",
    ]) }
    expect(savedServers(storage)).toEqual([
      "https://example.com", "http://localhost:8080", "https://other.example",
    ])
    expect(savedServers({ getItem: () => null })).toEqual([])
  })

  test("never turns malformed or tampered saved data into navigation links", () => {
    for (const value of ["{", "null", "{}", '[123]', '["javascript:alert(1)"]',
      '["https://user:secret@example.com"]']) {
      expect(() => savedServers({ getItem: () => value })).toThrow()
    }
    expect(() => savedServers({ getItem: () => { throw new Error("denied") } })).toThrow()
  })
})

describe("restart restores the last chosen connection", () => {
  function storage() {
    const values = new Map()
    return { getItem: key => values.get(key) ?? null, setItem: (key, value) => values.set(key, value) }
  }

  test("first run stays in the chooser; local mode starts the service after a restart", () => {
    const saved = storage()
    let calls = 0
    const location = { hash: "", replace() { throw new Error("unexpected remote navigation") } }
    resumeConnection(saved, location, () => { calls++ })
    expect(calls).toBe(0)
    rememberConnection(saved, { mode: "local" })
    resumeConnection(saved, location, () => { calls++ })
    expect(calls).toBe(1)
    expect(lastConnection(saved)).toEqual({ mode: "local" })
  })

  test("remote mode restores the exact saved deployment; explicit chooser never reconnects", () => {
    const saved = storage()
    saved.setItem("desktop-server-origins", JSON.stringify(["https://one.example", "https://two.example:8443"]))
    rememberConnection(saved, { mode: "remote", origin: "https://two.example:8443/" })
    const destinations = []
    const location = { hash: "#choose", replace: origin => destinations.push(origin) }
    const unexpected = () => { throw new Error("unexpected local startup") }
    resumeConnection(saved, location, unexpected)
    expect(destinations).toEqual([])
    location.hash = ""
    resumeConnection(saved, location, unexpected)
    expect(destinations).toEqual(["https://two.example:8443"])
  })

  test("invalid or removed saved origins never cause automatic navigation", () => {
    const saved = storage()
    for (const value of ["{", "[]", "123", '{"mode":"remote","origin":"javascript:alert(1)"}', '{"mode":"remote","origin":"https://removed.example"}']) {
      saved.setItem("desktop-last-connection", value)
      expect(() => resumeConnection(saved, { hash: "", replace() { throw new Error("unsafe navigation") } }, () => {})).toThrow()
    }
  })

  test("only first run and an explicit switch reveal the chooser during startup", async () => {
    const saved = storage()
    let reveals = 0
    const show = () => { reveals++ }
    const location = { hash: "", replace() {} }
    const start = async () => {}
    resumeConnection(saved, location, start, show)
    expect(reveals).toBe(1)
    rememberConnection(saved, { mode: "local" })
    await resumeConnection(saved, location, start, show)
    expect(reveals).toBe(1)
    saved.setItem("desktop-server-origins", '["https://saved.example"]')
    rememberConnection(saved, { mode: "remote", origin: "https://saved.example" })
    resumeConnection(saved, location, start, show)
    expect(reveals).toBe(1)
    location.hash = "#choose"
    resumeConnection(saved, location, start, show)
    expect(reveals).toBe(2)
  })
})
