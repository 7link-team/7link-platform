import { describe, expect, it } from "bun:test"
import type { ApiClient } from "../../lib/api-client"
import { crudFactory, crudTableOptions, type CrudResource } from "./resource"
import type { OperationRequestContract } from "../auto-form/contract"

const noFallback = new Proxy({} as ApiClient, {
  get: (_, name) => () => {
    throw new Error(`Unexpected fallback request: ${String(name)}`)
  },
})

describe("resource operation bindings", () => {
  it("uses the existing client's read/create/update callbacks without guessing REST paths", async () => {
    const calls: unknown[] = []
    const resource: CrudResource = {
      basePath: "/console/roles",
      columns: [],
      getFormDef: () => ({}),
      getInfo: async (id) => {
        calls.push(["GET", "/roles", id])
        return { id, name: "Role" }
      },
      commitAdd: async (body) =>
        calls.push(["POST", "/tenants/current/roles", body]),
      commitEdit: async (id, body) => calls.push(["PATCH", "/roles", id, body]),
    }
    const factory = crudFactory(resource, noFallback, "edit")
    expect(await factory.getInfo("42")).toEqual({ id: "42", name: "Role" })
    await factory.commitAdd({ name: "New" })
    await factory.commitEdit("42", { description: "Changed" })
    expect(calls).toEqual([
      ["GET", "/roles", "42"],
      ["POST", "/tenants/current/roles", { name: "New" }],
      ["PATCH", "/roles", "42", { description: "Changed" }],
    ])
  })

  it("keeps mode-specific metadata, including the unavailable contract sentinel", () => {
    const contract: OperationRequestContract = {
      version: "1",
      operation_id: "create",
      method: "POST",
      path: "/roles",
      request_body: null,
      schemas: {},
    }
    const resource: CrudResource = {
      basePath: "/roles",
      columns: [],
      getFormDef: () => ({}),
      getContract: (mode) =>
        mode === "add" || mode === "copy" ? contract : null,
    }
    expect(crudFactory(resource, noFallback, "copy").contract).toBe(contract)
    expect(crudFactory(resource, noFallback, "add").contract).toBe(contract)
    expect(crudFactory(resource, noFallback, "edit").contract).toBeNull()
    expect(
      crudFactory({ ...resource, getContract: undefined }, noFallback, "edit")
        .contract
    ).toBeUndefined()
  })

  it("preserves the existing REST defaults and resource mappings", async () => {
    const calls: unknown[] = []
    const api = {
      get: async (url: string) => {
        calls.push(["GET", url])
        return { data: { id: "1" } }
      },
      post: async (url: string, body: unknown) => {
        calls.push(["POST", url, body])
      },
      put: async (url: string, body: unknown) => {
        calls.push(["PUT", url, body])
      },
    } as unknown as ApiClient
    const resource: CrudResource = {
      basePath: "/items",
      apiPath: "/api/items",
      createApiPath: "/api/new-items",
      columns: [],
      getFormDef: () => ({}),
      toLocalData: (row) => ({ ...row, local: true }),
      toServerData: (row) => ({ name: row.name }),
      editable: false,
    }
    const factory = crudFactory(resource, api, "edit")
    expect(await factory.getInfo("1")).toEqual({ id: "1" })
    await factory.commitAdd({ name: "A" })
    await factory.commitEdit("1", { name: "B" })
    expect(calls).toEqual([
      ["GET", "/api/items/1"],
      ["POST", "/api/new-items", { name: "A" }],
      ["PUT", "/api/items/1", { name: "B" }],
    ])
    expect(factory.toLocalData).toBe(resource.toLocalData)
    expect(factory.toServerData).toBe(resource.toServerData)
    expect(factory.editable).toBe(false)
  })
})

describe("resource list and action bindings", () => {
  const nav = { detail: () => {}, edit: () => {}, copy: () => {} }
  it("passes normalized list requests to the existing client binding", async () => {
    const queries: unknown[] = []
    const resource: CrudResource = {
      basePath: "/ui/items",
      columns: [],
      getFormDef: () => ({}),
      fetch: async (query) => {
        queries.push(query)
        return { list: [{ id: "1" }], total: 1, offset: query.offset ?? 0 }
      },
    }
    const table = crudTableOptions(resource, {
      api: noFallback,
      nav,
      refresh: () => {},
    })
    const query = {
      offset: 20,
      limit: 10,
      filter: { owner: "self" },
      order: { name: "ASC" as const },
      fields: ["id"],
    }
    expect(await table.fetch!(query)).toEqual({
      list: [{ id: "1" }],
      total: 1,
      offset: 20,
    })
    expect(queries).toEqual([query])
  })

  it("uses declared action rules for rendering and invocation", async () => {
    let navigations = 0,
      deletes = 0,
      confirmations = 0
    const resource: CrudResource = {
      basePath: "/items",
      columns: [],
      getFormDef: () => ({}),
      deleteRecord: async () => {
        deletes++
      },
      actionRules: {
        detail: { show: (row) => row.visible === true },
        edit: { disabled: (row) => row.locked === true },
        copy: { show: () => false },
        delete: { show: (row) => row.owned === true },
      },
    }
    const options = crudTableOptions(resource, {
      api: noFallback,
      nav: {
        detail: () => {
          navigations++
        },
        edit: () => {
          navigations++
        },
        copy: () => {
          navigations++
        },
      },
      refresh: () => {},
      confirm: async () => {
        confirmations++
        return true
      },
    })
    const row = { id: "1", visible: false, locked: true, owned: false }
    options.onclick?.(row)
    for (const action of options.actions!) await action.onclick?.(row)
    expect(navigations).toBe(0)
    expect(deletes).toBe(0)
    expect(confirmations).toBe(0)
    expect(options.actions![0]!.disabled?.(row)).toBe(true)
    expect(options.actions![1]!.show?.(row)).toBe(false)
    expect(options.actions![2]!.show?.(row)).toBe(false)
  })

  it("rechecks deletion after confirmation, and refreshes only after successful custom deletion", async () => {
    let allowed = true,
      deletes = 0,
      refreshes = 0,
      fail = false
    let resolveConfirm!: (value: boolean) => void
    const resource: CrudResource = {
      basePath: "/console/items",
      columns: [],
      getFormDef: () => ({}),
      features: { edit: false, copy: false },
      deleteRecord: async (row) => {
        expect(row.id).toBe("1")
        if (fail) throw Error("failed")
        deletes++
      },
      actionRules: { delete: { disabled: () => !allowed } },
    }
    const options = crudTableOptions(resource, {
      api: noFallback,
      nav,
      refresh: () => {
        refreshes++
      },
      confirm: () =>
        new Promise<boolean>((resolve) => {
          resolveConfirm = resolve
        }),
    })
    const remove = options.actions![0]!.onclick!
    const denied = remove({ id: "1" })
    allowed = false
    resolveConfirm(true)
    await denied
    expect(deletes).toBe(0)
    expect(refreshes).toBe(0)
    allowed = true
    const cancelled = remove({ id: "1" })
    resolveConfirm(false)
    await cancelled
    expect(deletes).toBe(0)
    const success = remove({ id: "1" })
    resolveConfirm(true)
    await success
    expect(deletes).toBe(1)
    expect(refreshes).toBe(1)
    fail = true
    const rejected = remove({ id: "1" })
    resolveConfirm(true)
    await expect(Promise.resolve(rejected)).rejects.toThrow("failed")
    expect(refreshes).toBe(1)
  })
})
