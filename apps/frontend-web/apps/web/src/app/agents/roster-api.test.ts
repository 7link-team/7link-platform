import { expect, it } from "bun:test"
import { createIamApi } from "../../lib/iam-api"
import { createRosterApi, emptyRosterFilters } from "./roster-api"

it("preserves opaque IDs, exact role/source filters and global offset sorting", async () => {
  let observed: URL | undefined
  const server = Bun.serve({ port: 0, fetch(request) {
    observed = new URL(request.url)
    return Response.json({ code: 0, data: { items: [{ kind: "HUMAN", principal_id: "9007199254740995" }], total: 51, has_more: false } })
  } })
  try {
    const api = createRosterApi(createIamApi(`http://localhost:${server.port}`))
    const page = await api.list("9007199254740993", { ...emptyRosterFilters, search: "A & B", role: "LEAD", machine_id: "9007199254740997", origin: "AGENT_SPAWNED", sort: "-fpa_30d" }, 50)
    expect(page.items[0]?.principal_id).toBe("9007199254740995")
    expect(observed?.pathname).toBe("/tenants/9007199254740993/roster-entries")
    expect(Object.fromEntries(observed!.searchParams)).toEqual({ limit: "50", offset: "50", search: "A & B", role: "LEAD", machine_id: "9007199254740997", origin: "AGENT_SPAWNED", sort: "-fpa_30d" })
    const abort = new AbortController(); abort.abort()
    await expect(api.list("7", emptyRosterFilters, 0, abort.signal)).rejects.toThrow()
  } finally { await server.stop(true) }
})

it("surfaces current roster authorization failures", async () => {
  const server = Bun.serve({ port: 0, fetch() { return Response.json({ message: "Forbidden" }, { status: 403 }) } })
  try { await expect(createRosterApi(createIamApi(`http://localhost:${server.port}`)).list("7", emptyRosterFilters, 0)).rejects.toThrow() }
  finally { await server.stop(true) }
})
