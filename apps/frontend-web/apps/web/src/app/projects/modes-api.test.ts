import {expect, test} from "bun:test"
import {createIamApi} from "../../lib/iam-api"
import {createProjectModesApi, modeNeedsProof, modeTargets, type TeamMode} from "./modes-api"

test("project mode controls preserve exact version, scoped route, proof and retry key over HTTP", async () => {
  const requests: {url:string; headers:Headers}[] = [], bodies: unknown[] = []
  const server = Bun.serve({port: 0, async fetch(request) {
    requests.push({url:request.url,headers:new Headers(request.headers)})
    if (request.method === "POST") bodies.push(await request.json())
    return Response.json({code: 0, data: request.method === "GET" ? [] : {mode_change_id: "19"}, meta: {page: {total: 21}}})
  }})
  try {
    const api = createProjectModesApi(createIamApi(`http://127.0.0.1:${server.port}`, () => "1"))
    await api.criteria("9007199254740993")
    expect((await api.history("9007199254740993", 10)).total).toBe(21)
    await api.change("9007199254740993", 8, "AUTONOMOUS", "A deliberate mode change", false, "same-key", "proof")
    await api.change("9007199254740993", 8, "HYBRID", "A deliberate takeover", true, "takeover-key", "renewed-proof")
    expect(new URL(requests[0]!.url).pathname).toEndWith("/9007199254740993/project/mode-preconditions")
    expect(new URL(requests[1]!.url).searchParams.get("offset")).toBe("10")
    expect(requests[2]!.headers.get("If-Match")).toBe('"8"')
    expect(requests[2]!.headers.get("Idempotency-Key")).toBe("same-key")
    expect(bodies[0]).toEqual({to_mode: "AUTONOMOUS", reason: "A deliberate mode change", reauth_proof: "proof"})
    expect(new URL(requests[3]!.url).pathname).toEndWith("/project/takeovers")
    expect(bodies[1]).toEqual({reason: "A deliberate takeover", reauth_proof: "renewed-proof"})
    expect(() => api.change("3", 0, "HYBRID", "Valid long reason", false, "x")).toThrow()
    expect(() => api.change("3", 1, "HYBRID", "short", false, "x")).toThrow()
    expect(() => api.criteria("3/../4")).toThrow()
    expect(requests).toHaveLength(4)
  } finally { server.stop(true) }
})

test("mode targets and proof rules match the guarded state machine", () => {
  const modes: TeamMode[] = ["HUMAN_ONLY", "HYBRID", "AUTONOMOUS"]
  for (const current of modes) for (const target of modes) {
    expect(modeTargets(current).includes(target)).toBe(target !== current && !(current === "HUMAN_ONLY" && target === "AUTONOMOUS"))
    expect(modeNeedsProof(current, target, false)).toBe(target === "AUTONOMOUS" || (current === "AUTONOMOUS" && target === "HUMAN_ONLY"))
    expect(modeNeedsProof(current, target, true)).toBe(true)
  }
})
