import { expect, test } from "bun:test"
import { principalLabel } from "./principal-label"

test("principal labels use permitted names, never duplicate or invent an identity", () => {
  const label = (value?: Parameters<typeof principalLabel>[0]) =>
    principalLabel(value, "Unavailable account", "Anonymized account")
  expect(label({ display_name: "Alice", login_name: "alice" })).toBe("Alice（alice）")
  expect(label({ display_name: "alice", login_name: "alice" })).toBe("alice")
  expect(label({ display_name: " Alice " })).toBe("Alice")
  expect(label({ login_name: "alice" })).toBe("alice")
  expect(label({})).toBe("Unavailable account")
  expect(label()).toBe("Unavailable account")
  expect(label({ anonymized: true, login_name: "erased:123", display_name: "old name" })).toBe("Anonymized account")
})
