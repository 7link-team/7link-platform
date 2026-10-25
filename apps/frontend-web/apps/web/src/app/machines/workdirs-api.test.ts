import {expect, test} from "bun:test"
import {createIamApi} from "../../lib/iam-api"
import {createWorkdirsApi} from "./workdirs-api"

test("workdir commands preserve exact IDs version and retry identity on actual transport", async () => {
  const calls: {path: string; method: string; key: string | null; match: string | null; body: unknown}[] = []
  const server = Bun.serve({port: 0, async fetch(request) {
    calls.push({path: new URL(request.url).pathname, method: request.method, key: request.headers.get("Idempotency-Key"), match: request.headers.get("If-Match"), body: await request.json()})
    return Response.json({code: 0, data: {id: "9007199254740993", version: 4}})
  }})
  try {
    const api = createWorkdirsApi(createIamApi(`http://127.0.0.1:${server.port}`))
    const row = {id: "9007199254740993", project_id: "21", repository_id: "31", path: "D:/repo", version: 3}
    for (let n = 0; n < 2; n++) await api.update("11", row, {path: "D:/next", archived: true}, "same-intent")
    await api.probe("11", row.id)
    expect(calls[0]).toEqual(calls[1])
    expect(calls[0]).toEqual({path: `/machines/11/workdirs/${row.id}`, method: "PUT", key: "same-intent", match: "3", body: {path: "D:/next", archived: true}})
    expect(calls[2]?.body).toEqual({kind: "CAPABILITIES", workdir_id: row.id})
    expect(() => api.probe("11", "../private")).toThrow()
    expect(calls).toHaveLength(3)
  } finally {server.stop(true)}
})
