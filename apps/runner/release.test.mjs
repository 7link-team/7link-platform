import {test} from "node:test"
import assert from "node:assert/strict"
import {buildTargets, targets} from "./release.mjs"

test("release includes six native platforms and refuses partial build success", () => {
  const calls = []
  buildTargets([], (command, args, options) => {calls.push({command, args, options}); return {status: 0}})
  assert.equal(calls.length, 6)
  assert.deepEqual(calls.map(call => `${call.options.env.GOOS}/${call.options.env.GOARCH}`), ["windows/amd64", "windows/arm64", "darwin/amd64", "darwin/arm64", "linux/amd64", "linux/arm64"])
  assert.ok(calls.every(call => call.options.windowsHide && call.options.env.CGO_ENABLED === "0"))
  assert.throws(() => buildTargets([targets[0]], () => ({status: 1})))
  assert.throws(() => buildTargets(["unknown"], () => ({status: 0})))
})
