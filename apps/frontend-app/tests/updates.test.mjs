import { test, expect } from "bun:test"
import { renderUpdate, startUpdater } from "../launcher/update.mjs"
import { compareVersions, releaseConfig, updateArtifacts, updateManifest, publishFeed } from "../scripts/release-updates.mjs"

const repo = "owner/application", tag = "v1.2.3-rc.4"
const signature = current => Buffer.from(`untrusted comment: test\nsignature\ntrusted comment: timestamp:1\tfile:artifact\tversion:${current}\nglobal-signature`).toString("base64")
const manifest = (current = tag) => updateManifest({ repo, tag: current, created: "2026-01-01T00:00:00Z", signature: () => signature(current.replace(/^v/, "")) })

test("release configuration selects candidate/stable independently of business servers and requires signing", () => {
  const candidate = releaseConfig({ repo, tag, publicKey: "public-verification-key", platform: "macos" })
  expect(candidate.version).toBe("1.2.3-rc.4")
  expect(candidate.bundle).toEqual({ createUpdaterArtifacts: true, macOS: { signingIdentity: "-" } })
  expect(candidate.plugins.updater.requireSignedVersion).toBe(true)
  expect(candidate.plugins.updater.endpoints).toEqual([`https://raw.githubusercontent.com/${repo}/desktop-updates/candidate.json`])
  expect(releaseConfig({ repo, tag: "v1.2.3", publicKey: "key" }).plugins.updater.endpoints[0]).toEndWith("/stable.json")
  expect(() => releaseConfig({ repo, tag, publicKey: "" })).toThrow()
  expect(() => releaseConfig({ repo: "../other", tag, publicKey: "key" })).toThrow()
})

test("version order handles numeric prereleases, final releases and large components without downgrade", () => {
  const ascending = ["1.0.0-alpha", "1.0.0-alpha.1", "1.0.0-alpha.beta", "1.0.0-beta", "1.0.0-beta.2", "1.0.0-beta.11", "1.0.0-rc.1", "1.0.0", "1.0.1", "9007199254740993.0.0", "9007199254740994.0.0"]
  for (let index = 1; index < ascending.length; index++) {
    expect(compareVersions(ascending[index], ascending[index - 1])).toBe(1)
    expect(compareVersions(ascending[index - 1], ascending[index])).toBe(-1)
  }
  expect(compareVersions("v1.2.3-rc.4", "1.2.3-rc.4")).toBe(0)
  for (const invalid of ["1.2", "01.2.3", "1.2.3-rc.01", "1.2.3+build", "v1.2.3/other"]) expect(() => compareVersions(invalid, "1.0.0")).toThrow()
})

test("manifest covers six real installation targets and refuses mismatched or absent version-bound signatures", () => {
  const result = manifest()
  expect(Object.keys(result.platforms).sort()).toEqual(["darwin-aarch64", "darwin-x86_64", "linux-aarch64", "linux-x86_64", "windows-x86_64", "windows-x86_64-portable"])
  expect(result.platforms["windows-x86_64-portable"].url).toEndWith("_portable.zip")
  expect(result.platforms["windows-x86_64"].url).toEndWith("-setup.exe")
  expect(result.platforms["darwin-aarch64"].url).toEndWith("_arm64.app.tar.gz")
  expect(result.platforms["linux-x86_64"].url).toEndWith("_amd64.AppImage")
  expect(new Set(Object.values(updateArtifacts(repo, tag))).size).toBe(6)
  for (const value of ["", signature("1.2.3-rc.3"), Buffer.from("trusted comment: timestamp:1\tfile:artifact").toString("base64")]) {
    expect(() => updateManifest({ repo, tag, created: result.pub_date, signature: () => value })).toThrow()
  }
})

function github(initial = {}) {
  const files = new Map(Object.entries(initial).map(([name, value]) => [name, { sha: "old-"+name, content: Buffer.from(JSON.stringify(value)).toString("base64") }]))
  const writes = []
  let conflict = false
  const request = async (url, options) => {
    const path = new URL(url).pathname.split(`/repos/${repo}/`)[1]
    if (path === "git/ref/heads/desktop-updates") return Response.json({ ref: "refs/heads/desktop-updates" })
    const name = path.replace(/^contents\//, ""), body = options.body && JSON.parse(options.body)
    if (options.method === "GET") return files.has(name) ? Response.json(files.get(name)) : Response.json({}, { status: 404 })
    if (conflict) { conflict = false; return Response.json({}, { status: 409 }) }
    writes.push({ name, body }); files.set(name, { sha: "new-"+name, content: body.content })
    return Response.json({ content: { sha: "new-"+name } })
  }
  return { request, writes, files, setConflict: () => { conflict = true } }
}

test("candidate never reaches stable; stable advances both; retries conflicts and never regresses a channel", async () => {
  const candidate = github()
  await publishFeed({ repo, manifest: manifest(), token: "test-token", request: candidate.request })
  expect(candidate.writes.map(item => item.name)).toEqual(["candidate.json"])
  const stable = github({ "candidate.json": manifest("v1.3.0-rc.1") })
  stable.setConflict()
  await publishFeed({ repo, manifest: manifest("v1.2.3"), token: "test-token", request: stable.request })
  expect(stable.writes.map(item => item.name)).toEqual(["stable.json"])
  const promotion = github()
  await publishFeed({ repo, manifest: manifest("v1.2.3"), token: "test-token", request: promotion.request })
  expect(promotion.writes.map(item => item.name)).toEqual(["stable.json", "candidate.json"])
  expect(promotion.writes.every(item => item.body.branch === "desktop-updates")).toBe(true)
  await publishFeed({ repo, manifest: manifest("v1.2.2"), token: "test-token", request: promotion.request })
  expect(promotion.writes).toHaveLength(2)
})

function document() {
  const nodes = new Map()
  return { querySelector: selector => {
    if (!nodes.has(selector)) nodes.set(selector, { textContent: "", hidden: false, disabled: false, listeners: {}, addEventListener(event, action) { this.listeners[event] = action }, removeAttribute(name) { delete this[name] } })
    return nodes.get(selector)
  } }
}

test("update UI shows explicit progress/failure and renders notes as text; installation requires a click", async () => {
  const doc = document(), calls = []
  const stop = startUpdater(doc, async command => { calls.push(command); return { phase: "available", current: "1.0.0", version: "1.1.0", notes: "<script>untrusted notes</script>" } })
  await Promise.resolve(); await Promise.resolve()
  expect(calls).toEqual(["update_status"])
  expect(doc.querySelector("#install-update").hidden).toBe(false)
  expect(doc.querySelector("#notes").textContent).toBe("<script>untrusted notes</script>")
  doc.querySelector("#install-update").listeners.click()
  await Promise.resolve(); await Promise.resolve()
  expect(calls.filter(command => command === "install_update")).toHaveLength(1)
  renderUpdate(doc, { phase: "downloading", downloaded: 50, total: 100 })
  expect(doc.querySelector("#install-update").disabled).toBe(true)
  expect(doc.querySelector("#check-update").disabled).toBe(true)
  expect(doc.querySelector("#update-progress").value).toBe(50)
  renderUpdate(doc, { phase: "error", error: "Signature rejected" })
  expect(doc.querySelector("#update-error").textContent).toBe("Signature rejected")
  expect(doc.querySelector("#install-update").hidden).toBe(true)
  expect(doc.querySelector("#check-update").disabled).toBe(false)
  stop()
})
