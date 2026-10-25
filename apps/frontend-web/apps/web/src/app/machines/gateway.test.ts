import { expect, it } from "bun:test"
import { createIamApi } from "../../lib/iam-api"

it("gateway commands bind tenant, version and idempotency without sending read-only facts or plaintext keys", async () => {
  const calls: {path: string; method: string; version: string | null; key: string | null; body: unknown}[] = []
  const server = Bun.serve({port: 0, async fetch(request) {
    const raw = await request.text()
    calls.push({path: new URL(request.url).pathname, method: request.method, version: request.headers.get("If-Match"), key: request.headers.get("Idempotency-Key"), body: raw ? JSON.parse(raw) : null})
    return Response.json({code: 0, data: {}})
  }})
  try {
    const api = createIamApi(`http://127.0.0.1:${server.port}`, () => "wrong-tenant")
    const view = {key_ref: "42", enabled: false, default_model: "", providers: [], models: [], budgets: [], version: 4, revision: 4, available: false, reason: "RUNNER_GATEWAY_UNAVAILABLE"}
    await api.gatewayConfig("7")
    await api.saveGatewayConfig("7", 4, view)
    await api.createGatewayKey("7", "key-command")
    await api.createGatewayRequest("7", "model-id", "request-command", "20", 1000, 2000)
    await api.gatewayRequests("7")
    expect(calls).toEqual([
      {path: "/tenants/7/model-gateway/config", method: "GET", version: null, key: null, body: null},
      {path: "/tenants/7/model-gateway/config", method: "PUT", version: '"4"', key: null, body: {key_ref: "42", enabled: false, default_model: "", providers: [], models: [], budgets: []}},
      {path: "/tenants/7/model-gateway/keys", method: "POST", version: null, key: "key-command", body: null},
      {path: "/tenants/7/model-gateway/requests", method: "POST", version: null, key: "request-command", body: {model: "model-id", project_id: "20", max_input_tokens: 1000, max_output_tokens: 2000}},
      {path: "/tenants/7/model-gateway/requests", method: "GET", version: null, key: null, body: null},
    ])
    expect(JSON.stringify(calls)).not.toContain("secret")
  } finally {await server.stop(true)}
})

it("imports provider credentials only to the explicit tenant sealing endpoint", async () => {
  const calls: {path: string; key: string | null; body: unknown}[] = []
  const server = Bun.serve({port: 0, async fetch(request) {
    calls.push({path: new URL(request.url).pathname, key: request.headers.get("Idempotency-Key"), body: await request.json()})
    return Response.json({code: 0, data: {id: "42", last4: "test"}}, {status: 201})
  }})
  try {
    const api = createIamApi(`http://127.0.0.1:${server.port}`, () => "wrong-tenant")
    const result = await api.importGatewayProviderKey("7", "provider-test", "sealed-import")
    expect(calls).toEqual([{path: "/tenants/7/model-gateway/provider-keys", key: "sealed-import", body: {secret: "provider-test"}}])
    expect(result.id).toBe("42")
    expect(JSON.stringify(result)).not.toContain("provider-test")
  } finally {await server.stop(true)}
})
