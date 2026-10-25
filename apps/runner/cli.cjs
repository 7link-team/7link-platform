#!/usr/bin/env node
const {spawnSync} = require("node:child_process")
const {join} = require("node:path")
const {accessSync, chmodSync, constants} = require("node:fs")

function executable(platform, arch) {
  if (!["win32", "darwin", "linux"].includes(platform) || !["x64", "arm64"].includes(arch)) throw new Error("Unsupported machine platform")
  return join(__dirname, "bin", `${platform}-${arch}`, platform === "win32" ? "machine-runner.exe" : "machine-runner")
}

if (require.main === module) {
  try {
    const binary = executable(process.platform, process.arch)
    // npm archives produced on Windows can lose Unix executable bits.
    if (process.platform !== "win32") {
      try {accessSync(binary, constants.X_OK)} catch {chmodSync(binary, 0o755)}
    }
    const result = spawnSync(binary, process.argv.slice(2), {stdio: "inherit", windowsHide: true})
    if (result.error) throw result.error
    process.exitCode = result.status ?? 1
  } catch {
    process.stderr.write("Machine program unavailable for this platform; reinstall a complete release package.\n")
    process.exitCode = 1
  }
}
module.exports = {executable}
