import {expect, test} from "bun:test"
import {createIamApi} from "../../lib/iam-api"
import {createEnrollmentApi} from "./enrollment-api"

test("enrollment inspection looks up a code and decisions bind the exact tenant, ID and confirmed name", async () => {
  const calls: {path: string; method: string; body: unknown}[] = []
  const server = Bun.serve({port: 0, async fetch(request) {
    calls.push({path: new URL(request.url).pathname + new URL(request.url).search, method: request.method, body: request.method === "GET" ? null : await request.json()})
    return Response.json({code: 0, data: {id: "9007199254740993", state: "PENDING"}})
  }})
  try {
    const api = createEnrollmentApi(createIamApi(`http://127.0.0.1:${server.port}`))
    await api.settings("7", "WINDOWS")
    const found = await api.lookup("7", "abcd-efgh")
    expect(found.id).toBe("9007199254740993")
    const status = await api.status("7", "9007199254740993")
    expect(status.id).toBe("9007199254740993")
    await api.decide("7", status.id, "ABCD-EFGH", false)
    await api.decide("7", status.id, "ABCD-EFGH", true, "build-01")
    expect(calls).toEqual([
      {path: "/tenants/7/machine-enrollment-settings?os=WINDOWS", method: "GET", body: null},
      {path: "/tenants/7/machine-enrollments?user_code=ABCD-EFGH", method: "GET", body: null},
      {path: "/tenants/7/machine-enrollments/9007199254740993", method: "GET", body: null},
      {path: "/tenants/7/machine-enrollments/9007199254740993/decision", method: "PUT", body: {user_code: "ABCD-EFGH", approve: false}},
      {path: "/tenants/7/machine-enrollments/9007199254740993/decision", method: "PUT", body: {user_code: "ABCD-EFGH", approve: true, name: "build-01"}},
    ])
    expect(() => api.status("7", "../secret")).toThrow()
  } finally {server.stop(true)}
})
