import { describe, expect, it } from "bun:test"
import { apiRoute, pagedRoute, type ApiRoute } from "./api-route"

// Included in the app's typecheck; these assertions must remain compiler errors.
function contractErrors() {
  // @ts-expect-error unpublished template
  apiRoute("get", "/missing")
  // @ts-expect-error unsupported method
  apiRoute("post", "/files/{file_id}/content", { path: { file_id: "1" } })
  // @ts-expect-error required path parameters
  apiRoute("get", "/files/{file_id}")
  // @ts-expect-error wrong parameter name
  apiRoute("get", "/files/{file_id}", { path: { id: "1" } })
  // @ts-expect-error wrong parameter type
  apiRoute("get", "/files/{file_id}", { path: { file_id: 1 } })
  // @ts-expect-error unknown query parameter
  apiRoute("get", "/me/files", { query: { offset: 0 } })
  // @ts-expect-error numeric queries must be numbers
  apiRoute("get", "/me/files", { query: { limit: "100" } })
  // @ts-expect-error query enum
  apiRoute("get", "/me/files", { query: { state: "deleted" } })
  // @ts-expect-error required query parameters
  apiRoute("delete", "/tenants/{tenant_id}/authorization-relationships", {
    path: { tenant_id: "1" },
  })
  // @ts-expect-error cursor routes cannot use offset pagination
  pagedRoute("/me/files")
  // @ts-expect-error operations without queries cannot use offset pagination
  pagedRoute("/me")
  // @ts-expect-error strings cannot enter the transport
  const address: ApiRoute = "/me"
  return address
}
void contractErrors

describe("OpenAPI request addresses", () => {
  it("encodes identifiers once and serializes typed query values", () => {
    expect(
      apiRoute("get", "/tenants/{tenant_id}/members", {
        path: { tenant_id: "a/b %" },
        query: { search: "a+b &c", offset: 0, limit: 200 },
      }).url
    ).toBe("/tenants/a%2Fb%20%25/members?search=a%2Bb+%26c&offset=0&limit=200")
  })
  it("preserves relative file separators but encodes each segment", () => {
    expect(
      apiRoute("get", "/files/{root_file_id}/-/{path}", {
        path: { root_file_id: "a/b", path: "one two/三/%2F" },
      }).url
    ).toBe("/files/a%2Fb/-/one%20two/%E4%B8%89/%252F")
  })
  it("omits undefined queries and retains zero and false", () => {
    expect(
      apiRoute("get", "/tenants", {
        query: { include_deleted: false, offset: 0, limit: undefined },
      }).url
    ).toBe("/tenants?include_deleted=false&offset=0")
  })
  it("pages only published offset/limit operations", () => {
    const route = pagedRoute("/tenants/{tenant_id}/members", {
      path: { tenant_id: "1" },
      query: { search: "a b" },
    })
    expect(route.page(200, 200).url).toBe(
      "/tenants/1/members?search=a+b&offset=200&limit=200"
    )
    expect(route.url).toBe("/tenants/1/members?search=a+b")
  })
})
