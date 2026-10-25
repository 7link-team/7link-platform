import {expect, test} from "bun:test"
import {createIamApi} from "../../lib/iam-api"
import {createModelCatalogApi} from "./model-catalog-api"
test("manual catalogs preserve exact ID, CAS version and overrides; conflicts remain errors", async () => {
 const calls: {path: string; method: string; body: unknown}[] = []
 const server = Bun.serve({port: 0, async fetch(request) {
  calls.push({path: new URL(request.url).pathname, method: request.method, body: request.method === "GET" ? null : await request.json()})
  if (calls.length === 3) return Response.json({code: 409, message: "version conflict", data: null}, {status: 409})
  return Response.json({code: 0, data: {runtime: "CODEX", observed_ids: [], manual_ids: ["proxy"], families: {proxy: "google"}, version: 3}})
 }})
 try {
  const api = createModelCatalogApi(createIamApi(`http://127.0.0.1:${server.port}`))
  const row = await api.get("9007199254740993", "CODEX")
  const update = {expected_version: row.version, model_ids: ["proxy"], families: {proxy: "google"}}
  await api.replace("9007199254740993", "CODEX", update)
  expect(calls).toEqual([{path: "/machines/9007199254740993/model-catalogs/CODEX", method: "GET", body: null}, {path: "/machines/9007199254740993/model-catalogs/CODEX/manual-models", method: "PUT", body: update}])
  await expect(api.replace("9007199254740993", "CODEX", update)).rejects.toThrow("version conflict")
  expect(() => api.get("../foreign", "CODEX")).toThrow()
 } finally {server.stop(true)}
})
