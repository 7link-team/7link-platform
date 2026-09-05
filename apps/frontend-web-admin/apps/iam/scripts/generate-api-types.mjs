// Regenerates src/lib/api-schema.ts from the backend's OpenAPI document.
//
// The document itself is the backend's to publish. Until it is committed there,
// pass a running instance instead:
//
//   bun scripts/generate-api-types.mjs --spec http://localhost:8091/api/openapi.json
//
// Generating from a running instance is fine for a person at a keyboard and no
// good for CI, which is why the default is a path on disk.

import { spawnSync } from "node:child_process"
import { existsSync, mkdtempSync, rmSync, writeFileSync } from "node:fs"
import { tmpdir } from "node:os"
import { join } from "node:path"
import { fileURLToPath } from "node:url"

const repoRoot = fileURLToPath(new URL("../../../../../", import.meta.url))
const defaultSpec = join(repoRoot, "apps/backend-api/docs/openapi.json")
const output = fileURLToPath(new URL("../src/lib/api-schema.ts", import.meta.url))

const flag = process.argv.indexOf("--spec")
const spec = flag === -1 ? defaultSpec : process.argv[flag + 1]
if (!spec) throw new Error("--spec needs a path or a URL")

let input = spec
let scratch

if (/^https?:\/\//.test(spec)) {
  const response = await fetch(spec)
  if (!response.ok)
    throw new Error(`${spec} answered ${response.status}`)
  scratch = mkdtempSync(join(tmpdir(), "iam-openapi-"))
  input = join(scratch, "openapi.json")
  writeFileSync(input, await response.text())
} else if (!existsSync(input)) {
  throw new Error(
    `${input} does not exist. The backend publishes this document; until it does, ` +
      `point --spec at a running instance.`
  )
}

try {
  const result = spawnSync(
    "bun",
    ["x", "openapi-typescript", input, "-o", output],
    { stdio: "inherit", shell: process.platform === "win32" }
  )
  if (result.status !== 0) process.exit(result.status ?? 1)
} finally {
  if (scratch) rmSync(scratch, { recursive: true, force: true })
}
