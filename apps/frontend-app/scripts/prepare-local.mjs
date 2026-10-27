import { spawnSync } from "node:child_process"
import { cpSync, existsSync, mkdirSync, readFileSync, rmSync } from "node:fs"
import { dirname, join, resolve } from "node:path"
import { fileURLToPath } from "node:url"

const app = resolve(dirname(fileURLToPath(import.meta.url)), "..")
const repository = resolve(app, "../..")
const resources = join(app, "src-tauri/resources/local")
const config = JSON.parse(readFileSync(join(app, "src-tauri/tauri.conf.json"), "utf8"))
const run = (command, args, cwd, env = {}) => {
  const result = spawnSync(command, args, { cwd, env: { ...process.env, ...env }, stdio: "inherit", windowsHide: true })
  if (result.error) throw result.error
  if (result.status !== 0) throw new Error(`${command} failed (${result.status})`)
}
// Only this generated, ignored resource tree is replaced. User data never
// lives alongside packaged resources or in the source/build directories.
if (resources !== resolve(app, "src-tauri/resources/local")) throw new Error("Invalid resource directory")
rmSync(resources, { recursive: true, force: true })
mkdirSync(resources, { recursive: true })
const web = process.env.DESKTOP_WEB_DIST
  ? resolve(process.env.DESKTOP_WEB_DIST)
  : join(repository, "apps/frontend-web/apps/web/dist")
if (!process.env.DESKTOP_WEB_DIST) {
  const workspace = join(repository, "apps/frontend-web")
  run("bun", ["install", "--frozen-lockfile"], workspace)
  run("bun", ["run", "--cwd", "apps/web", "build"], workspace, { VITE_API_URL: "/api" })
}
if (!existsSync(join(web, "index.html"))) throw new Error("Production Web index.html missing")
cpSync(web, join(resources, "web"), { recursive: true })
const executable = process.platform === "win32" ? "local-service.exe" : "local-service"
const version = process.env.TAG || config.version
run("go", ["build", "-p", "1", "-trimpath", "-buildvcs=false", "-ldflags",
  `-s -w -X 7link.com/platform/cmd/7link-api/cmd.buildVersion=${version}`,
  "-o", join(resources, executable), "./cmd/7link-api"], join(repository, "apps/backend-api"), { CGO_ENABLED: "1" })
console.log(`Prepared ${config.productName} local resources (${process.platform}/${process.arch})`)
