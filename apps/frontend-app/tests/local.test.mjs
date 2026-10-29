import { expect, test } from "bun:test"
import { showChooser, startLocal } from "../launcher/local.mjs"

function chooser() {
  const fields = new Map()
  for (const id of ["start-local", "local-setup", "local-status", "local-login", "local-password", "local-password-confirm", "submit"]) {
    fields.set(id, { value: "", textContent: "", hidden: id === "local-setup", disabled: false, listeners: {},
      addEventListener(name, listener) { this.listeners[name] = listener },
      setAttribute(name, value) { this[name] = value },
      focus() { this.focused = true },
      querySelector() { return fields.get("submit") },
    })
  }
  return { fields, doc: { querySelector: selector => fields.get(selector.slice(1)) } }
}
const settle = () => new Promise(resolve => setTimeout(resolve, 0))

test("local setup uses parent commands, clears passwords and navigates only after readiness", async () => {
  const { fields, doc } = chooser()
  const commands = [], destinations = []
  startLocal(doc, async (command, args) => {
    commands.push({ command, args: args ? { ...args } : undefined })
    return command === "start_local" ? { event: "setup" } : { event: "ready", origin: "http://127.0.0.1:32123" }
  }, origin => destinations.push(origin))
  fields.get("start-local").listeners.click()
  await settle()
  expect(fields.get("local-setup").hidden).toBe(false)
  expect(fields.get("local-login").focused).toBe(true)
  expect(destinations).toEqual([])
  fields.get("local-login").value = " owner "
  fields.get("local-password").value = fields.get("local-password-confirm").value = "chosen password"
  await fields.get("local-setup").listeners.submit({ preventDefault() {} })
  expect(commands).toEqual([{ command: "start_local", args: undefined }, { command: "setup_local", args: { login: "owner", password: "chosen password" } }])
  expect(fields.get("local-password").value).toBe("")
  expect(fields.get("local-password-confirm").value).toBe("")
  expect(destinations).toEqual(["http://127.0.0.1:32123"])
})

test("local loading prevents duplicate starts and failures leave a usable retry button", async () => {
  const { fields, doc } = chooser()
  let reject, calls = 0
  startLocal(doc, () => { calls++; return new Promise((_, fail) => { reject = fail }) }, () => { throw new Error("unexpected navigation") })
  fields.get("start-local").listeners.click()
  fields.get("start-local").listeners.click()
  expect(calls).toBe(1)
  expect(fields.get("start-local").disabled).toBe(true)
  expect(fields.get("local-setup")["aria-busy"]).toBe("true")
  reject(new Error("port occupied"))
  await settle()
  expect(fields.get("start-local").disabled).toBe(false)
  expect(fields.get("local-status").textContent).toContain("port occupied")
  expect(fields.get("local-setup")["aria-busy"]).toBe("false")
})

test("mismatched setup passwords never invoke native initialization", async () => {
  const { fields, doc } = chooser()
  let calls = 0
  startLocal(doc, () => { calls++ }, () => {})
  fields.get("local-password").value = "first"
  fields.get("local-password-confirm").value = "different"
  await fields.get("local-setup").listeners.submit({ preventDefault() {} })
  expect(calls).toBe(0)
  expect(fields.get("local-password-confirm").focused).toBe(true)
  expect(fields.get("local-status").textContent).toContain("不一致")
})

test("backend initialization refusal remains setup and does not navigate", async () => {
  const { fields, doc } = chooser()
  startLocal(doc, async () => ({ event: "setup", message: "password policy refused" }), () => { throw new Error("unexpected navigation") })
  fields.get("start-local").listeners.click()
  await settle()
  expect(fields.get("local-setup").hidden).toBe(false)
  expect(fields.get("local-status").textContent).toBe("password policy refused")
})

test("restoring local mode hides connection controls until setup or a recoverable error", async () => {
  const { doc } = chooser()
  let revealCount = 0
  const destinations = []
  let complete
  const start = startLocal(doc, () => new Promise(resolve => { complete = resolve }),
    origin => destinations.push(origin), () => { revealCount++ })
  const pending = start()
  expect(revealCount).toBe(0)
  complete({ event: "ready", origin: "http://127.0.0.1:32123" })
  await pending
  expect(revealCount).toBe(0)
  expect(destinations).toEqual(["http://127.0.0.1:32123"])

  for (const response of [{ event: "setup" }, { event: "invalid" }, new Error("start failed")]) {
    const retry = startLocal(doc, async () => {
      if (response instanceof Error) throw response
      return response
    }, () => { throw new Error("unexpected navigation") }, () => { revealCount++ })
    await retry()
  }
  expect(revealCount).toBe(3)
})

test("revealing the chooser removes startup feedback and exposes connection controls", () => {
  const progress = { hidden: false }, controls = { hidden: true }
  showChooser({ querySelector: selector => selector === "#connection-progress" ? progress : controls })
  expect(progress.hidden).toBe(true)
  expect(controls.hidden).toBe(false)
})
