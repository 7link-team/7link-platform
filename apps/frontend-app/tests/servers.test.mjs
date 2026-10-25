import { describe, expect, test } from "bun:test"
import { savedServers, serverOrigin } from "../launcher/servers.mjs"

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
