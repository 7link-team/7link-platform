import { describe, expect, test } from "bun:test"
import { parseDevLoginAccounts } from "./dev-login-accounts"

const configuredAccounts = JSON.stringify([
  {
    id: "admin",
    role: "Administrator",
    loginName: "admin@example.test",
    password: "local-secret",
    tenantID: "38001505780891648",
  },
])

describe("parseDevLoginAccounts", () => {
  test("returns configured accounts in development", () => {
    expect(parseDevLoginAccounts(configuredAccounts, true)).toEqual([
      {
        id: "admin",
        role: "Administrator",
        loginName: "admin@example.test",
        password: "local-secret",
        tenantID: "38001505780891648",
      },
    ])
  })

  test("never exposes configured credentials outside development", () => {
    expect(parseDevLoginAccounts(configuredAccounts, false)).toEqual([])
  })

  test("ignores malformed or incomplete configuration", () => {
    expect(parseDevLoginAccounts("not-json", true)).toEqual([])
    expect(
      parseDevLoginAccounts(
        JSON.stringify([{ id: "member", role: "Member" }]),
        true
      )
    ).toEqual([])
    expect(
      parseDevLoginAccounts(
        JSON.stringify([
          {
            id: "member",
            role: "Member",
            loginName: "member@example.test",
            password: "secret",
            tenantID: 123,
          },
        ]),
        true
      )
    ).toEqual([])
  })
})
