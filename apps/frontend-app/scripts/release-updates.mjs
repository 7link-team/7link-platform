import { readFileSync, writeFileSync } from "node:fs"
import { join, resolve } from "node:path"
import { pathToFileURL } from "node:url"

export function version(value) {
  const match = /^v?(0|[1-9]\d*)\.(0|[1-9]\d*)\.(0|[1-9]\d*)(?:-([0-9A-Za-z-]+(?:\.[0-9A-Za-z-]+)*))?$/.exec(value)
  if (!match || match[4]?.split(".").some(part => /^\d+$/.test(part) && part.length > 1 && part[0] === "0")) throw new Error("Invalid release version")
  return { text: value.replace(/^v/, ""), core: match.slice(1, 4).map(BigInt), pre: match[4]?.split(".") || [] }
}

export function compareVersions(a, b) {
  const left = version(a), right = version(b)
  for (let index = 0; index < 3; index++) if (left.core[index] !== right.core[index]) return left.core[index] > right.core[index] ? 1 : -1
  if (!left.pre.length || !right.pre.length) return left.pre.length === right.pre.length ? 0 : left.pre.length ? -1 : 1
  for (let index = 0; index < Math.max(left.pre.length, right.pre.length); index++) {
    const x = left.pre[index], y = right.pre[index]
    if (x === y) continue
    if (x === undefined || y === undefined) return x === undefined ? -1 : 1
    const numericX = /^\d+$/.test(x), numericY = /^\d+$/.test(y)
    if (numericX && numericY) return BigInt(x) > BigInt(y) ? 1 : -1
    if (numericX !== numericY) return numericX ? -1 : 1
    return x > y ? 1 : -1
  }
  return 0
}

function repository(value) {
  if (!/^[A-Za-z0-9][A-Za-z0-9_.-]*\/[A-Za-z0-9][A-Za-z0-9_.-]*$/.test(value)) throw new Error("Invalid repository")
  return value
}

export function releaseConfig({ tag, repo, publicKey, platform }) {
  const parsed = version(tag)
  if (!publicKey?.trim()) throw new Error("DESKTOP_UPDATE_PUBLIC_KEY must be configured before releasing")
  const bundle = { createUpdaterArtifacts: true }
  if (platform === "macos") bundle.macOS = { signingIdentity: "-" }
  return {
    version: parsed.text, bundle,
    plugins: { updater: {
      pubkey: publicKey.trim(), requireSignedVersion: true,
      endpoints: [`https://raw.githubusercontent.com/${repository(repo)}/desktop-updates/${parsed.pre.length ? "candidate" : "stable"}.json`],
      windows: { installMode: "passive" },
    } },
  }
}

export function updateArtifacts(repo, tag) {
  repository(repo); version(tag)
  const prefix = `${repo.split("/")[1]}-desktop_${tag}_`
  return {
    "windows-x86_64": `${prefix}windows_amd64-setup.exe`,
    "windows-x86_64-portable": `${prefix}windows_amd64_portable.zip`,
    "darwin-x86_64": `${prefix}macos_amd64.app.tar.gz`,
    "darwin-aarch64": `${prefix}macos_arm64.app.tar.gz`,
    "linux-x86_64": `${prefix}linux_amd64.AppImage`,
    "linux-aarch64": `${prefix}linux_arm64.AppImage`,
  }
}

export function updateManifest({ repo, tag, created, notes = "", signature }) {
  const current = version(tag).text
  if (!created || !Number.isFinite(Date.parse(created))) throw new Error("Release creation date is required")
  const platforms = Object.fromEntries(Object.entries(updateArtifacts(repo, tag)).map(([target, name]) => {
    const signed = signature(name).trim()
    const decoded = Buffer.from(signed, "base64").toString("utf8")
    if (!decoded.split("\n").some(line => line.startsWith("trusted comment:") && line.split("\t").includes(`version:${current}`))) throw new Error(`Missing version-bound signature for ${target}`)
    return [target, { url: `https://github.com/${repo}/releases/download/${tag}/${name}`, signature: signed }]
  }))
  return { version: current, notes, pub_date: new Date(created).toISOString(), platforms }
}

export async function publishFeed({ repo, manifest, token, request = fetch }) {
  repository(repo); version(manifest.version)
  if (!token) throw new Error("GitHub token is required")
  const api = async (path, method = "GET", body, allowMissing = false) => {
    const response = await request(`https://api.github.com/repos/${repo}/${path}`, {
      method, headers: { Authorization: `Bearer ${token}`, Accept: "application/vnd.github+json", "X-GitHub-Api-Version": "2022-11-28", "Content-Type": "application/json" },
      ...(body ? { body: JSON.stringify(body) } : {}),
    })
    if (allowMissing && response.status === 404) return null
    if (!response.ok) throw new Error(`Update feed API ${method} failed (${response.status})`)
    return response.status === 204 ? null : response.json()
  }
  if (!await api("git/ref/heads/desktop-updates", "GET", undefined, true)) {
    const tree = await api("git/trees", "POST", { tree: [{ path: ".gitignore", mode: "100644", type: "blob", content: "" }] })
    const commit = await api("git/commits", "POST", { message: "Initialize desktop update feeds", tree: tree.sha, parents: [] })
    try { await api("git/refs", "POST", { ref: "refs/heads/desktop-updates", sha: commit.sha }) }
    catch (error) { if (!await api("git/ref/heads/desktop-updates", "GET", undefined, true)) throw error }
  }
  const channels = version(manifest.version).pre.length ? ["candidate"] : ["stable", "candidate"]
  for (const channel of channels) {
    let published = false
    for (let attempt = 0; attempt < 3; attempt++) {
      const existing = await api(`contents/${channel}.json?ref=desktop-updates`, "GET", undefined, true)
      if (existing) {
        const previous = JSON.parse(Buffer.from(existing.content, "base64").toString("utf8"))
        if (compareVersions(previous.version, manifest.version) >= 0) { published = true; break }
      }
      try {
        await api(`contents/${channel}.json`, "PUT", {
          branch: "desktop-updates", message: `Publish desktop ${channel} ${manifest.version}`,
          content: Buffer.from(JSON.stringify(manifest, null, 2)+"\n").toString("base64"),
          ...(existing ? { sha: existing.sha } : {}),
        })
        published = true; break
      } catch (error) { if (attempt === 2) throw error }
    }
    if (!published) throw new Error("Update feed was not published")
  }
}

if (process.argv[1] && import.meta.url === pathToFileURL(resolve(process.argv[1])).href) {
  const [command, destination] = process.argv.slice(2)
  const repo = process.env.GITHUB_REPOSITORY, tag = process.env.TAG
  if (command === "config") {
    writeFileSync(destination, JSON.stringify(releaseConfig({ tag, repo, publicKey: process.env.DESKTOP_UPDATE_PUBLIC_KEY, platform: process.env.PLATFORM }), null, 2)+"\n")
  } else if (command === "manifest") {
    const manifest = updateManifest({ repo, tag, created: process.env.CREATED, notes: `Release ${tag}`, signature: name => {
      if (!readFileSync(join(destination, name)).length) throw new Error("Empty update artifact")
      return readFileSync(join(destination, name+".sig"), "utf8")
    } })
    writeFileSync(join(destination, `${repo.split("/")[1]}-desktop_${tag}_updates.json`), JSON.stringify(manifest, null, 2)+"\n")
  } else if (command === "publish") {
    await publishFeed({ repo, manifest: JSON.parse(readFileSync(destination, "utf8")), token: process.env.GH_TOKEN })
  } else throw new Error("Expected config, manifest or publish command")
}
