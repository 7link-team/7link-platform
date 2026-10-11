import { describe, expect, it } from "bun:test"
import { ApiError, type FilePage } from "./iam-api"
import {
  fileBrowserViewLoader,
  type FileCollectionScope,
} from "./file-collections"

const updated = "2026-10-09T00:00:00Z"

function root(id: string, owner: Record<string, string>, scope: string) {
  return {
    id,
    name: `root ${id}`,
    kind: "directory",
    scope,
    revision: 1,
    updated_at: updated,
    tenant_id: "7",
    ...owner,
  }
}

function service(pages: Record<string, FilePage | Error> = {}) {
  const calls: string[] = []
  const api = {
    files: async (
      entityID?: string | null,
      after = "",
      system = false,
      state: "active" | "trashed" = "active"
    ) => {
      const owner = entityID === null ? "account" : (entityID ?? "tenant")
      calls.push(`files ${owner} ${state} ${after}`)
      if (system) return { items: [] }
      const page = pages[`${owner} ${state} ${after}`]
      if (page instanceof Error) throw page
      return (
        page ??
        (state === "trashed"
          ? { items: [] }
          : owner === "tenant"
            ? { items: [root("10", {}, "tenant")] }
            : owner === "account"
              ? {
                  items: [
                    {
                      ...root("11", { principal_id: "9" }, "account"),
                      tenant_id: undefined,
                    },
                  ],
                }
              : { items: [root(`2${owner}`, { entity_id: owner }, "entity")] })
      )
    },
    departmentFiles: async (
      departmentID: string,
      after = "",
      state: "active" | "trashed" = "active"
    ) => {
      calls.push(`department ${departmentID} ${state} ${after}`)
      if (departmentID === "31") throw new ApiError(403, "forbidden")
      return state === "trashed"
        ? { items: [] }
        : { items: [root("30", { department_id: departmentID }, "department")] }
    },
    departments: async () => {
      calls.push("departments")
      return [
        { id: "30", name: "Sales", entity_id: "5" },
        { id: "31", name: "Hidden", entity_id: "6" },
      ]
    },
    memberEntities: async (permission: string, tenant?: string) => {
      calls.push(`entities ${permission} ${tenant}`)
      return [{ id: "5", name: "Five", status: "active" }]
    },
    fileItems: async (
      _entityID: string | null | undefined,
      fileID: string,
      after = ""
    ) => {
      calls.push(`items ${fileID} ${after}`)
      return {
        items: [
          {
            id: "100",
            name: `child of ${fileID}`,
            kind: "file",
            revision: 1,
            updated_at: updated,
          },
        ],
        path: [{ id: fileID, name: `dir ${fileID}` }],
      }
    },
  }
  return {
    calls,
    load: fileBrowserViewLoader(
      api as unknown as Parameters<typeof fileBrowserViewLoader>[0]
    ),
  }
}

const tenantScope: FileCollectionScope = {
  account: false,
  system: false,
  tenantID: "7",
  entityID: "",
  canViewDepartments: true,
  recycleBin: false,
  reload: 0,
}

const collectionReads = (calls: readonly string[]) =>
  calls.filter((call) => !call.startsWith("items "))

describe("file browser view loading", () => {
  it("reads every collection of a tenant scope and names their roots", async () => {
    const { calls, load } = service()
    const view = await load(tenantScope, null, "")
    expect(collectionReads(calls).sort()).toEqual([
      "department 30 active ",
      "department 31 active ",
      "departments",
      "entities file_view 7",
      "files 5 active ",
      "files tenant active ",
    ])
    expect(view.roots.map((root) => [root.id, root.ownerName])).toEqual([
      ["10", undefined],
      ["25", "Five"],
      ["30", "Sales"],
    ])
    // With no directory asked for, the first root is listed.
    expect(view.directoryID).toBe("10")
    expect(view.nodes.map((node) => node.name)).toEqual(["child of 10"])
  })

  it("reads only the directory when moving between directories and pages", async () => {
    const { calls, load } = service()
    await load(tenantScope, null, "")
    const collections = collectionReads(calls).length
    const view = await load(tenantScope, "25", "")
    await load(tenantScope, "25", "100")
    await load(tenantScope, "30", "")
    expect(collectionReads(calls)).toHaveLength(collections)
    expect(calls.filter((call) => call.startsWith("items "))).toEqual([
      "items 10 ",
      "items 25 ",
      "items 25 100",
      "items 30 ",
    ])
    expect(view.directoryID).toBe("25")
    expect(view.path).toEqual([{ id: "25", name: "dir 25" }])
  })

  it("reads the collections again when the scope or a reload asks for it", async () => {
    const { calls, load } = service()
    await load(tenantScope, null, "")
    const once = collectionReads(calls).length
    await load({ ...tenantScope, reload: 1 }, "25", "")
    expect(collectionReads(calls)).toHaveLength(once * 2)
    await load({ ...tenantScope, reload: 1, entityID: "5" }, null, "")
    expect(
      collectionReads(calls)
        .slice(once * 2)
        .sort()
    ).toEqual(["department 30 active ", "departments", "files 5 active "])
  })

  it("pages the recycle bin without reading the active collections again", async () => {
    const { calls, load } = service()
    const bin = { ...tenantScope, recycleBin: true }
    await load(bin, null, "")
    const active = calls.filter((call) => call.includes(" active ")).length
    await load(bin, null, "200")
    expect(calls.filter((call) => call.includes(" active "))).toHaveLength(
      active
    )
    expect(
      calls.filter((call) => call.includes(" trashed 200")).sort()
    ).toEqual([
      "department 30 trashed 200",
      "department 31 trashed 200",
      "files 5 trashed 200",
      "files tenant trashed 200",
    ])
  })

  it("asks again after a failed read rather than keeping the failure", async () => {
    const pages: Record<string, FilePage | Error> = {
      "account active ": new Error("offline"),
    }
    const { calls, load } = service(pages)
    const account = { ...tenantScope, account: true, tenantID: "" }
    await expect(load(account, null, "")).rejects.toThrow("offline")
    delete pages["account active "]
    const view = await load(account, null, "")
    expect(view.roots.map((root) => root.id)).toEqual(["11"])
    expect(calls.filter((call) => call.startsWith("files "))).toEqual([
      "files account active ",
      "files account active ",
    ])
  })

  it("reads an account's own collection once and its bin by page", async () => {
    const { calls, load } = service()
    const account = { ...tenantScope, account: true, tenantID: "" }
    const view = await load(account, null, "")
    expect(view.roots.map((root) => root.id)).toEqual(["11"])
    await load({ ...account, recycleBin: true }, null, "300")
    expect(calls).toEqual([
      "files account active ",
      "items 11 ",
      "files account active ",
      "files account trashed 300",
    ])
  })

  it("refuses a root cursor that is not a file ID or does not advance", async () => {
    for (const cursors of [["abc"], ["-1"], ["10", "10"], ["10", "9"]]) {
      const pages: Record<string, FilePage> = {}
      let after = ""
      for (const next_after of cursors) {
        pages[`tenant active ${after}`] = {
          items: after ? [] : [root("10", {}, "tenant")],
          next_after,
        }
        after = next_after
      }
      const { load } = service(pages)
      await expect(load(tenantScope, null, "")).rejects.toThrow(
        "invalid_response"
      )
    }
  })
})
