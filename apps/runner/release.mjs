import {spawnSync} from "node:child_process"
import {mkdirSync} from "node:fs"
import {dirname, join} from "node:path"
import {fileURLToPath, pathToFileURL} from "node:url"

export const targets = ["win32-x64", "win32-arm64", "darwin-x64", "darwin-arm64", "linux-x64", "linux-arm64"]
export function buildTargets(selection, build = spawnSync) {
  const root = dirname(fileURLToPath(import.meta.url))
  const selected = selection.length ? selection : targets
  if (selected.some(target => !targets.includes(target))) throw new Error("Unsupported release target")
  for (const target of selected) {
    const [platform, architecture] = target.split("-")
    const output = join(root, "bin", target, platform === "win32" ? "machine-runner.exe" : "machine-runner")
    mkdirSync(dirname(output), {recursive: true})
    const result = build("go", ["build", "-p", "1", "-trimpath", "-o", output, "./cmd/runner"], {cwd: root, stdio: "inherit", windowsHide: true, env: {...process.env, CGO_ENABLED: "0", GOOS: platform === "win32" ? "windows" : platform, GOARCH: architecture === "x64" ? "amd64" : "arm64"}})
    if (result.error || result.status !== 0) throw new Error(`Native build failed for ${target}`)
  }
}
if (process.argv[1] && import.meta.url === pathToFileURL(process.argv[1]).href) buildTargets(process.argv.slice(2))
