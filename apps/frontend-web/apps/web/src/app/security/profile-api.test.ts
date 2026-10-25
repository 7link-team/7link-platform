import { expect, it } from "bun:test"
import { createIamApi } from "../../lib/iam-api"
import { createSelfProfileApi, selfProfileRoute } from "./profile-api"

it("updates the exact self profile without tenant administration, preserves clearing, and propagates rejection", async () => {
  const principal = "9007199254740993"
  const requests: { path: string; method: string; body: unknown }[] = []
  let status = 200
  let responseID = principal
  const server = Bun.serve({ port: 0, async fetch(request) {
    requests.push({ path: new URL(request.url).pathname, method: request.method, body: request.method === "PATCH" ? await request.json() : null })
    return Response.json(status === 200 ? { code: 0, data: { id: responseID, display_name: "My nickname", email: "", phone: "+12025550123", email_verified: false, phone_verified: false } } : { code: 403, message: "Forbidden" }, { status })
  } })
  try {
    const api = createSelfProfileApi(createIamApi(`http://localhost:${server.port}`))
    const profile = await api.get(principal)
    expect(profile.id).toBe(principal)
    await api.update(principal, { display_name: "My nickname", email: "" })
    expect(requests).toEqual([
      { path: `/principals/${principal}/profile`, method: "GET", body: null },
      { path: `/principals/${principal}/profile`, method: "PATCH", body: { display_name: "My nickname", email: "" } },
    ])
    responseID = "999"
    await expect(api.get(principal)).rejects.toThrow("Invalid self profile response")
    await expect(api.update(principal, { phone: "" })).rejects.toThrow("Invalid self profile response")
    status = 403
    await expect(api.update(principal, { display_name: "Denied" })).rejects.toThrow()
    const controller = new AbortController(); controller.abort()
    await expect(api.get(principal, controller.signal)).rejects.toThrow()
    expect(requests).toHaveLength(5)
    for (const value of ["0", "../other", "1/2", "1e3"]) expect(() => selfProfileRoute(value)).toThrow()
  } finally { await server.stop(true) }
})
