import {describe, expect, it} from "bun:test"
import {createIamApi} from "../../lib/iam-api"
import {machineListFilters} from "./filters-state"

describe("machine filters request", () => {
  it("restores URL filters and drops invalid enum values", () => {
    expect(machineListFilters(new URLSearchParams("state=ONLINE&runtime=CODEX&free_slots=true"))).toEqual({state: "ONLINE", runtime: "CODEX", free_slots: true})
    expect(machineListFilters(new URLSearchParams("state=UNKNOWN&runtime=UNKNOWN&free_slots=false"))).toEqual({state: undefined, runtime: undefined, free_slots: undefined})
  })

  it("sends filters before server paging and preserves string cursor IDs", async () => {
    const requests: URL[] = []
    const server = Bun.serve({port: 0, fetch(request) {
      requests.push(new URL(request.url))
      return Response.json({code: 0, data: {items: [], has_more: false}})
    }})
    try {
      const api = createIamApi(`http://127.0.0.1:${server.port}`, () => "7")
      await api.machines("7", "9007199254740993", undefined, {state: "ONLINE", runtime: "CODEX", free_slots: true})
      await api.machines("7")
      const query = requests[0]!.searchParams
      expect(query.get("state")).toBe("ONLINE")
      expect(query.get("runtime")).toBe("CODEX")
      expect(query.get("free_slots")).toBe("true")
      expect(query.get("cursor")).toBe("9007199254740993")
      expect(query.get("limit")).toBe("30")
      expect(requests[1]!.searchParams.has("state")).toBe(false)
      expect(requests[1]!.searchParams.has("runtime")).toBe(false)
      expect(requests[1]!.searchParams.has("free_slots")).toBe(false)
    } finally {server.stop(true)}
  })

})
