const {test} = require("node:test")
const assert = require("node:assert/strict")
const {executable} = require("./cli.cjs")
const {spawnSync} = require("node:child_process")
const {mkdtempSync, copyFileSync, rmSync} = require("node:fs")
const {tmpdir} = require("node:os")
const {join} = require("node:path")

test("only supported native binary paths can be selected", () => {
  for (const os of ["win32", "darwin", "linux"]) for (const arch of ["x64", "arm64"]) assert.ok(executable(os, arch).includes(`${os}-${arch}`))
  assert.throws(() => executable("../private", "x64"))
  assert.throws(() => executable("linux", "../../private"))
})

test("an incomplete installed package fails explicitly instead of reporting success", () => {
  const directory = mkdtempSync(join(tmpdir(), "machine-launcher-"))
  try {
    const launcher = join(directory, "cli.cjs")
    copyFileSync(join(__dirname, "cli.cjs"), launcher)
    const result = spawnSync(process.execPath, [launcher, "--help"], {encoding: "utf8", windowsHide: true})
    assert.equal(result.status, 1)
    assert.match(result.stderr, /reinstall a complete release package/)
  } finally {rmSync(directory, {recursive: true, force: true})}
})
