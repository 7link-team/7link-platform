import { describe, expect, test } from "bun:test"
import {
  devAccountsForEntrance,
  parseDevLoginAccounts,
} from "./dev-login-accounts"

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
  test("shows only explicitly assigned entrance personas", () => {
    const accounts = parseDevLoginAccounts(
      JSON.stringify([
        {
          id: "system",
          role: "System",
          loginName: "system",
          password: "secret",
          entrances: ["system"],
        },
        {
          id: "tenant",
          role: "Tenant",
          loginName: "tenant",
          password: "secret",
          entrances: ["tenant"],
        },
        {
          id: "user",
          role: "User",
          loginName: "user",
          password: "secret",
          entrances: ["user"],
        },
        {
          id: "legacy",
          role: "Legacy",
          loginName: "legacy",
          password: "secret",
        },
        {
          id: "invalid",
          role: "Invalid",
          loginName: "invalid",
          password: "secret",
          entrances: ["invalid"],
        },
      ]),
      true
    )
    expect(
      devAccountsForEntrance("system", accounts).map((account) => account.id)
    ).toEqual(["system"])
    expect(
      devAccountsForEntrance("tenant", accounts).map((account) => account.id)
    ).toEqual(["tenant"])
    expect(
      devAccountsForEntrance(null, accounts).map((account) => account.id)
    ).toEqual(["user"])
    expect(devAccountsForEntrance("platform", accounts)).toEqual([])
  })
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
