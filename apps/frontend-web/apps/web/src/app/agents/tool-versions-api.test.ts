import { expect, it } from "bun:test"
import { createIamApi } from "../../lib/iam-api"
import { createToolVersionsApi } from "./tool-versions-api"

it("rotates a write-only credential with exact revision, bound proof and idempotency key", async () => {
  let request: { method: string; path: string; key: string | null; body: unknown } | undefined
  const server = Bun.serve({port: 0, async fetch(req) {
    request = {method: req.method, path: new URL(req.url).pathname, key: req.headers.get("Idempotency-Key"), body: await req.json()}
    return Response.json({code: 0, data: {set: true, last4: "1234", rotated_at: "2026-10-22T00:00:00Z", version: 5}})
  }})
  try {
    const api = createToolVersionsApi(createIamApi(`http://localhost:${server.port}`))
    const result = await api.credential("7", "9007199254740993", {version: 4, secret: "write-only-1234", reauth_proof: "target-bound-proof"}, "credential-key")
    expect(result.last4).toBe("1234")
    expect(request).toEqual({method: "PUT", path: "/tenants/7/agent-tools/9007199254740993/credential", key: "credential-key", body: {version: 4, secret: "write-only-1234", reauth_proof: "target-bound-proof"}})
    expect(result).not.toHaveProperty("secret")
  } finally {await server.stop(true)}
})

it("fences tool writes by tenant revision and idempotency while preserving immutable configuration inputs", async () => {
  const seen: {path: string; method: string; key: string | null; body: unknown}[] = []
  const server = Bun.serve({port: 0, async fetch(request) {
    seen.push({path: new URL(request.url).pathname, method: request.method, key: request.headers.get("Idempotency-Key"), body: request.method === "POST" ? await request.json() : null})
    return Response.json({code: 0, data: {id: "9007199254740997", version_no: 2}})
  }})
  try {
    const api = createToolVersionsApi(createIamApi(`http://localhost:${server.port}`))
    const published = await api.publish("7", "9007199254740993", {version: 4, transport: "STREAMABLE_HTTP", endpoint: "https://example.invalid/new", content_hash: "", change_note: "Reviewed endpoint replacement"}, "publish-key")
    expect(published.version_no).toBe(2)
    await api.state("7", "9007199254740993", {version: 5, version_no: 2, version_state: "REVOKED", reason: "Withdraw reviewed version"}, "revoke-key")
    await api.import("7", {entry_key: "local-tool", kind: "MCP_SERVER", title: "Local", transport: "STREAMABLE_HTTP", endpoint: "https://example.invalid/mcp", content_hash: "", from_local: {machine_id: "9007199254740995", runtime: "CODEX", scope: "home", name: "observed-tool"}}, "import-key")
    expect(seen.map(row => row.path)).toEqual(["/tenants/7/agent-tools/9007199254740993/versions", "/tenants/7/agent-tools/9007199254740993/state", "/tenants/7/agent-tools"])
    expect(seen.every(row => row.method === "POST")).toBe(true)
    expect(seen.map(row => row.key)).toEqual(["publish-key", "revoke-key", "import-key"])
    expect(seen[1]?.body).toEqual({version: 5, version_no: 2, version_state: "REVOKED", reason: "Withdraw reviewed version"})
    expect(seen[2]?.body).toMatchObject({from_local: {machine_id: "9007199254740995", runtime: "CODEX", name: "observed-tool", scope: "home"}})
  } finally {await server.stop(true)}
})

it("propagates revoked machine authority and cancels source/history reads", async () => {
  const seen: URL[] = []
  const server = Bun.serve({port: 0, fetch(request) {seen.push(new URL(request.url)); return Response.json({code: 142403, message: "Forbidden"}, {status: 403})}})
  try {
    const api = createToolVersionsApi(createIamApi(`http://localhost:${server.port}`))
    await expect(api.sources("8", "55")).rejects.toThrow()
    expect(seen[0]?.pathname).toBe("/tenants/8/agent-tool-sources")
    expect(seen[0]?.searchParams.get("machine_id")).toBe("55")
    const controller = new AbortController(); controller.abort()
    await expect(api.detail("8", "99", controller.signal)).rejects.toThrow()
    expect(seen).toHaveLength(1)
  } finally {await server.stop(true)}
})
