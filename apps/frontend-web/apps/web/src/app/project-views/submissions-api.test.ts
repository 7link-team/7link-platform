import { expect, it } from "bun:test"
import { createIamApi } from "../../lib/iam-api"
import { createSubmissionsApi, type SubmissionDetail, type SubmissionQuestion } from "./submissions-api"

it("keeps submission IDs exact, fences project responses and sends only the versioned text answer", async () => {
  const id = "9007199254740993"
  const question: SubmissionQuestion = { round_id: "9007199254740995", round_version: 2, number: 1, text: "Timezone?", state: "SENT" }
  const detail: SubmissionDetail = { id, project_id: "7", ref_no: 42, status: "CLARIFYING", kind: "NEW", version: 2, received_at: "2026-10-22T00:00:00Z", updated_at: "2026-10-22T00:00:00Z", open_questions_count: 1, questions: [question], deliveries: [], downloads_available: false, change_view_available: false }
  let response: unknown = { items: [detail], has_more: true, next_cursor: "opaque+/=" }
  const writes: { url: string; method: string; headers: Headers; body: unknown }[] = []
  const server = Bun.serve({ port: 0, async fetch(request) { writes.push({ url: request.url, method: request.method, headers: request.headers, body: request.method === "PUT" ? await request.json() : null }); return Response.json({ code: 0, data: response }) } })
  try {
    const api = createSubmissionsApi(createIamApi(`http://127.0.0.1:${server.port}`))
    const page = await api.list("1", "7", "opaque+/=")
    expect(page.items?.[0]?.id).toBe(id)
    expect(new URL(writes[0]!.url).searchParams.get("cursor")).toBe("opaque+/=")
    expect(new URL(writes[0]!.url).searchParams.get("project_id")).toBe("7")
    response = detail
    expect(await api.get("1", "7", id)).toEqual(detail)
    response = { ...detail, project_id: "8" }
    await expect(api.get("1", "7", id)).rejects.toThrow("Invalid submission project")
    response = { id: question.round_id, version: 3, state: "ANSWERED" }
    await api.answer("1", id, question, "UTC", "stable-key")
    const write = writes.at(-1)!
    expect(new URL(write.url).pathname).toBe(`/tenants/1/intake-submissions/${id}/clarification-rounds/${question.round_id}/answers/1`)
    expect(write.method).toBe("PUT")
    expect(write.headers.get("If-Match")).toBe('"2"')
    expect(write.headers.get("Idempotency-Key")).toBe("stable-key")
    expect(write.body).toEqual({ answer_text: "UTC" })
    const count = writes.length
    expect(() => api.answer("1", id, { ...question, state: "ANSWERED" }, "UTC", "key")).toThrow()
    expect(writes).toHaveLength(count)
  } finally { await server.stop(true) }
})

it("uses completed native upload file IDs and never sends invalid attachment sets", async () => {
  const calls: { method: string; path: string; body: unknown }[] = []
  let noRoot = false
  const server = Bun.serve({ port: 0, async fetch(request) {
    const path = new URL(request.url).pathname
    let body: unknown = null
    if (request.method === "POST") {
      const form = await request.formData()
      body = { parent: form.get("parent_id"), name: (form.get("file") as File).name }
    } else if (request.method === "PUT") body = await request.json()
    calls.push({ method: request.method, path, body })
    return Response.json({ code: 0, data: request.method === "POST" ? { id: "9007199254740999", kind: "file" } : request.method === "PUT" ? { id: "71", version: 2, state: "OPEN" } : { items: noRoot ? [] : [{ id: "70", kind: "directory" }] } })
  } })
  try {
    const api = createSubmissionsApi(createIamApi(`http://127.0.0.1:${server.port}`))
    const uploaded = await api.upload("7", new File(["evidence"], "evidence.txt"), "No project root")
    expect(uploaded).toBe("9007199254740999")
    expect(calls[1]).toEqual({ method: "POST", path: "/entities/7/files", body: { parent: "70", name: "evidence.txt" } })
    const question: SubmissionQuestion = { round_id: "71", round_version: 1, number: 1, text: "Evidence?", state: "SENT" }
    await api.answer("1", "50", question, "Evidence", "stable-key", undefined, [uploaded])
    expect(calls.at(-1)?.body).toEqual({ answer_text: "Evidence", upload_ids: [uploaded] })
    const count = calls.length
    for (const ids of [[uploaded, uploaded], ["0"], ["1", "2", "3", "4", "5", "6"]]) expect(() => api.answer("1", "50", question, "Evidence", "key", undefined, ids)).toThrow()
    expect(calls).toHaveLength(count)
    noRoot = true
    await expect(api.upload("7", new File(["evidence"], "evidence.txt"), "No project root")).rejects.toThrow("No project root")
    expect(calls.at(-1)?.method).toBe("GET")
  } finally { await server.stop(true) }
})

it("downloads the exact question attachment and surfaces current access denial", async () => {
  let denied = false
  let path = ""
  const server = Bun.serve({ port: 0, fetch(request) {
    path = new URL(request.url).pathname
    return denied ? Response.json({code:403,message:"Revoked"},{status:403}) : new Response("exact attachment bytes", { headers: { "Content-Type": "text/plain" } })
  } })
  try {
    const api = createSubmissionsApi(createIamApi(`http://127.0.0.1:${server.port}`))
    const question: SubmissionQuestion = { round_id: "71", round_version: 2, number: 1, text: "Evidence?", state: "ANSWERED" }
    expect(await (await api.download("1", "50", question, "901")).text()).toBe("exact attachment bytes")
    expect(path).toBe("/tenants/1/intake-submissions/50/clarification-rounds/71/answers/1/attachments/901/content")
    denied = true
    await expect(api.download("1", "50", question, "901")).rejects.toThrow("Revoked")
    expect(() => api.download("1", "50", question, "0")).toThrow()
    expect(() => api.download("1", "50", { ...question, number: 0 }, "901")).toThrow()
  } finally { await server.stop(true) }
})
