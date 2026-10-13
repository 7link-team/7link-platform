import { describe, expect, it } from "bun:test"
import { readFileSync } from "node:fs"

// Map rendering uses Leaflet's own API (BSD-2). react-leaflet and its core are
// Hippocratic-2.1, which is not an open-source licence this package can ship,
// and this file was their only use. Reintroducing either fails here.
describe("leaflet map dependencies", () => {
  const read = (path: string) =>
    readFileSync(new URL(path, import.meta.url), "utf8")
  it("renders the map without react-leaflet", () => {
    expect(read("./_leaflet-map.tsx")).not.toMatch(/["']@?react-leaflet/)
    const manifest = JSON.parse(read("../../../package.json")) as {
      dependencies?: Record<string, string>
      devDependencies?: Record<string, string>
    }
    for (const name of ["react-leaflet", "@react-leaflet/core"]) {
      expect(manifest.dependencies ?? {}).not.toHaveProperty(name)
      expect(manifest.devDependencies ?? {}).not.toHaveProperty(name)
    }
    expect(manifest.dependencies).toHaveProperty("leaflet")
  })
})
