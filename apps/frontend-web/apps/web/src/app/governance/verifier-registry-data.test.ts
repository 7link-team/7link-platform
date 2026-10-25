import { afterEach, expect, it } from "bun:test"
import { createIamApi } from "../../lib/iam-api"
import { verifierFormDefinition, verifierFormValues, verifierRegistryAPI, type Verifier } from "./verifier-registry-data"

const row: Verifier = {id: "9007199254740993", key: "unit-tests", name: "Tests", layer: "AUTOMATED", state: "ACTIVE", required: true, applies_to: {work_item_types: ["BUG"], risk_levels: ["HIGH"], project_ids: ["9007199254740995"]}, version: 4, current_version_no: 2, current: {id: "9007199254740997", version_no: 2, spec: {argv: ["tool", "literal argument with spaces", "$(never execute)"], env: {TEST_VALUE: "a=b"}, report_format: "NONE", stdout_tail_bytes: 8192}, timeout_ms: 1000, change_note: "Original", created_by: "9007199254740999", created_at: "2026-10-23T00:00:00Z"}}
let server: ReturnType<typeof Bun.serve> | undefined
afterEach(() => server?.stop(true))
it("round trips command boundaries, environment values and exact project IDs without shell tokenization", () => {
  const definition = verifierFormDefinition(verifierFormValues(row))
  expect(definition.spec.argv).toEqual(row.current.spec.argv)
  expect(definition.spec.env).toEqual({TEST_VALUE: "a=b"})
  expect(definition.applies_to.project_ids).toEqual(["9007199254740995"])
  expect(definition.timeout_ms).toBe(1000)
  expect(definition.change_note).toBe("")
  for (const value of ["../7", "0", "9223372036854775808", "9&tenant=1"]) expect(() => verifierFormDefinition({...verifierFormValues(row), project_ids: value})).toThrow()
  expect(() => verifierFormDefinition({...verifierFormValues(row), env: "bad-key=value"})).toThrow()
  const human = verifierFormDefinition({...verifierFormValues(row), layer: "HUMAN", instructions: "Inspect actual output", eligible_role_keys: "reviewer, delivery_lead"})
  expect(human.spec).toEqual({instructions: "Inspect actual output", eligible_role_keys: ["reviewer", "delivery_lead"]})
  const ai = verifierFormDefinition({...verifierFormValues(row), layer: "AI_ASSISTED", agent_id: "9007199254741001", prompt_template: "Review exact result", max_cost_micros: 1000})
  expect(ai.spec).toEqual({agent_id: "9007199254741001", prompt_template: "Review exact result", max_cost_micros: 1000})
})
it("uses native scoped list/global owned routes and preserves replay/CAS headers and abort", async () => {
  const requests: {method: string; path: string; search: URLSearchParams; headers: Headers; body: Record<string, unknown> | null}[] = []
  server = Bun.serve({port: 0, async fetch(request) {const url = new URL(request.url); requests.push({method: request.method, path: url.pathname, search: url.searchParams, headers: request.headers, body: request.method === "GET" ? null : await request.json() as Record<string, unknown>}); return Response.json({code: 0, data: request.method === "GET" ? {items: [row], has_more: false} : row})}})
  const api = verifierRegistryAPI("7", createIamApi(server.url.toString().replace(/\/$/, "")))
  await api.list({work_item_type: "BUG", risk_level: "HIGH", project_id: "9007199254740995"})
  await api.versions(row.id, "9007199254740997")
  const definition = verifierFormDefinition(verifierFormValues(row))
  for (let retry = 0; retry < 2; retry++) expect((await api.save(definition, row, "same-exact-attempt")).id).toBe(row.id)
  await api.save(definition, null, "create-attempt")
  expect(requests[0]?.path).toBe("/tenants/7/verifiers")
  expect(requests[0]?.search.get("project_id")).toBe("9007199254740995")
  expect(requests[1]?.path).toBe(`/verifiers/${row.id}/versions`)
  expect(requests[1]?.search.get("cursor")).toBe("9007199254740997")
  for (const request of requests.slice(2, 4)) {expect(request.method).toBe("PATCH"); expect(request.path).toBe(`/verifiers/${row.id}`); expect(request.headers.get("If-Match")).toBe('"4"'); expect(request.headers.get("Idempotency-Key")).toBe("same-exact-attempt"); expect(request.body).not.toHaveProperty("key"); expect(request.body).not.toHaveProperty("layer")}
  expect(requests[4]?.path).toBe("/tenants/7/verifiers"); expect(requests[4]?.body?.key).toBe("unit-tests")
  const controller = new AbortController(); controller.abort(); await expect(api.list({}, controller.signal)).rejects.toThrow()
  expect(() => api.get("../private")).toThrow()
})
